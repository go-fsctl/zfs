// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"testing"

	"golang.org/x/sys/unix"
)

// This file drives every branch of the pool-admin kernel paths in
// scan_linux.go (scrub/resilver, trim, initialize, vdev attach/detach/set-state,
// reopen) through the ioctlFn / dstHook seams, fault-injecting success and each
// errno WITHOUT a real /dev/zfs, zfs module, or root. The genuine ioctls are
// exercised end to end by TestIntegrationPoolAdmin (skipped when /dev/zfs is
// absent).

// poolStatsConfig is the config nvlist a faked ZFS_IOC_POOL_STATS returns: a
// root vdev with one mirror of two file leaves plus a scan_stats array. It is
// the shape h.PoolStats decodes for vdev-guid resolution and ScanStatus.
func poolStatsConfig() Nvlist {
	ps := make([]uint64, 16)
	ps[pssFunc] = POOL_SCAN_SCRUB
	ps[pssState] = DSS_SCANNING
	ps[pssToExamine] = 1000
	ps[pssExamined] = 500
	return Nvlist{
		ZPOOL_CONFIG_POOL_NAME: "tank",
		ZPOOL_CONFIG_VDEV_TREE: Nvlist{
			ZPOOL_CONFIG_TYPE:       VDEV_TYPE_ROOT,
			ZPOOL_CONFIG_SCAN_STATS: ps,
			ZPOOL_CONFIG_CHILDREN: []Nvlist{
				{
					ZPOOL_CONFIG_TYPE: VDEV_TYPE_MIRROR,
					ZPOOL_CONFIG_CHILDREN: []Nvlist{
						{ZPOOL_CONFIG_TYPE: VDEV_TYPE_FILE, ZPOOL_CONFIG_PATH: "/tank/a.img", ZPOOL_CONFIG_GUID: uint64(111)},
						{ZPOOL_CONFIG_TYPE: VDEV_TYPE_FILE, ZPOOL_CONFIG_PATH: "/tank/b.img", ZPOOL_CONFIG_GUID: uint64(222)},
					},
				},
			},
		},
	}
}

// statThenOp wires a fake ioctlFn that serves poolStatsConfig() on the first
// call (the PoolStats lookup) and then runs op on every subsequent call. op
// receives the (1-based) op-call index so it can vary behaviour per call.
func statThenOp(t *testing.T, op func(call int, cmd *zfsCmd) error) {
	t.Helper()
	n := 0
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		n++
		if n == 1 {
			putDst(t, cmd, poolStatsConfig())
			return nil
		}
		return op(n-1, cmd)
	}
}

// ---- ScanPool / scrub wrappers / PoolStats / ScanStatus ----

func TestScanPoolBranches(t *testing.T) {
	defer snapshotSeams()()

	// setName error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().ScanPool(string(make([]byte, maxPathLen)), ScanScrub, ScanNormal); err == nil {
		t.Fatal("want setName error")
	}

	// Plain ioctl error (not one of the benign errnos).
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().ScanPool("tank", ScanScrub, ScanNormal); err == nil {
		t.Fatal("want ioctl error")
	}

	// ECANCELED while resuming a scrub (fn!=none, cmd=normal) -> benign no-op.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return unix.ECANCELED }
	if err := okHandle().ScanPool("tank", ScanScrub, ScanNormal); err != nil {
		t.Fatalf("ECANCELED resume should be no-op, got %v", err)
	}
	// ECANCELED that is NOT a resume still surfaces.
	if err := okHandle().ScanPool("tank", ScanNone, ScanNormal); err == nil {
		t.Fatal("want ECANCELED surfaced for cancel")
	}

	// ENOENT while pausing -> benign no-op.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return unix.ENOENT }
	if err := okHandle().ScanPool("tank", ScanScrub, ScanPause); err != nil {
		t.Fatalf("ENOENT pause should be no-op, got %v", err)
	}
	// ENOENT that is NOT a pause still surfaces.
	if err := okHandle().ScanPool("tank", ScanScrub, ScanNormal); err == nil {
		t.Fatal("want ENOENT surfaced for non-pause")
	}

	// Success, verifying zc_cookie/zc_flags carry func/cmd.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		if cmd.getU64(offZcCookie) != uint64(ScanResilver) || cmd.getU64(offZcFlags) != uint64(ScanNormal) {
			t.Errorf("cookie/flags = %d/%d, want %d/%d",
				cmd.getU64(offZcCookie), cmd.getU64(offZcFlags), ScanResilver, ScanNormal)
		}
		return nil
	}
	if err := okHandle().ScanPool("tank", ScanResilver, ScanNormal); err != nil {
		t.Fatalf("ScanPool success: %v", err)
	}
}

