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

// PoolConfigs lists the currently imported pools by issuing
// ZFS_IOC_POOL_CONFIGS and decoding the returned nvlist. The result maps each
// pool name to its configuration nvlist (the same nvlist `zpool` consults).
//
// This proves the ioctl + nvlist DECODE path against the live kernel: no
// pool name is supplied in zc_name; the kernel packs every imported pool's
// config into zc_nvlist_dst.
func (h *Handle) PoolConfigs() (map[string]Nvlist, error) {
	nv, err := h.callWithDst(ZFS_IOC_POOL_CONFIGS, func(c *zfsCmd) error {
		// zc_cookie carries the generation count the caller last saw; 0
		// always returns the current set.
		c.setU64(offZcCookie, 0)
		return nil
	}, 64*1024)
	if err != nil {
		return nil, fmt.Errorf("ZFS_IOC_POOL_CONFIGS: %w", err)
	}
	out := make(map[string]Nvlist, len(nv))
	for name, v := range nv {
		if sub, ok := v.(Nvlist); ok {
			out[name] = sub
		}
	}
	return out, nil
}

// PoolNames is a convenience wrapper returning just the imported pool names.
func (h *Handle) PoolNames() ([]string, error) {
	cfgs, err := h.PoolConfigs()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(cfgs))
	for n := range cfgs {
		names = append(names, n)
	}
	return names, nil
}

// Snapshot atomically creates one or more snapshots via ZFS_IOC_SNAPSHOT.
//
// The kernel expects an nvlist in zc_nvlist_src shaped as:
//
//	{
//	  "snaps":  { "<pool>@<snap1>": <bool true>, ... },  // nvlist of names
//	  "props":  { ... }                                  // optional, omitted
//	}
//
// and zc_name set to the containing pool. All snapshots must live in the same
// pool. Each name must be a full "dataset@snap" path. This proves driving a
// MUTATING kernel op via a packed nvlist.
func (h *Handle) Snapshot(pool string, fullnames []string) error {
	if len(fullnames) == 0 {
		return fmt.Errorf("Snapshot: no snapshot names given")
	}
	snaps := make(Nvlist, len(fullnames))
	for _, n := range fullnames {
		snaps[n] = true // DATA_TYPE_BOOLEAN_VALUE; kernel only checks presence
	}
	src := Nvlist{"snaps": snaps}

	cmd := &zfsCmd{}
	if err := cmd.setName(pool); err != nil {
		return err
	}
	ka, err := cmd.setSrc(src)
	if err != nil {
		return err
	}
	// Provide a small dst buffer: on failure the kernel returns a per-snap
	// errors nvlist there. We size it modestly and ignore overflow.
	dst := make([]byte, 16*1024)
	cmd.setU64(offZcNvlistDst, uint64(uintptr(unsafe.Pointer(&dst[0]))))
	cmd.setU64(offZcNvlistDstSize, uint64(len(dst)))

	err = h.ioctl(ZFS_IOC_SNAPSHOT, cmd)
	runtime.KeepAlive(ka)
	runtime.KeepAlive(dst)
	if err != nil {
		return fmt.Errorf("ZFS_IOC_SNAPSHOT %v: %w", fullnames, err)
	}
	return nil
}

// CreateFilesystem creates a new ZFS filesystem dataset named `name` (e.g.
// "testpool/ds1") via ZFS_IOC_CREATE.
//
// The kernel expects zc_name = dataset name and an nvlist in zc_nvlist_src:
//
//	{ "type": <uint64 DMU_OST_ZFS> }   // (props would go under "props")
//
// This is the second MUTATING proof.
func (h *Handle) CreateFilesystem(name string) error {
	return h.create(name, DMU_OST_ZFS)
}

// CreateVolume creates a ZFS volume (zvol). volsize bytes are required as a
// property; for simplicity callers needing a zvol should set props — here we
// expose the filesystem path which is what the validation exercises.
func (h *Handle) create(name string, ostype int32) error {
	// The kernel reads "type" via fnvlist_lookup_int32 — it MUST be a
	// DATA_TYPE_INT32 nvpair (a uint64 yields ZFS_ERR_IOC_ARG_BADTYPE,
	// errno 1032).
	src := Nvlist{"type": ostype}

	cmd := &zfsCmd{}
	if err := cmd.setName(name); err != nil {
		return err
	}
	ka, err := cmd.setSrc(src)
	if err != nil {
		return err
	}
	err = h.ioctl(ZFS_IOC_CREATE, cmd)
	runtime.KeepAlive(ka)
	if err != nil {
		return fmt.Errorf("ZFS_IOC_CREATE %q: %w", name, err)
	}
	return nil
}

// ObjsetStats issues ZFS_IOC_OBJSET_STATS for a dataset and returns the
// decoded properties nvlist. Useful as an extra read-path proof and to verify
// a created dataset exists.
func (h *Handle) ObjsetStats(name string) (Nvlist, error) {
	nv, err := h.callWithDst(ZFS_IOC_OBJSET_STATS, func(c *zfsCmd) error {
		return c.setName(name)
	}, 64*1024)
	if err != nil {
		return nil, fmt.Errorf("ZFS_IOC_OBJSET_STATS %q: %w", name, err)
	}
	return nv, nil
}

// Available reports whether /dev/zfs is present and openable. Integration
// tests use this to skip when not running inside the ZFS guest.
func Available() bool {
	return unix.Access("/dev/zfs", unix.R_OK) == nil
}
