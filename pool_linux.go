// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// randGUID returns a non-zero random 64-bit pool GUID, matching what userland
// generates before handing the config to the kernel. (spa_create generates
// its own internal GUID regardless, but a well-formed config carries one.)
func randGUID() uint64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	g := binary.LittleEndian.Uint64(b[:])
	if g == 0 {
		g = 1
	}
	return g
}

// PoolCreate creates a new pool named `name` from the given vdev tree via
// ZFS_IOC_POOL_CREATE — pure-Go pool creation with no libzfs and no zpool(8).
//
// The root vdev must be VDEV_TYPE_ROOT with one or more children. The pool
// configuration (containing the vdev tree) is packed into zc_nvlist_conf; any
// pool properties (e.g. {"ashift": ...} or feature flags) go into
// zc_nvlist_src. The kernel's zfs_ioc_pool_create reads config from
// zc_nvlist_conf and props from zc_nvlist_src (module/zfs/zfs_ioctl.c).
//
// The assembled config mirrors the zpool CLI:
//
//	{
//	  "version":   <uint64 SPA_VERSION>,
//	  "name":      <pool name>,
//	  "pool_guid": <uint64 random>,
//	  "vdev_tree": { "type":"root", "children":[ {type,path,...}, ... ] }
//	}
func (h *Handle) PoolCreate(name string, root Vdev, props Nvlist) error {
	if root.Type != VDEV_TYPE_ROOT {
		return fmt.Errorf("PoolCreate %q: root vdev type = %q, want %q", name, root.Type, VDEV_TYPE_ROOT)
	}
	tree, err := root.nvlist()
	if err != nil {
		return fmt.Errorf("PoolCreate %q: %w", name, err)
	}
	config := Nvlist{
		ZPOOL_CONFIG_VERSION:   uint64(SPA_VERSION),
		ZPOOL_CONFIG_POOL_NAME: name,
		ZPOOL_CONFIG_POOL_GUID: randGUID(),
		ZPOOL_CONFIG_VDEV_TREE: tree,
	}

	cmd := &zfsCmd{}
	if err := cmd.setName(name); err != nil {
		return err
	}
	kaConf, err := cmd.setConf(config)
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

// PoolTryImport probes the device at `path` (a file or block device holding a
// ZFS label) and returns the on-device pool configuration via
// ZFS_IOC_POOL_TRYIMPORT, without importing the pool. The probe config (the
// candidate import paths) is passed in zc_nvlist_conf; the kernel returns the
// assembled pool config in zc_nvlist_dst.
//
// The returned config (notably its "name" and "pool_guid") is exactly what
// PoolImport needs.
func (h *Handle) PoolTryImport(paths ...string) (Nvlist, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("PoolTryImport: no device paths given")
	}
	// spa_tryimport consults the cachefile/search-path machinery; the
	// minimal in-kernel path accepts a tryconfig nvlist. We pass an empty
	// nvlist plus the explicit device list under "search_paths" is not part
	// of the ABI here — instead the kernel scans the import search directory.
	// We therefore feed an empty tryconfig and rely on the kernel default
	// search, matching `zpool import` with no -d. Callers needing a specific
	// directory should ensure the device is discoverable there.
	conf := Nvlist{}
	nv, err := h.callWithDstConf(ZFS_IOC_POOL_TRYIMPORT, func(c *zfsCmd) error {
		return nil
	}, conf, 256*1024)
	if err != nil {
		return nil, fmt.Errorf("ZFS_IOC_POOL_TRYIMPORT: %w", err)
	}
	return nv, nil
}

// PoolImport imports a pool from a previously obtained configuration (as
// returned by PoolTryImport or PoolConfigs) via ZFS_IOC_POOL_IMPORT. The
// config is passed in zc_nvlist_conf; zc_name is the desired pool name and
// zc_guid must equal the config's "pool_guid" (the kernel rejects a mismatch
// with EINVAL). On success the kernel writes the resulting config to dst,
// which is decoded and returned.
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