func TestScrubWrappers(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().ScrubStart("tank"); err != nil {
		t.Fatalf("ScrubStart: %v", err)
	}
	if err := okHandle().ScrubStop("tank"); err != nil {
		t.Fatalf("ScrubStop: %v", err)
	}
	if err := okHandle().ScrubPause("tank"); err != nil {
		t.Fatalf("ScrubPause: %v", err)
	}
	if err := okHandle().ResilverStart("tank"); err != nil {
		t.Fatalf("ResilverStart: %v", err)
	}
}

func TestPoolStatsAndScanStatus(t *testing.T) {
	defer snapshotSeams()()

	// PoolStats ioctl error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().PoolStats("tank"); err == nil {
		t.Fatal("want PoolStats ioctl error")
	}
	// ScanStatus surfaces the same error.
	if _, err := okHandle().ScanStatus("tank"); err == nil {
		t.Fatal("want ScanStatus error")
	}

	// Success: config carries scan_stats -> typed status.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, poolStatsConfig())
		return nil
	}
	nv, err := okHandle().PoolStats("tank")
	if err != nil || nv[ZPOOL_CONFIG_POOL_NAME] != "tank" {
		t.Fatalf("PoolStats = %v, %v", nv, err)
	}
	st, err := okHandle().ScanStatus("tank")
	if err != nil {
		t.Fatalf("ScanStatus: %v", err)
	}
	if st.Func != ScanScrub || st.State != ScanStateScanning || st.Percent() != 50 {
		t.Errorf("ScanStatus = %+v (pct %v)", st, st.Percent())
	}
}

// ---- vdevGUIDs / TrimPool / InitializePool ----

func TestVdevGUIDsBranches(t *testing.T) {
	defer snapshotSeams()()

	// PoolStats error propagates.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().vdevGUIDs("tank", nil); err == nil {
		t.Fatal("want PoolStats error")
	}

	// Empty list -> whole pool (collect leaves).
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, poolStatsConfig())
		return nil
	}
	g, err := okHandle().vdevGUIDs("tank", nil)
	if err != nil || len(g) != 2 {
		t.Fatalf("whole-pool guids = %v, %v", g, err)
	}

	// Named path, bare guid, and unknown vdev.
	g, err = okHandle().vdevGUIDs("tank", []string{"/tank/a.img", "999"})
	if err != nil || g["111"] != 111 || g["999"] != 999 {
		t.Fatalf("named guids = %v, %v", g, err)
	}
	if _, err := okHandle().vdevGUIDs("tank", []string{"/nope"}); err == nil {
		t.Fatal("want unknown-vdev error")
	}

	// Empty list but no leaves in config -> error.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{ZPOOL_CONFIG_VDEV_TREE: Nvlist{ZPOOL_CONFIG_TYPE: VDEV_TYPE_ROOT}})
		return nil
	}
	if _, err := okHandle().vdevGUIDs("tank", nil); err == nil {
		t.Fatal("want no-leaf-vdevs error")
	}
}

func TestTrimPoolBranches(t *testing.T) {
	defer snapshotSeams()()

	// vdevGUIDs (PoolStats) error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().TrimPool("tank", nil, 0, false, POOL_TRIM_START); err == nil {
		t.Fatal("want guid-resolution error")
	}

	// Op ioctl error after a good stats lookup.
	statThenOp(t, func(int, *zfsCmd) error { return errInjected })
	if err := okHandle().TrimPool("tank", []string{"/tank/a.img"}, 100, true, POOL_TRIM_START); err == nil {
		t.Fatal("want trim ioctl error")
	}

	// Per-vdev errlist failure in outnvl.
	statThenOp(t, func(_ int, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{ZPOOL_TRIM_VDEVS: Nvlist{"111": int32(int32(unix.EINVAL))}})
		return nil
	})
	if err := okHandle().TrimPool("tank", []string{"/tank/a.img"}, 0, false, POOL_TRIM_START); err == nil {
		t.Fatal("want per-vdev trim error")
	}

	// Success (rate>0 + secure set), no outnvl.
	statThenOp(t, func(int, *zfsCmd) error { return nil })
	if err := okHandle().TrimPool("tank", []string{"/tank/a.img"}, 1<<20, true, POOL_TRIM_START); err != nil {
		t.Fatalf("TrimPool success: %v", err)
	}
}

