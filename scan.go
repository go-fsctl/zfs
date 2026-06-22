// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import (
	"fmt"
	"strconv"
)

// ScanFunc selects which scan ZFS_IOC_POOL_SCAN starts (or cancels). It maps
// directly onto pool_scan_func_t and is written into zc_cookie.
type ScanFunc uint64

const (
	// ScanNone cancels any in-progress scan (POOL_SCAN_NONE).
	ScanNone ScanFunc = POOL_SCAN_NONE
	// ScanScrub starts (or resumes) a scrub (POOL_SCAN_SCRUB).
	ScanScrub ScanFunc = POOL_SCAN_SCRUB
	// ScanResilver starts a resilver (POOL_SCAN_RESILVER).
	ScanResilver ScanFunc = POOL_SCAN_RESILVER
)

// ScanCmd controls pause/resume of a scrub. It maps onto pool_scrub_cmd_t and
// is written into zc_flags.
type ScanCmd uint64

const (
	// ScanNormal begins or resumes a scan (POOL_SCRUB_NORMAL).
	ScanNormal ScanCmd = POOL_SCRUB_NORMAL
	// ScanPause pauses an active scrub (POOL_SCRUB_PAUSE).
	ScanPause ScanCmd = POOL_SCRUB_PAUSE
)

// ScanState is the dsl_scan_state_t reported in a pool's scan stats.
type ScanState uint64

const (
	ScanStateNone     ScanState = DSS_NONE
	ScanStateScanning ScanState = DSS_SCANNING
	ScanStateFinished ScanState = DSS_FINISHED
	ScanStateCanceled ScanState = DSS_CANCELED
)

// String renders the dsl_scan_state_t the way `zpool status` discusses it.
func (s ScanState) String() string {
	switch s {
	case ScanStateNone:
		return "none"
	case ScanStateScanning:
		return "scanning"
	case ScanStateFinished:
		return "finished"
	case ScanStateCanceled:
		return "canceled"
	default:
		return fmt.Sprintf("ScanState(%d)", uint64(s))
	}
}

// String renders the pool_scan_func_t.
func (f ScanFunc) String() string {
	switch f {
	case ScanNone:
		return "none"
	case ScanScrub:
		return "scrub"
	case ScanResilver:
		return "resilver"
	default:
		return fmt.Sprintf("ScanFunc(%d)", uint64(f))
	}
}

// ScanStatus is a typed view of the pool_scan_stat_t the kernel reports under
// the vdev-tree-root "scan_stats" key (a uint64 array). It is decoded from the
// config returned by PoolConfigs / ZFS_IOC_POOL_STATS — see ScanStatus on the
// Handle. Byte counts are exact; Percent is a convenience derived from them.
type ScanStatus struct {
	Func      ScanFunc  // scrub / resilver / none
	State     ScanState // none / scanning / finished / canceled
	StartTime uint64    // pss_start_time (unix seconds)
	EndTime   uint64    // pss_end_time (unix seconds; 0 while running)
	ToExamine uint64    // pss_to_examine: total bytes to scan
	Examined  uint64    // pss_examined: bytes located by the scanner
	Issued    uint64    // pss_issued: bytes actually checked
	Processed uint64    // pss_processed
	Errors    uint64    // pss_errors
}

// Percent is the fraction of the scan completed (0..100), computed from
// Examined/ToExamine. Returns 100 when there is nothing to examine.
func (s ScanStatus) Percent() float64 {
	if s.ToExamine == 0 {
		return 100
	}
	p := float64(s.Examined) / float64(s.ToExamine) * 100
	if p > 100 {
		return 100
	}
	return p
}

// Scanning reports whether a scan is currently in progress.
func (s ScanStatus) Scanning() bool { return s.State == ScanStateScanning }

// Field indices into the pool_scan_stat_t uint64 array (the order of the
// struct members in sys/fs/zfs.h). Only the leading "stored on disk" plus a
// couple of derived fields are named; later error-scrub fields are ignored.
const (
	pssFunc      = 0
	pssState     = 1
	pssStartTime = 2
	pssEndTime   = 3
	pssToExamine = 4
	pssExamined  = 5
	pssSkipped   = 6
	pssProcessed = 7
	pssErrors    = 8
	pssPassExam  = 9
	pssPassStart = 10
	// ... (pass/error-scrub fields follow; not surfaced)
	pssIssued = 14 // pss_issued
)

