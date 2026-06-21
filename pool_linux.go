// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// PoolCreate creates a new pool named `name` from the given vdev tree via
// ZFS_IOC_POOL_CREATE — pure-Go pool creation with no libzfs and no zpool(8).
//
// The root vdev must be VDEV_TYPE_ROOT with one or more children. The bare
// root vdev tree is packed into zc_nvlist_conf; any pool properties (e.g.
// {"ashift": ...} or feature flags) go into zc_nvlist_src. The kernel's
// zfs_ioc_pool_create reads the vdev tree from zc_nvlist_conf and props from
// zc_nvlist_src (module/zfs/zfs_ioctl.c).
//
// The vdev tree packed into zc_nvlist_conf is exactly:
//
//	{ "type":"root", "children":[ {type,path,is_log,...}, ... ] }
//
// (the kernel/CLI generate the pool GUID and version internally).
func (h *Handle) PoolCreate(name string, root Vdev, props Nvlist) error {
	if root.Type != VDEV_TYPE_ROOT {
		return fmt.Errorf("PoolCreate %q: root vdev type = %q, want %q", name, root.Type, VDEV_TYPE_ROOT)
	}
	// The kernel's zfs_ioc_pool_create passes zc_nvlist_conf straight to
	// spa_create() as the *root vdev tree* (its `nvroot` argument) — it is
	// NOT a wrapping config object with a "vdev_tree" member. The zpool CLI
	// likewise writes the bare nvroot into zc_nvlist_conf
	// (lib/libzfs/libzfs_pool.c zpool_create -> zcmd_write_conf_nvlist).
	tree, err := root.nvlist()
	if err != nil {
		return fmt.Errorf("PoolCreate %q: %w", name, err)
	}

	cmd := &zfsCmd{}
	if err := cmd.setName(name); err != nil {
		return err
	}
	kaConf, err := cmd.setConf(tree)
	if err != nil {
		return err
	}
	kaSrc, err := cmd.setSrc(props) // nil props => no src nvlist
	if err != nil {
		return err
	}
	err = h.ioctl(ZFS_IOC_POOL_CREATE, cmd)
	runtime.KeepAlive(kaConf)
	runtime.KeepAlive(kaSrc)
	if err != nil {
		return fmt.Errorf("ZFS_IOC_POOL_CREATE %q: %w", name, err)
	}
	return nil
}

// PoolDestroy destroys the imported pool `name` via ZFS_IOC_POOL_DESTROY. The
// pool must be imported; its devices are released. Only zc_name is consulted.
func (h *Handle) PoolDestroy(name string) error {
	cmd := &zfsCmd{}
	if err := cmd.setName(name); err != nil {
		return err
	}
	if err := h.ioctl(ZFS_IOC_POOL_DESTROY, cmd); err != nil {
		return fmt.Errorf("ZFS_IOC_POOL_DESTROY %q: %w", name, err)
	}
	return nil
}

// PoolExport exports (deactivates) the imported pool `name` via
// ZFS_IOC_POOL_EXPORT. force maps to zc_cookie and hardforce to zc_guid, per
// zfs_ioc_pool_export. After export the pool is no longer imported but its
// devices retain valid labels and can be re-imported.
func (h *Handle) PoolExport(name string, force, hardforce bool) error {
	cmd := &zfsCmd{}
	if err := cmd.setName(name); err != nil {
		return err
	}
	if force {
		cmd.setU64(offZcCookie, 1)
	}
	if hardforce {
		cmd.setU64(offZcGuid, 1)
	}
	if err := h.ioctl(ZFS_IOC_POOL_EXPORT, cmd); err != nil {
		return fmt.Errorf("ZFS_IOC_POOL_EXPORT %q: %w", name, err)
	}
	return nil
}