func TestInitializePoolBranches(t *testing.T) {
	defer snapshotSeams()()

	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().InitializePool("tank", nil, POOL_INITIALIZE_START); err == nil {
		t.Fatal("want guid-resolution error")
	}

	statThenOp(t, func(int, *zfsCmd) error { return errInjected })
	if err := okHandle().InitializePool("tank", []string{"/tank/a.img"}, POOL_INITIALIZE_START); err == nil {
		t.Fatal("want initialize ioctl error")
	}

	statThenOp(t, func(_ int, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{ZPOOL_INITIALIZE_VDEVS: Nvlist{"111": int32(int32(unix.EBUSY))}})
		return nil
	})
	if err := okHandle().InitializePool("tank", []string{"/tank/a.img"}, POOL_INITIALIZE_START); err == nil {
		t.Fatal("want per-vdev initialize error")
	}

	statThenOp(t, func(int, *zfsCmd) error { return nil })
	if err := okHandle().InitializePool("tank", nil, POOL_INITIALIZE_CANCEL); err != nil {
		t.Fatalf("InitializePool success: %v", err)
	}
}

// ---- VdevAttach / VdevDetach / VdevSetState / VdevReopen ----

func TestVdevAttachBranches(t *testing.T) {
	defer snapshotSeams()()

	// PoolStats error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().VdevAttach("tank", "/tank/a.img", "/tank/c.img", false); err == nil {
		t.Fatal("want PoolStats error")
	}

	// Existing vdev not found in config.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, poolStatsConfig())
		return nil
	}
	if err := okHandle().VdevAttach("tank", "/tank/missing.img", "/tank/c.img", false); err == nil {
		t.Fatal("want existing-vdev-not-found error")
	}

	// nvlist build error: an empty new path makes Vdev.nvlist fail.
	if err := okHandle().VdevAttach("tank", "/tank/a.img", "", false); err == nil {
		t.Fatal("want new-vdev build error")
	}

	// setConf encode error via the encodeNative seam (after a good stats call).
	statThenOp(t, func(int, *zfsCmd) error { return nil })
	encodeNative = func(Nvlist) ([]byte, error) {
		// Let PoolStats decode (it uses DecodeNative); only the conf encode
		// inside VdevAttach uses encodeNative. Fail it.
		return nil, errInjected
	}
	if err := okHandle().VdevAttach("tank", "/tank/a.img", "/tank/c.img", false); err == nil {
		t.Fatal("want setConf encode error")
	}
	encodeNative = EncodeNative

	// Op ioctl error (replace=true sets zc_cookie=1; verify on success path).
	statThenOp(t, func(int, *zfsCmd) error { return errInjected })
	if err := okHandle().VdevAttach("tank", "/tank/a.img", "/dev/sdz", true); err == nil {
		t.Fatal("want attach ioctl error")
	}

	// Success: device path -> disk leaf, replace -> cookie=1, simple=0.
	statThenOp(t, func(_ int, cmd *zfsCmd) error {
		if cmd.getU64(offZcGuid) != 111 {
			t.Errorf("zc_guid = %d, want 111", cmd.getU64(offZcGuid))
		}
		if cmd.getU64(offZcCookie) != 1 {
			t.Errorf("replace cookie = %d, want 1", cmd.getU64(offZcCookie))
		}
		if cmd.buf[offZcSimple] != 0 {
			t.Errorf("zc_simple = %d, want 0", cmd.buf[offZcSimple])
		}
		return nil
	})
	if err := okHandle().VdevAttach("tank", "/tank/a.img", "/dev/sdz", true); err != nil {
		t.Fatalf("VdevAttach success: %v", err)
	}
}

func TestVdevDetachBranches(t *testing.T) {
	defer snapshotSeams()()

	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().VdevDetach("tank", "/tank/b.img"); err == nil {
		t.Fatal("want PoolStats error")
	}

	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, poolStatsConfig())
		return nil
	}
	if err := okHandle().VdevDetach("tank", "/tank/missing.img"); err == nil {
		t.Fatal("want vdev-not-found error")
	}

	statThenOp(t, func(int, *zfsCmd) error { return errInjected })
	if err := okHandle().VdevDetach("tank", "/tank/b.img"); err == nil {
		t.Fatal("want detach ioctl error")
	}

	statThenOp(t, func(_ int, cmd *zfsCmd) error {
		if cmd.getU64(offZcGuid) != 222 {
			t.Errorf("zc_guid = %d, want 222", cmd.getU64(offZcGuid))
		}
		return nil
	})
	if err := okHandle().VdevDetach("tank", "/tank/b.img"); err != nil {
		t.Fatalf("VdevDetach success: %v", err)
	}
}