// scanStatusFromStats decodes a pool_scan_stat_t uint64 array into ScanStatus.
// A nil/short array (no scan ever run) yields a zero-value (state none) status.
func scanStatusFromStats(ps []uint64) ScanStatus {
	var s ScanStatus
	get := func(i int) uint64 {
		if i < len(ps) {
			return ps[i]
		}
		return 0
	}
	if len(ps) <= pssErrors {
		// No (or truncated) stats: treat as "no scan".
		return s
	}
	s.Func = ScanFunc(get(pssFunc))
	s.State = ScanState(get(pssState))
	s.StartTime = get(pssStartTime)
	s.EndTime = get(pssEndTime)
	s.ToExamine = get(pssToExamine)
	s.Examined = get(pssExamined)
	s.Processed = get(pssProcessed)
	s.Errors = get(pssErrors)
	s.Issued = get(pssIssued)
	return s
}

// scanStatusFromConfig extracts the ScanStatus from a pool *config* nvlist (as
// returned by PoolConfigs). It reads cfg["vdev_tree"]["scan_stats"]. When the
// pool has never been scanned the scan_stats key may be absent; that yields a
// zero-value ScanStatus (state none) and ok=false.
func scanStatusFromConfig(cfg Nvlist) (ScanStatus, bool) {
	tree, ok := cfg[ZPOOL_CONFIG_VDEV_TREE].(Nvlist)
	if !ok {
		return ScanStatus{}, false
	}
	ps, ok := tree[ZPOOL_CONFIG_SCAN_STATS].([]uint64)
	if !ok {
		return ScanStatus{}, false
	}
	return scanStatusFromStats(ps), true
}

// vdevGUIDByPath walks a pool config's vdev tree looking for a leaf whose
// "path" matches the given device path, and returns its "guid". The vdev
// ioctls (attach/detach/set_state) identify the target by guid, not path, so
// callers that know a device path resolve it here first (mirroring the
// zpool_find_vdev path the CLI performs in userland). It searches the
// vdev_tree under cfg and recurses through children.
func vdevGUIDByPath(cfg Nvlist, path string) (uint64, error) {
	tree, ok := cfg[ZPOOL_CONFIG_VDEV_TREE].(Nvlist)
	if !ok {
		return 0, fmt.Errorf("config has no %q", ZPOOL_CONFIG_VDEV_TREE)
	}
	if g, found := findVdevGUID(tree, path); found {
		return g, nil
	}
	return 0, fmt.Errorf("vdev with path %q not found in pool config", path)
}

// collectLeafGUIDs gathers the guids of every leaf (file/disk) vdev under tree,
// keyed by the decimal guid string (the key form the trim/initialize innvls
// use). Used when an empty vdev list selects the whole pool.
func collectLeafGUIDs(tree Nvlist, out map[string]uint64) {
	if tree == nil {
		return
	}
	kids, ok := tree[ZPOOL_CONFIG_CHILDREN].([]Nvlist)
	if !ok {
		// Leaf: record its guid if it has a path (skip the root sentinel).
		if _, hasPath := tree[ZPOOL_CONFIG_PATH].(string); hasPath {
			if g, ok := tree[ZPOOL_CONFIG_GUID].(uint64); ok {
				out[strconv.FormatUint(g, 10)] = g
			}
		}
		return
	}
	for _, c := range kids {
		collectLeafGUIDs(c, out)
	}
}

// findVdevGUID recurses a vdev nvlist subtree for a leaf with the given path.
func findVdevGUID(vd Nvlist, path string) (uint64, bool) {
	if p, ok := vd[ZPOOL_CONFIG_PATH].(string); ok && p == path {
		if g, ok := vd[ZPOOL_CONFIG_GUID].(uint64); ok {
			return g, true
		}
	}
	kids, ok := vd[ZPOOL_CONFIG_CHILDREN].([]Nvlist)
	if !ok {
		return 0, false
	}
	for _, c := range kids {
		if g, found := findVdevGUID(c, path); found {
			return g, true
		}
	}
	return 0, false
}
