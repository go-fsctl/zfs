// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"os"
	"testing"
)

// These integration tests drive the live ZFS kernel via /dev/zfs. They are
// skipped automatically when /dev/zfs is absent (i.e. everywhere except the
// disposable ZFS guest). They expect a pool whose name is in ZFS_TEST_POOL
// (default "testpool") to already be imported.
//
// Run inside the guest as root:
//
//	ZFS_TEST_POOL=testpool sudo -E go test -run Integration -v ./...

func testPool() string {
	if p := os.Getenv("ZFS_TEST_POOL"); p != "" {
		return p
	}
	return "testpool"
}

func requireKernel(t *testing.T) *Handle {
	t.Helper()
	if !Available() {
		t.Skip("/dev/zfs not present; skipping kernel integration test")
	}
	h, err := Open()
	if err != nil {
		t.Skipf("cannot open /dev/zfs (need root): %v", err)
	}
	return h
}

func TestIntegrationPoolConfigs(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()

	cfgs, err := h.PoolConfigs()
	if err != nil {
		t.Fatalf("PoolConfigs: %v", err)
	}
	t.Logf("imported pools: %d", len(cfgs))
	pool := testPool()
	cfg, ok := cfgs[pool]
	if !ok {
		t.Fatalf("pool %q not found; got %v", pool, keysOf(cfgs))
	}
	// Sanity-check a few well-known config keys.
	if name, ok := cfg["name"].(string); !ok || name != pool {
		t.Errorf("config name = %v, want %q", cfg["name"], pool)
	}
	if _, ok := cfg["pool_guid"].(uint64); !ok {
		t.Errorf("missing pool_guid (got %T)", cfg["pool_guid"])
	}
	if v, ok := cfg["version"]; ok {
		t.Logf("pool %q version=%v guid=%v", pool, v, cfg["pool_guid"])
	}
}

func TestIntegrationSnapshot(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()
	pool := testPool()
	snap := pool + "@gofsctl_snap1"
	if err := h.Snapshot(pool, []string{snap}); err != nil {
		t.Fatalf("Snapshot %q: %v", snap, err)
	}
	t.Logf("created snapshot %s", snap)
}

func TestIntegrationCreateFilesystem(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()
	ds := testPool() + "/gofsctl_ds1"
	if err := h.CreateFilesystem(ds); err != nil {
		t.Fatalf("CreateFilesystem %q: %v", ds, err)
	}
	t.Logf("created filesystem %s", ds)
	// Verify via the read path.
	if _, err := h.ObjsetStats(ds); err != nil {
		t.Errorf("ObjsetStats %q after create: %v", ds, err)
	}
}

func keysOf(m map[string]Nvlist) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// TestIntegrationPoolLifecycle exercises the full pure-Go pool + dataset
// lifecycle against the live kernel. It is destructive and self-contained: it
// creates its own file-backed pool (it does NOT touch ZFS_TEST_POOL) and tears
// it down at the end. Requires root and two writable backing files under
// $ZFS_TEST_DIR (default /var/tmp).
//
//	sudo -E ZFS_TEST_DIR=/var/tmp go test -run PoolLifecycle -v ./...
func TestIntegrationPoolLifecycle(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()

	dir := os.Getenv("ZFS_TEST_DIR")
	if dir == "" {
		dir = "/var/tmp"
	}
	const name = "gofsctl_itpool"
	d0 := dir + "/gofsctl_it_d0.img"

	// Backing file must pre-exist and be >= 64MiB.
	f, err := os.OpenFile(d0, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		t.Skipf("cannot create backing file %s: %v", d0, err)
	}
	if err := f.Truncate(256 << 20); err != nil {
		f.Close()
		t.Fatalf("truncate %s: %v", d0, err)
	}
	f.Close()
	defer os.Remove(d0)

	// Best-effort cleanup of any leftover from a previous failed run.
	_ = h.PoolDestroy(name)

	root := Vdev{Type: VDEV_TYPE_ROOT, Children: []Vdev{{Type: VDEV_TYPE_FILE, Path: d0}}}
	if err := h.PoolCreate(name, root, nil); err != nil {
		t.Fatalf("PoolCreate: %v", err)
	}
	t.Logf("PoolCreate %s OK", name)
	defer func() { _ = h.PoolDestroy(name) }()

	// Pool shows up in PoolConfigs.
	cfgs, err := h.PoolConfigs()
	if err != nil {
		t.Fatalf("PoolConfigs: %v", err)
	}
	cfg, ok := cfgs[name]
	if !ok {
		t.Fatalf("created pool %q not in PoolConfigs", name)
	}

	// Pool properties read back.
	pp, err := h.PoolGetProps(name)
	if err != nil {
		t.Fatalf("PoolGetProps: %v", err)
	}
	if _, ok := pp["size"]; !ok {
		t.Errorf("PoolGetProps missing size: %v", pp)
	}

	// Dataset create + property set/get.
	ds := name + "/ds1"
	if err := h.CreateFilesystem(ds); err != nil {
		t.Fatalf("CreateFilesystem: %v", err)
	}
	// atime is an INDEX prop -> uint64 enum (0 = off). quota is a NUMBER prop.
	if err := h.SetProp(ds, Nvlist{"atime": uint64(0)}); err != nil {
		t.Fatalf("SetProp atime: %v", err)
	}
	if err := h.SetProp(ds, Nvlist{"quota": uint64(64 << 20)}); err != nil {
		t.Fatalf("SetProp quota: %v", err)
	}
	props, err := h.GetProps(ds)
	if err != nil {
		t.Fatalf("GetProps: %v", err)
	}
	if v, _ := props["quota"].(uint64); v != 64<<20 {
		t.Errorf("quota = %v, want %d", props["quota"], 64<<20)
	}
	if v, _ := props["atime"].(uint64); v != 0 {
		t.Errorf("atime = %v, want 0", props["atime"])
	}

	// Rename, snapshot, destroy snapshot, destroy dataset.
	ds2 := name + "/ds2"
	if err := h.Rename(ds, ds2, false); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	snap := ds2 + "@s1"
	if err := h.Snapshot(name, []string{snap}); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := h.Destroy(snap, false); err != nil {
		t.Fatalf("Destroy snapshot: %v", err)
	}
	if err := h.Destroy(ds2, false); err != nil {
		t.Fatalf("Destroy dataset: %v", err)
	}

	// Export then re-import from the captured config.
	if err := h.PoolExport(name, false, false); err != nil {
		t.Fatalf("PoolExport: %v", err)
	}
	if _, err := h.PoolImport(name, cfg); err != nil {
		t.Fatalf("PoolImport: %v", err)
	}
	t.Logf("export/import round-trip OK")

	// PoolDestroy (the deferred cleanup also runs, harmlessly).
	if err := h.PoolDestroy(name); err != nil {
		t.Fatalf("PoolDestroy: %v", err)
	}
	t.Logf("PoolDestroy %s OK", name)
}
