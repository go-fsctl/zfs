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
