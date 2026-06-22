// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import (
	"reflect"
	"testing"
)

// TestScanEnumValues pins the scan/trim/initialize/vdev enum values against the
// 2.2.2 headers, since they are written directly into zc fields / innvls.
func TestScanEnumValues(t *testing.T) {
	for _, c := range []struct {
		name string
		got  int
		want int
	}{
		{"POOL_SCAN_NONE", POOL_SCAN_NONE, 0},
		{"POOL_SCAN_SCRUB", POOL_SCAN_SCRUB, 1},
		{"POOL_SCAN_RESILVER", POOL_SCAN_RESILVER, 2},
		{"POOL_SCRUB_NORMAL", POOL_SCRUB_NORMAL, 0},
		{"POOL_SCRUB_PAUSE", POOL_SCRUB_PAUSE, 1},
		{"DSS_NONE", DSS_NONE, 0},
		{"DSS_SCANNING", DSS_SCANNING, 1},
		{"DSS_FINISHED", DSS_FINISHED, 2},
		{"DSS_CANCELED", DSS_CANCELED, 3},
		{"POOL_INITIALIZE_START", POOL_INITIALIZE_START, 0},
		{"POOL_INITIALIZE_CANCEL", POOL_INITIALIZE_CANCEL, 1},
		{"POOL_INITIALIZE_SUSPEND", POOL_INITIALIZE_SUSPEND, 2},
		{"POOL_TRIM_START", POOL_TRIM_START, 0},
		{"POOL_TRIM_CANCEL", POOL_TRIM_CANCEL, 1},
		{"POOL_TRIM_SUSPEND", POOL_TRIM_SUSPEND, 2},
		{"VDEV_STATE_OFFLINE", VDEV_STATE_OFFLINE, 2},
		{"VDEV_STATE_FAULTED", VDEV_STATE_FAULTED, 5},
		{"VDEV_STATE_DEGRADED", VDEV_STATE_DEGRADED, 6},
		{"VDEV_STATE_HEALTHY", VDEV_STATE_HEALTHY, 7},
		{"VDEV_STATE_ONLINE", VDEV_STATE_ONLINE, 7},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

// TestScanStatusFromStats decodes a synthetic pool_scan_stat_t uint64 array
// (the wire form of "scan_stats") and checks the typed view.
func TestScanStatusFromStats(t *testing.T) {
	// Build an array long enough to include pss_issued (index 14).
	ps := make([]uint64, 16)
	ps[pssFunc] = POOL_SCAN_SCRUB
	ps[pssState] = DSS_SCANNING
	ps[pssStartTime] = 1_700_000_000
	ps[pssEndTime] = 0
	ps[pssToExamine] = 1000
	ps[pssExamined] = 250
	ps[pssProcessed] = 240
	ps[pssErrors] = 3
	ps[pssIssued] = 200

	st := scanStatusFromStats(ps)
	if st.Func != ScanScrub {
		t.Errorf("Func = %v, want scrub", st.Func)
	}
	if st.State != ScanStateScanning {
		t.Errorf("State = %v, want scanning", st.State)
	}
	if !st.Scanning() {
		t.Error("Scanning() = false, want true")
	}
	if st.ToExamine != 1000 || st.Examined != 250 || st.Issued != 200 || st.Errors != 3 {
		t.Errorf("byte counts wrong: %+v", st)
	}
	if got := st.Percent(); got != 25 {
		t.Errorf("Percent() = %v, want 25", got)
	}
}

// TestScanStatusEmpty verifies a never-scanned pool decodes to state none.
func TestScanStatusEmpty(t *testing.T) {
	if st := scanStatusFromStats(nil); st.State != ScanStateNone {
		t.Errorf("nil stats: state = %v, want none", st.State)
	}
	if st := scanStatusFromStats([]uint64{0, 0}); st.State != ScanStateNone {
		t.Errorf("short stats: state = %v, want none", st.State)
	}
	// Percent with nothing to examine is 100 (vacuously complete).
	if got := (ScanStatus{}).Percent(); got != 100 {
		t.Errorf("empty Percent() = %v, want 100", got)
	}
}

// TestScanStatusFromConfig threads scan_stats through a config nvlist shaped
// like the one PoolConfigs returns, round-tripped through the native codec.
func TestScanStatusFromConfig(t *testing.T) {
	ps := make([]uint64, 16)
	ps[pssFunc] = POOL_SCAN_RESILVER
	ps[pssState] = DSS_FINISHED
	ps[pssToExamine] = 4096
	ps[pssExamined] = 4096

	cfg := Nvlist{
		ZPOOL_CONFIG_POOL_NAME: "testpool",
		ZPOOL_CONFIG_VDEV_TREE: Nvlist{
			ZPOOL_CONFIG_TYPE:       VDEV_TYPE_ROOT,
			ZPOOL_CONFIG_SCAN_STATS: ps,
		},
	}
	// Round-trip to prove uint64-array survives native encode/decode inside a
	// nested nvlist.
	b, err := EncodeNative(cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := DecodeNative(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	st, ok := scanStatusFromConfig(out)
	if !ok {
		t.Fatal("scanStatusFromConfig: ok=false")
	}
	if st.Func != ScanResilver || st.State != ScanStateFinished {
		t.Errorf("got %+v", st)
	}
	if got := st.Percent(); got != 100 {
		t.Errorf("Percent() = %v, want 100", got)
	}
}

// TestScanStatusFromConfigMissing checks the no-scan_stats case is ok=false.
func TestScanStatusFromConfigMissing(t *testing.T) {
	cfg := Nvlist{ZPOOL_CONFIG_VDEV_TREE: Nvlist{ZPOOL_CONFIG_TYPE: VDEV_TYPE_ROOT}}
	if _, ok := scanStatusFromConfig(cfg); ok {
		t.Error("expected ok=false when scan_stats absent")
	}
	if _, ok := scanStatusFromConfig(Nvlist{}); ok {
		t.Error("expected ok=false when vdev_tree absent")
	}
}

// TestVdevGUIDByPath checks the path->guid walker against a mirror config
// nvlist of the shape PoolConfigs returns.
func TestVdevGUIDByPath(t *testing.T) {
	cfg := Nvlist{
		ZPOOL_CONFIG_VDEV_TREE: Nvlist{
			ZPOOL_CONFIG_TYPE: VDEV_TYPE_ROOT,
			ZPOOL_CONFIG_CHILDREN: []Nvlist{
				{
					ZPOOL_CONFIG_TYPE: VDEV_TYPE_MIRROR,
					ZPOOL_CONFIG_CHILDREN: []Nvlist{
						{ZPOOL_CONFIG_TYPE: VDEV_TYPE_FILE, ZPOOL_CONFIG_PATH: "/tmp/a.img", ZPOOL_CONFIG_GUID: uint64(111)},
						{ZPOOL_CONFIG_TYPE: VDEV_TYPE_FILE, ZPOOL_CONFIG_PATH: "/tmp/b.img", ZPOOL_CONFIG_GUID: uint64(222)},
					},
				},
			},
		},
	}
	g, err := vdevGUIDByPath(cfg, "/tmp/b.img")
	if err != nil {
		t.Fatalf("vdevGUIDByPath: %v", err)
	}
	if g != 222 {
		t.Errorf("guid = %d, want 222", g)
	}
	if _, err := vdevGUIDByPath(cfg, "/tmp/missing.img"); err == nil {
		t.Error("expected error for missing path")
	}
}

// TestCollectLeafGUIDs verifies the whole-pool leaf enumeration used when the
// trim/initialize vdev list is empty.
func TestCollectLeafGUIDs(t *testing.T) {
	tree := Nvlist{
		ZPOOL_CONFIG_TYPE: VDEV_TYPE_ROOT,
		ZPOOL_CONFIG_CHILDREN: []Nvlist{
			{ZPOOL_CONFIG_TYPE: VDEV_TYPE_FILE, ZPOOL_CONFIG_PATH: "/tmp/a.img", ZPOOL_CONFIG_GUID: uint64(111)},
			{
				ZPOOL_CONFIG_TYPE: VDEV_TYPE_MIRROR,
				ZPOOL_CONFIG_CHILDREN: []Nvlist{
					{ZPOOL_CONFIG_TYPE: VDEV_TYPE_FILE, ZPOOL_CONFIG_PATH: "/tmp/b.img", ZPOOL_CONFIG_GUID: uint64(222)},
					{ZPOOL_CONFIG_TYPE: VDEV_TYPE_FILE, ZPOOL_CONFIG_PATH: "/tmp/c.img", ZPOOL_CONFIG_GUID: uint64(333)},
				},
			},
		},
	}
	out := map[string]uint64{}
	collectLeafGUIDs(tree, out)
	want := map[string]uint64{"111": 111, "222": 222, "333": 333}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("leaf guids = %v, want %v", out, want)
	}
}

// TestScanStatusPercentClamp covers the Examined>ToExamine clamp branch (a
// transient the kernel can report while a scan revises its estimate).
func TestScanStatusPercentClamp(t *testing.T) {
	s := ScanStatus{ToExamine: 100, Examined: 250}
	if got := s.Percent(); got != 100 {
		t.Errorf("Percent() = %v, want clamped 100", got)
	}
}

// TestScanStatusFromStatsShort covers the truncated-array early return (an
// array present but shorter than pss_errors).
func TestScanStatusFromStatsShort(t *testing.T) {
	if st := scanStatusFromStats(make([]uint64, 4)); st.State != ScanStateNone {
		t.Errorf("short stats: state = %v, want none", st.State)
	}
}

// TestScanStatusFromStatsMidLength uses an array long enough to pass the
// pss_errors guard but shorter than pss_issued, exercising the get() helper's
// out-of-range (return 0) arm for the trailing Issued field.
func TestScanStatusFromStatsMidLength(t *testing.T) {
	ps := make([]uint64, pssErrors+1) // index 0..pssErrors present, pssIssued absent
	ps[pssState] = DSS_FINISHED
	st := scanStatusFromStats(ps)
	if st.State != ScanStateFinished {
		t.Errorf("state = %v, want finished", st.State)
	}
	if st.Issued != 0 {
		t.Errorf("Issued = %d, want 0 (field absent)", st.Issued)
	}
}

// TestVdevGUIDByPathNoTree covers the missing-vdev_tree error branch.
func TestVdevGUIDByPathNoTree(t *testing.T) {
	if _, err := vdevGUIDByPath(Nvlist{}, "/x"); err == nil {
		t.Error("expected error when vdev_tree absent")
	}
}

// TestCollectLeafGUIDsNil covers the nil-tree guard.
func TestCollectLeafGUIDsNil(t *testing.T) {
	out := map[string]uint64{}
	collectLeafGUIDs(nil, out)
	if len(out) != 0 {
		t.Errorf("nil tree produced %v", out)
	}
}

// TestStringers exercises the String() methods for coverage / stability.
func TestStringers(t *testing.T) {
	if ScanScrub.String() != "scrub" || ScanResilver.String() != "resilver" || ScanNone.String() != "none" {
		t.Error("ScanFunc.String mismatch")
	}
	if ScanStateScanning.String() != "scanning" || ScanStateFinished.String() != "finished" {
		t.Error("ScanState.String mismatch")
	}
}
