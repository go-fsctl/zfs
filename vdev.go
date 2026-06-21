// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import "fmt"

// Vdev describes a single node in a pool's vdev tree. It maps onto the
// ZPOOL_CONFIG_* nvlist the kernel consumes from zc_nvlist_conf during
// ZFS_IOC_POOL_CREATE. A leaf is a "file" or "disk" with a Path; an interior
// node ("root", "mirror", "raidz") carries Children. Extra is merged verbatim
// for any keys this struct does not model (e.g. "nparity", "ashift").
//
// This type (and its nvlist rendering) is platform-neutral so configs can be
// built and tested anywhere; only the kernel ops that consume it are Linux.
type Vdev struct {
	Type     string // VDEV_TYPE_ROOT / _FILE / _DISK / _MIRROR / _RAIDZ
	Path     string // leaf vdevs only (absolute path to file or device)
	Children []Vdev // interior vdevs only
	Extra    Nvlist // optional additional config keys
}

// nvlist renders the Vdev (and its subtree) into the native config nvlist
// shape. Leaf file/disk vdevs carry {type, path[, whole_disk], is_log};
// interior vdevs carry {type, children:[...]}. This mirrors the nvlist the
// zpool CLI hands to ZFS_IOC_POOL_CREATE (cmd/zpool/zpool_vdev.c).
func (v Vdev) nvlist() (Nvlist, error) {
	if v.Type == "" {
		return nil, fmt.Errorf("vdev: empty type")
	}
	nv := Nvlist{ZPOOL_CONFIG_TYPE: v.Type}
	for k, val := range v.Extra {
		nv[k] = val
	}
	switch v.Type {
	case VDEV_TYPE_FILE, VDEV_TYPE_DISK:
		if v.Path == "" {
			return nil, fmt.Errorf("vdev %q: missing path", v.Type)
		}
		nv[ZPOOL_CONFIG_PATH] = v.Path
		if v.Type == VDEV_TYPE_DISK {
			if _, ok := nv[ZPOOL_CONFIG_WHOLE_DISK]; !ok {
				nv[ZPOOL_CONFIG_WHOLE_DISK] = uint64(0)
			}
		}
		// A bare leaf used as a top-level vdev is not a log device.
		if _, ok := nv[ZPOOL_CONFIG_IS_LOG]; !ok {
			nv[ZPOOL_CONFIG_IS_LOG] = uint64(0)
		}
	default:
		if len(v.Children) == 0 {
			return nil, fmt.Errorf("vdev %q: no children", v.Type)
		}
		kids := make([]Nvlist, 0, len(v.Children))
		for i, c := range v.Children {
			cn, err := c.nvlist()
			if err != nil {
				return nil, fmt.Errorf("child %d: %w", i, err)
			}
			kids = append(kids, cn)
		}
		nv[ZPOOL_CONFIG_CHILDREN] = kids
	}
	return nv, nil
}

// flattenProps reduces a ZFS properties nvlist (name -> {value, source, ...})
// to name -> value. Pairs that are not the nested {value:...} shape (e.g.
// scalar stats fields) are passed through unchanged so nothing is lost. Used
// by GetProps / PoolGetProps.
func flattenProps(nv Nvlist) map[string]Value {
	out := make(map[string]Value, len(nv))
	for k, v := range nv {
		if sub, ok := v.(Nvlist); ok {
			if val, ok := sub["value"]; ok {
				out[k] = val
				continue
			}
		}
		out[k] = v
	}
	return out
}
