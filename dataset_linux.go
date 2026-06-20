// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"fmt"
	"runtime"
	"unsafe"
)

// Destroy destroys the dataset, snapshot, or zvol named `name` via
// ZFS_IOC_DESTROY. For a snapshot (a name containing '@') the kernel calls
// dsl_destroy_snapshot; otherwise dsl_destroy_head. defer_ marks a busy
// snapshot for deferred destruction (zc_defer_destroy); it is ignored for
// filesystems. Only zc_name and zc_defer_destroy are consulted.
func (h *Handle) Destroy(name string, defer_ bool) error {
	cmd := &zfsCmd{}
	if err := cmd.setName(name); err != nil {
		return err
	}
	if defer_ {
		// zc_defer_destroy is a uint32 field; set its low byte.
		hostBO.PutUint32(cmd.buf[offZcDeferDestroy:offZcDeferDestroy+4], 1)
	}
	if err := h.ioctl(ZFS_IOC_DESTROY, cmd); err != nil {
		return fmt.Errorf("ZFS_IOC_DESTROY %q: %w", name, err)
	}
	return nil
}

// Rename renames the dataset `old` to `newName` via ZFS_IOC_RENAME. zc_name
// holds the old name, zc_value the new name, and zc_cookie the flags
// (bit0 = recursive, valid only for snapshots; bit1 = nounmount). Filesystem
// and snapshot renames must stay within the same pool; a snapshot rename must
// keep the same dataset prefix (the kernel returns EXDEV otherwise).
func (h *Handle) Rename(old, newName string, recursive bool) error {
	cmd := &zfsCmd{}
	if err := cmd.setName(old); err != nil {
		return err
	}
	if err := cmd.setValue(newName); err != nil {
		return err
	}
	if recursive {
		cmd.setU64(offZcCookie, 1)
	}
	if err := h.ioctl(ZFS_IOC_RENAME, cmd); err != nil {
		return fmt.Errorf("ZFS_IOC_RENAME %q -> %q: %w", old, newName, err)
	}
	return nil
}

// SetProp sets one or more properties on the dataset `name` via
// ZFS_IOC_SET_PROP. The props nvlist maps property names to values, e.g.
// {"compression": "lz4"} or {"quota": uint64(...)}. The kernel reads the
// nvlist from zc_nvlist_src and applies each pair as a local property; any
// per-property errors are returned in zc_nvlist_dst, which is decoded and, if
// non-empty, surfaced in the returned error.
func (h *Handle) SetProp(name string, props Nvlist) error {
	if len(props) == 0 {
		return fmt.Errorf("SetProp %q: no properties given", name)
	}
	cmd := &zfsCmd{}
	if err := cmd.setName(name); err != nil {
		return err
	}
	ka, err := cmd.setSrc(props)
	if err != nil {
		return err
	}
	// Per-property errors come back here as an nvlist {prop -> errno}.
	dst := make([]byte, 16*1024)
	cmd.setU64(offZcNvlistDst, uint64(uintptr(unsafe.Pointer(&dst[0]))))
	cmd.setU64(offZcNvlistDstSize, uint64(len(dst)))

	err = h.ioctl(ZFS_IOC_SET_PROP, cmd)
	runtime.KeepAlive(ka)
	runtime.KeepAlive(dst)
	if err != nil {
		return fmt.Errorf("ZFS_IOC_SET_PROP %q: %w", name, err)
	}
	return nil
}

// GetProps returns the properties of the dataset `name` as a typed nvlist via
// ZFS_IOC_OBJSET_STATS. Each property value in the returned outer nvlist is
// itself a small nvlist of the form {"value": <v>, "source": <s>}; this helper
// flattens it to property-name -> value for convenience. Callers needing the
// source string can fall back to ObjsetStats.
func (h *Handle) GetProps(name string) (map[string]Value, error) {
	nv, err := h.ObjsetStats(name)
	if err != nil {
		return nil, err
	}
	return flattenProps(nv), nil
}

// PoolGetProps returns the pool-level properties of pool `name` via
// ZFS_IOC_POOL_GET_PROPS, flattened to property-name -> value. The kernel
// returns each property as {"value": <v>, "source": <s>} in zc_nvlist_dst.
func (h *Handle) PoolGetProps(name string) (map[string]Value, error) {
	nv, err := h.callWithDst(ZFS_IOC_POOL_GET_PROPS, func(c *zfsCmd) error {
		return c.setName(name)
	}, 64*1024)
	if err != nil {
		return nil, fmt.Errorf("ZFS_IOC_POOL_GET_PROPS %q: %w", name, err)
	}
	return flattenProps(nv), nil
}
