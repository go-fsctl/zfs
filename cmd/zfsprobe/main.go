// SPDX-License-Identifier: BSD-3-Clause
//
// # Copyright (c) 2026, go-fsctl
//
// zfsprobe is a live demonstration of github.com/go-fsctl/zfs driving the ZFS
// kernel purely via /dev/zfs ioctls (no cgo, no libzfs, no CLI). It creates a
// throwaway file-backed pool, runs the full dataset + pool lifecycle on it,
// and tears it all down again.
//
// Run as root inside a disposable ZFS guest:
//
//	sudo zfsprobe [pool-name] [backing-file]
package main

import (
	"fmt"
	"os"

	"github.com/go-fsctl/zfs"
)

func main() {
	pool := "gofsctl_probe"
	backing := "/var/tmp/gofsctl_probe.img"
	if len(os.Args) > 1 {
		pool = os.Args[1]
	}
	if len(os.Args) > 2 {
		backing = os.Args[2]
	}

	// Backing vdev: a 256 MiB sparse file.
	f, err := os.OpenFile(backing, os.O_RDWR|os.O_CREATE, 0600)
	check("create backing file", err)
	check("size backing file", f.Truncate(256<<20))
	f.Close()
	defer os.Remove(backing)

	h, err := zfs.Open()
	check("open /dev/zfs", err)
	defer h.Close()
	fmt.Println("opened /dev/zfs (pure-Go ioctl path)")

	// POOL_CREATE: pure-Go pool creation from a file vdev tree.
	root := zfs.Vdev{Type: zfs.VDEV_TYPE_ROOT, Children: []zfs.Vdev{
		{Type: zfs.VDEV_TYPE_FILE, Path: backing},
	}}
	check("PoolCreate", h.PoolCreate(pool, root, nil))
	fmt.Printf("OK: created pool %q via ZFS_IOC_POOL_CREATE\n", pool)

	// Confirm via the read path and capture the config for re-import later.
	cfgs, err := h.PoolConfigs()
	check("PoolConfigs", err)
	cfg, ok := cfgs[pool]
	if !ok {
		fmt.Printf("FAIL: pool %q not listed via ioctl\n", pool)
		os.Exit(1)
	}
	pp, err := h.PoolGetProps(pool)
	check("PoolGetProps", err)
	fmt.Printf("OK: pool %q guid=%v size=%v health=%v\n",
		pool, cfg["pool_guid"], pp["size"], pp["health"])

	// Dataset create + property set/get.
	ds := pool + "/ds1"
	check("CreateFilesystem", h.CreateFilesystem(ds))
	check("SetProp atime", h.SetProp(ds, zfs.Nvlist{"atime": uint64(0)}))
	check("SetProp quota", h.SetProp(ds, zfs.Nvlist{"quota": uint64(64 << 20)}))
	props, err := h.GetProps(ds)
	check("GetProps", err)
	fmt.Printf("OK: %s atime=%v quota=%v\n", ds, props["atime"], props["quota"])

	// Rename, snapshot, destroy.
	ds2 := pool + "/ds2"
	check("Rename", h.Rename(ds, ds2, false))
	snap := ds2 + "@s1"
	check("Snapshot", h.Snapshot(pool, []string{snap}))
	check("Destroy snapshot", h.Destroy(snap, false))
	check("Destroy dataset", h.Destroy(ds2, false))
	fmt.Printf("OK: renamed/snapshotted/destroyed %s\n", ds2)

	// Export then re-import from the captured config.
	check("PoolExport", h.PoolExport(pool, false, false))
	_, err = h.PoolImport(pool, cfg)
	check("PoolImport", err)
	fmt.Printf("OK: export/import round-trip on %q\n", pool)

	// Tear down.
	check("PoolDestroy", h.PoolDestroy(pool))
	fmt.Printf("OK: destroyed pool %q\n", pool)
}

func check(what string, err error) {
	if err != nil {
		fmt.Printf("FAIL: %s: %v\n", what, err)
		os.Exit(1)
	}
}