// PoolTryImport refines a candidate pool configuration via
// ZFS_IOC_POOL_TRYIMPORT and returns the kernel's assembled config, without
// importing the pool. The tryconfig is passed in zc_nvlist_conf; the kernel
// returns the assembled config in zc_nvlist_dst.
//
// IMPORTANT: the kernel's spa_tryimport (module/zfs/spa.c) does NOT scan
// devices itself — it requires the tryconfig to already contain at least
// ZPOOL_CONFIG_POOL_NAME and ZPOOL_CONFIG_POOL_STATE (and a vdev tree), i.e.
// a config that userland assembled by reading the on-disk vdev labels. An
// empty or device-path-only tryconfig yields EINVAL.
//
// This library does not yet decode the on-disk (XDR-encoded) vdev label, so it
// cannot build that tryconfig from a bare device path; callers that already
// hold a config (e.g. from PoolConfigs on a still-imported pool) can pass it
// here. For the common "export then re-import" flow, capture the config with
// PoolConfigs before PoolExport and feed it straight to PoolImport — see
// PoolImport.
func (h *Handle) PoolTryImport(tryconfig Nvlist) (Nvlist, error) {
	if tryconfig == nil {
		return nil, fmt.Errorf("PoolTryImport: nil tryconfig")
	}
	nv, err := h.callWithDstConf(ZFS_IOC_POOL_TRYIMPORT, func(c *zfsCmd) error {
		return nil
	}, tryconfig, 256*1024)
	if err != nil {
		return nil, fmt.Errorf("ZFS_IOC_POOL_TRYIMPORT: %w", err)
	}
	return nv, nil
}

// PoolImport imports a pool from a previously obtained configuration via
// ZFS_IOC_POOL_IMPORT. The config is passed in zc_nvlist_conf; zc_name is the
// desired pool name and zc_guid must equal the config's "pool_guid" (the
// kernel rejects a mismatch with EINVAL). On success the kernel writes the
// resulting config to dst, which is decoded and returned.
//
// The config must be a full pool config (carrying pool_guid and the vdev
// tree), such as the one PoolConfigs returns for a still-imported pool. A
// typical export/re-import round-trip is:
//
//	cfg := must(h.PoolConfigs())[name] // capture while imported
//	h.PoolExport(name, false, false)
//	h.PoolImport(name, cfg)            // re-import from the captured config
func (h *Handle) PoolImport(name string, config Nvlist) (Nvlist, error) {
	guid, ok := config[ZPOOL_CONFIG_POOL_GUID].(uint64)
	if !ok {
		return nil, fmt.Errorf("PoolImport %q: config missing uint64 pool_guid", name)
	}
	nv, err := h.callWithDstConf(ZFS_IOC_POOL_IMPORT, func(c *zfsCmd) error {
		if err := c.setName(name); err != nil {
			return err
		}
		c.setU64(offZcGuid, guid)
		return nil
	}, config, 256*1024)
	if err != nil {
		return nil, fmt.Errorf("ZFS_IOC_POOL_IMPORT %q: %w", name, err)
	}
	return nv, nil
}

// callWithDstConf is callWithDst plus a config nvlist packed into
// zc_nvlist_conf. Used by the import/tryimport ioctls, which take the pool
// config in conf and return a (potentially large) config nvlist in dst.
func (h *Handle) callWithDstConf(req uintptr, build func(*zfsCmd) error, conf Nvlist, dstSize uint64) (Nvlist, error) {
	for attempt := 0; attempt < 2; attempt++ {
		cmd := &zfsCmd{}
		if err := build(cmd); err != nil {
			return nil, err
		}
		kaConf, err := cmd.setConf(conf)
		if err != nil {
			return nil, err
		}
		dst := make([]byte, dstSize)
		cmd.setU64(offZcNvlistDst, uint64(uintptr(unsafe.Pointer(&dst[0]))))
		cmd.setU64(offZcNvlistDstSize, dstSize)

		err = h.ioctl(req, cmd)
		runtime.KeepAlive(kaConf)
		runtime.KeepAlive(dst)
		if err == unix.ENOMEM {
			need := cmd.getU64(offZcNvlistDstSize)
			if need > dstSize && need < 1<<30 {
				dstSize = need
				continue
			}
		}
		if err != nil {
			return nil, err
		}
		return DecodeNative(dst)
	}
	return nil, fmt.Errorf("ioctl dst buffer kept growing")
}
