// SPDX-License-Identifier: BSD-3-Clause
//
// # Copyright (c) 2026, go-fsctl
//
// zfsprobe is a live demonstration of github.com/go-fsctl/zfs driving the ZFS
// kernel purely via /dev/zfs ioctls (no cgo, no libzfs, no CLI). It lists
// imported pools, then creates a snapshot and a filesystem on the target pool.
package main

import (
	"fmt"
	"os"

	"github.com/go-fsctl/zfs"
)

func main() {
	pool := "testpool"
	if len(os.Args) > 1 {
		pool = os.Args[1]
	}

	h, err := zfs.Open()
	check("open /dev/zfs", err)
	defer h.Close()
	fmt.Println("opened /dev/zfs (pure-Go ioctl path)")

	// READ: ZFS_IOC_POOL_CONFIGS + native nvlist decode.
	cfgs, err := h.PoolConfigs()
	check("PoolConfigs", err)
	fmt.Printf("ZFS_IOC_POOL_CONFIGS: %d pool(s) imported:\n", len(cfgs))
	for name, cfg := range cfgs {
		fmt.Printf("  - %s  pool_guid=%v version=%v state=%v\n",
			name, cfg["pool_guid"], cfg["version"], cfg["state"])
	}
	if _, ok := cfgs[pool]; !ok {
		fmt.Printf("FAIL: pool %q not found via ioctl\n", pool)
		os.Exit(1)
	}
	fmt.Printf("OK: pool %q listed via pure-Go ioctl\n", pool)

	// WRITE 1: ZFS_IOC_SNAPSHOT (packed native nvlist).
	snap := pool + "@gofsctl_snap1"
	check("Snapshot", h.Snapshot(pool, []string{snap}))
	fmt.Printf("OK: created snapshot %s via ZFS_IOC_SNAPSHOT\n", snap)

	// WRITE 2: ZFS_IOC_CREATE.
	ds := pool + "/gofsctl_ds1"
	check("CreateFilesystem", h.CreateFilesystem(ds))
	fmt.Printf("OK: created filesystem %s via ZFS_IOC_CREATE\n", ds)

	// READ-BACK: ZFS_IOC_OBJSET_STATS on the new dataset.
	st, err := h.ObjsetStats(ds)
	check("ObjsetStats", err)
	fmt.Printf("OK: ObjsetStats(%s) returned %d props\n", ds, len(st))
}

func check(what string, err error) {
	if err != nil {
		fmt.Printf("FAIL: %s: %v\n", what, err)
		os.Exit(1)
	}
}