func TestVdevSetStateBranches(t *testing.T) {
	defer snapshotSeams()()

	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().VdevSetState("tank", "/tank/b.img", VDEV_STATE_OFFLINE, 0); err == nil {
		t.Fatal("want PoolStats error")
	}

	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, poolStatsConfig())
		return nil
	}
	if _, err := okHandle().VdevSetState("tank", "/tank/missing.img", VDEV_STATE_OFFLINE, 0); err == nil {
		t.Fatal("want vdev-not-found error")
	}

	statThenOp(t, func(int, *zfsCmd) error { return errInjected })
	if _, err := okHandle().VdevSetState("tank", "/tank/b.img", VDEV_STATE_OFFLINE, 0); err == nil {
		t.Fatal("want set-state ioctl error")
	}

	// Success: kernel writes resulting state into zc_cookie.
	statThenOp(t, func(_ int, cmd *zfsCmd) error {
		if cmd.getU64(offZcGuid) != 222 || cmd.getU64(offZcObj) != 7 {
			t.Errorf("guid/flags = %d/%d, want 222/7", cmd.getU64(offZcGuid), cmd.getU64(offZcObj))
		}
		cmd.setU64(offZcCookie, VDEV_STATE_HEALTHY)
		return nil
	})
	got, err := okHandle().VdevSetState("tank", "/tank/b.img", VDEV_STATE_OFFLINE, 7)
	if err != nil || got != VDEV_STATE_HEALTHY {
		t.Fatalf("VdevSetState = %d, %v", got, err)
	}

	// Online/Offline convenience wrappers.
	statThenOp(t, func(_ int, cmd *zfsCmd) error { cmd.setU64(offZcCookie, VDEV_STATE_HEALTHY); return nil })
	if _, err := okHandle().VdevOnline("tank", "/tank/b.img"); err != nil {
		t.Fatalf("VdevOnline: %v", err)
	}
	statThenOp(t, func(_ int, cmd *zfsCmd) error { cmd.setU64(offZcCookie, VDEV_STATE_OFFLINE); return nil })
	if _, err := okHandle().VdevOffline("tank", "/tank/b.img"); err != nil {
		t.Fatalf("VdevOffline: %v", err)
	}
}

func TestVdevReopenBranches(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().VdevReopen("tank", false); err == nil {
		t.Fatal("want reopen ioctl error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().VdevReopen("tank", true); err != nil {
		t.Fatalf("VdevReopen: %v", err)
	}
}

// TestFirstVdevErr covers the helper's nil/missing/empty paths directly (the
// success paths above only hit the populated branch).
func TestFirstVdevErr(t *testing.T) {
	if err := firstVdevErr(nil, ZPOOL_TRIM_VDEVS); err != nil {
		t.Errorf("nil out: %v", err)
	}
	if err := firstVdevErr(Nvlist{}, ZPOOL_TRIM_VDEVS); err != nil {
		t.Errorf("missing key: %v", err)
	}
	if err := firstVdevErr(Nvlist{ZPOOL_TRIM_VDEVS: Nvlist{"1": int32(0)}}, ZPOOL_TRIM_VDEVS); err != nil {
		t.Errorf("zero errno: %v", err)
	}
}

// TestIsDevicePath covers both branches of the /dev/ prefix check.
func TestIsDevicePath(t *testing.T) {
	if !isDevicePath("/dev/sda") {
		t.Error("/dev/sda should be a device path")
	}
	if isDevicePath("/tank/a.img") || isDevicePath("/de") {
		t.Error("non-/dev paths should not be device paths")
	}
}

// TestScanStateStringDefault and TestScanFuncStringDefault cover the default
// arms of the stringers (the named arms are covered in scan_test.go).
func TestStringerDefaults(t *testing.T) {
	if got := ScanState(99).String(); got != "ScanState(99)" {
		t.Errorf("ScanState default = %q", got)
	}
	if got := ScanFunc(99).String(); got != "ScanFunc(99)" {
		t.Errorf("ScanFunc default = %q", got)
	}
	if got := ScanStateCanceled.String(); got != "canceled" {
		t.Errorf("canceled = %q", got)
	}
	if got := ScanStateNone.String(); got != "none" {
		t.Errorf("none = %q", got)
	}
}
