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
	"io"
	"os"

	"github.com/go-fsctl/zfs"
)

// handle is the subset of *zfs.Handle zfsprobe drives, so the whole lifecycle
// can be fault-injected in tests without a real /dev/zfs.
type handle interface {
	Close() error
	PoolCreate(name string, root zfs.Vdev, props zfs.Nvlist) error
	PoolConfigs() (map[string]zfs.Nvlist, error)
	PoolGetProps(name string) (map[string]zfs.Value, error)
	CreateFilesystem(name string) error
	SetProp(name string, props zfs.Nvlist) error
	GetProps(name string) (map[string]zfs.Value, error)
	Rename(old, newName string, recursive bool) error
	Snapshot(pool string, fullnames []string) error
	Destroy(name string, defer_ bool) error
	PoolExport(name string, force, hardforce bool) error
	PoolImport(name string, config zfs.Nvlist) (zfs.Nvlist, error)
	PoolDestroy(name string) error
}

// backingFile abstracts the throwaway image file zfsprobe sizes.
type backingFile interface {
	Truncate(int64) error
	Close() error
}

// Seams over the zfs package and the OS, overridable in tests. Production code
// uses the real implementations assigned here.
var (
	openHandle = func() (handle, error) { return zfs.Open() }
	openFile   = func(path string) (backingFile, error) {
		return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	}
	removeFile = os.Remove

	osExit           = os.Exit
	stdout io.Writer = os.Stdout
)

func main() { osExit(run(os.Args)) }

func run(args []string) int {
	pool := "gofsctl_probe"
	backing := "/var/tmp/gofsctl_probe.img"
	if len(args) > 1 {
		pool = args[1]
	}
	if len(args) > 2 {
		backing = args[2]
	}

	// Backing vdev: a 256 MiB sparse file.
	f, err := openFile(backing)
	if err != nil {
		return fail("create backing file", err)
	}
	if err := f.Truncate(256 << 20); err != nil {
		f.Close()
		return fail("size backing file", err)
	}
	f.Close()
	defer removeFile(backing)

	h, err := openHandle()
	if err != nil {
		return fail("open /dev/zfs", err)
	}
	defer h.Close()
	fmt.Fprintln(stdout, "opened /dev/zfs (pure-Go ioctl path)")

	// POOL_CREATE: pure-Go pool creation from a file vdev tree.
	root := zfs.Vdev{Type: zfs.VDEV_TYPE_ROOT, Children: []zfs.Vdev{
		{Type: zfs.VDEV_TYPE_FILE, Path: backing},
	}}
	if err := h.PoolCreate(pool, root, nil); err != nil {
		return fail("PoolCreate", err)
	}
	fmt.Fprintf(stdout, "OK: created pool %q via ZFS_IOC_POOL_CREATE\n", pool)

	// Confirm via the read path and capture the config for re-import later.
	cfgs, err := h.PoolConfigs()
	if err != nil {
		return fail("PoolConfigs", err)
	}
	cfg, ok := cfgs[pool]
	if !ok {
		fmt.Fprintf(stdout, "FAIL: pool %q not listed via ioctl\n", pool)
		return 1
	}
	pp, err := h.PoolGetProps(pool)
	if err != nil {
		return fail("PoolGetProps", err)
	}
	fmt.Fprintf(stdout, "OK: pool %q guid=%v size=%v health=%v\n",
		pool, cfg["pool_guid"], pp["size"], pp["health"])

	// Dataset create + property set/get.
	ds := pool + "/ds1"
	if err := h.CreateFilesystem(ds); err != nil {
		return fail("CreateFilesystem", err)
	}
	if err := h.SetProp(ds, zfs.Nvlist{"atime": uint64(0)}); err != nil {
		return fail("SetProp atime", err)
	}
	if err := h.SetProp(ds, zfs.Nvlist{"quota": uint64(64 << 20)}); err != nil {
		return fail("SetProp quota", err)
	}
	props, err := h.GetProps(ds)
	if err != nil {
		return fail("GetProps", err)
	}
	fmt.Fprintf(stdout, "OK: %s atime=%v quota=%v\n", ds, props["atime"], props["quota"])

	// Rename, snapshot, destroy.
	ds2 := pool + "/ds2"
	if err := h.Rename(ds, ds2, false); err != nil {
		return fail("Rename", err)
	}
	snap := ds2 + "@s1"
	if err := h.Snapshot(pool, []string{snap}); err != nil {
		return fail("Snapshot", err)
	}
	if err := h.Destroy(snap, false); err != nil {
		return fail("Destroy snapshot", err)
	}
	if err := h.Destroy(ds2, false); err != nil {
		return fail("Destroy dataset", err)
	}
	fmt.Fprintf(stdout, "OK: renamed/snapshotted/destroyed %s\n", ds2)

	// Export then re-import from the captured config.
	if err := h.PoolExport(pool, false, false); err != nil {
		return fail("PoolExport", err)
	}
	if _, err := h.PoolImport(pool, cfg); err != nil {
		return fail("PoolImport", err)
	}
	fmt.Fprintf(stdout, "OK: export/import round-trip on %q\n", pool)

	// Tear down.
	if err := h.PoolDestroy(pool); err != nil {
		return fail("PoolDestroy", err)
	}
	fmt.Fprintf(stdout, "OK: destroyed pool %q\n", pool)
	return 0
}

func fail(what string, err error) int {
	fmt.Fprintf(stdout, "FAIL: %s: %v\n", what, err)
	return 1
}
