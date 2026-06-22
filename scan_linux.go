// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"fmt"
	"runtime"
	"strconv"

	"golang.org/x/sys/unix"
)

// setU8 writes a single byte at the given offset within the zfs_cmd_t buffer.
// Used for the uint8 zc_simple field.
func (c *zfsCmd) setU8(off int, v uint8) { c.buf[off] = v }

// ScanPool drives ZFS_IOC_POOL_SCAN, the scrub/resilver entry point. fn selects
// the scan (scrub, resilver, or none-to-cancel) and is written into zc_cookie;
// cmd controls pause/resume and is written into zc_flags. This mirrors the
// legacy zc-based path the zpool CLI falls back to (libzfs zpool_scan), and the
// kernel's zfs_ioc_pool_scan:
//
//	zc_flags == POOL_SCRUB_PAUSE     -> pause the active scrub
//	zc_cookie == POOL_SCAN_NONE      -> cancel any in-progress scan
//	otherwise                        -> start (or resume) scan zc_cookie
//
// Resuming a paused scrub (fn=ScanScrub, cmd=ScanNormal) returns ECANCELED from
// the kernel, which the CLI treats as success; we do the same.
func (h *Handle) ScanPool(pool string, fn ScanFunc, cmd ScanCmd) error {
	c := &zfsCmd{}
	if err := c.setName(pool); err != nil {
		return err
	}
	c.setU64(offZcCookie, uint64(fn))
	c.setU64(offZcFlags, uint64(cmd))
	err := h.ioctl(ZFS_IOC_POOL_SCAN, c)
	if err != nil {
		// Resuming a paused scrub reports ECANCELED; pausing when none is
		// running reports ENOENT. Both are benign no-ops, matching libzfs.
		if err == unix.ECANCELED && fn != ScanNone && cmd == ScanNormal {
			return nil
		}
		if err == unix.ENOENT && fn != ScanNone && cmd == ScanPause {
			return nil
		}
		return fmt.Errorf("ZFS_IOC_POOL_SCAN %q (func=%s cmd=%d): %w", pool, fn, cmd, err)
	}
	return nil
}

// ScrubStart begins (or resumes) a scrub of pool.
func (h *Handle) ScrubStart(pool string) error {
	return h.ScanPool(pool, ScanScrub, ScanNormal)
}

// ScrubStop cancels any in-progress scrub/resilver of pool.
func (h *Handle) ScrubStop(pool string) error {
	return h.ScanPool(pool, ScanNone, ScanNormal)
}

// ScrubPause pauses an in-progress scrub of pool.
func (h *Handle) ScrubPause(pool string) error {
	return h.ScanPool(pool, ScanScrub, ScanPause)
}

// ResilverStart starts a resilver of pool.
func (h *Handle) ResilverStart(pool string) error {
	return h.ScanPool(pool, ScanResilver, ScanNormal)
}

// PoolStats issues ZFS_IOC_POOL_STATS for pool and returns the kernel's freshly
// generated config nvlist. Unlike the cached config from ZFS_IOC_POOL_CONFIGS,
// this config is produced by spa_get_stats -> spa_config_generate over the live
// root vdev, so it carries the dynamic "scan_stats" and per-vdev "vdev_stats"
// uint64 arrays the zpool CLI uses for `zpool status`. zc_name carries the pool;
// the config comes back in zc_nvlist_dst.
func (h *Handle) PoolStats(pool string) (Nvlist, error) {
	nv, err := h.callWithDst(ZFS_IOC_POOL_STATS, func(c *zfsCmd) error {
		return c.setName(pool)
	}, 256*1024)
	if err != nil {
		return nil, fmt.Errorf("ZFS_IOC_POOL_STATS %q: %w", pool, err)
	}
	return nv, nil
}

// ScanStatus reads the current scan (scrub/resilver) status of pool by fetching
// its live config via ZFS_IOC_POOL_STATS and decoding the vdev-tree-root
// "scan_stats" uint64 array into a typed ScanStatus. (The cached
// ZFS_IOC_POOL_CONFIGS config does NOT carry scan_stats — only the
// freshly-generated POOL_STATS config does, which is why `zpool status` uses
// it.) When the pool has never been scanned, a zero-value (state none) status
// is returned.
func (h *Handle) ScanStatus(pool string) (ScanStatus, error) {
	cfg, err := h.PoolStats(pool)
	if err != nil {
		return ScanStatus{}, fmt.Errorf("ScanStatus %q: %w", pool, err)
	}
	st, _ := scanStatusFromConfig(cfg)
	return st, nil
}

// vdevGUIDs resolves a list of vdev specifiers (each either an absolute device
// path present in the pool config, or a decimal guid string) into guids,
// reading the pool config once. An empty list selects every leaf vdev (the
// whole pool), matching `zpool trim <pool>` / `zpool initialize <pool>`.
func (h *Handle) vdevGUIDs(pool string, vdevs []string) (map[string]uint64, error) {
	cfg, err := h.PoolStats(pool)
	if err != nil {
		return nil, err
	}
	out := make(map[string]uint64)
	if len(vdevs) == 0 {
		tree, _ := cfg[ZPOOL_CONFIG_VDEV_TREE].(Nvlist)
		collectLeafGUIDs(tree, out)
		if len(out) == 0 {
			return nil, fmt.Errorf("pool %q: no leaf vdevs found", pool)
		}
		return out, nil
	}
	for _, v := range vdevs {
		if g, found := findVdevGUID(cfg[ZPOOL_CONFIG_VDEV_TREE].(Nvlist), v); found {
			out[strconv.FormatUint(g, 10)] = g
			continue
		}
		// Allow a bare guid string.
		if g, perr := strconv.ParseUint(v, 10, 64); perr == nil {
			out[v] = g
			continue
		}
		return nil, fmt.Errorf("vdev %q not found in pool %q", v, pool)
	}
	return out, nil
}

// TrimPool drives ZFS_IOC_POOL_TRIM (lzc_trim). It issues a manual TRIM/UNMAP
// against the named vdevs (or every vdev when vdevs is empty). rate caps the
// bytes/sec (0 = maximum); secure requests a secure TRIM where the device
// supports it. cmd is one of POOL_TRIM_START / _CANCEL / _SUSPEND.
//
// The innvl mirrors the kernel's zfs_ioc_pool_trim:
//
//	{
//	  "trim_command": <uint64 cmd>,
//	  "trim_vdevs":   { "<guid>": <uint64 guid>, ... },
//	  "trim_rate":    <uint64>,   // optional
//	  "trim_secure":  <bool>,     // optional
//	}
//
// Per-vdev failures come back in outnvl["trim_vdevs"] keyed by guid.
func (h *Handle) TrimPool(pool string, vdevs []string, rate uint64, secure bool, cmd uint64) error {
	guids, err := h.vdevGUIDs(pool, vdevs)
	if err != nil {
		return fmt.Errorf("TrimPool %q: %w", pool, err)
	}
	gnv := make(Nvlist, len(guids))
	for k, g := range guids {
		gnv[k] = g
	}
	innvl := Nvlist{
		ZPOOL_TRIM_COMMAND: cmd,
		ZPOOL_TRIM_VDEVS:   gnv,
	}
	if rate > 0 {
		innvl[ZPOOL_TRIM_RATE] = rate
	}
	if secure {
		innvl[ZPOOL_TRIM_SECURE] = true
	}
	out, err := h.callNewName(ZFS_IOC_POOL_TRIM, pool, innvl)
	if err != nil {
		return fmt.Errorf("ZFS_IOC_POOL_TRIM %q: %w", pool, err)
	}
	if e := firstVdevErr(out, ZPOOL_TRIM_VDEVS); e != nil {
		return fmt.Errorf("ZFS_IOC_POOL_TRIM %q: %w", pool, e)
	}
	return nil
}

// InitializePool drives ZFS_IOC_POOL_INITIALIZE (lzc_initialize). It writes a
// known pattern to all unallocated space of the named vdevs (or every vdev when
// vdevs is empty). cmd is POOL_INITIALIZE_START / _CANCEL / _SUSPEND.
//
// The innvl mirrors zfs_ioc_pool_initialize:
//
//	{
//	  "initialize_command": <uint64 cmd>,
//	  "initialize_vdevs":   { "<guid>": <uint64 guid>, ... },
//	}
//
// Per-vdev failures come back in outnvl["initialize_vdevs"] keyed by guid.
func (h *Handle) InitializePool(pool string, vdevs []string, cmd uint64) error {
	guids, err := h.vdevGUIDs(pool, vdevs)
	if err != nil {
		return fmt.Errorf("InitializePool %q: %w", pool, err)
	}
	gnv := make(Nvlist, len(guids))
	for k, g := range guids {
		gnv[k] = g
	}
	innvl := Nvlist{
		ZPOOL_INITIALIZE_COMMAND: cmd,
		ZPOOL_INITIALIZE_VDEVS:   gnv,
	}
	out, err := h.callNewName(ZFS_IOC_POOL_INITIALIZE, pool, innvl)
	if err != nil {
		return fmt.Errorf("ZFS_IOC_POOL_INITIALIZE %q: %w", pool, err)
	}
	if e := firstVdevErr(out, ZPOOL_INITIALIZE_VDEVS); e != nil {
		return fmt.Errorf("ZFS_IOC_POOL_INITIALIZE %q: %w", pool, e)
	}
	return nil
}

// VdevAttach attaches newVdev (an absolute device/file path) as a mirror of the
// already-present existingVdev in pool, via ZFS_IOC_VDEV_ATTACH. When replace
// is true the new device replaces the existing one (the `zpool replace` path):
// the kernel resilvers newVdev from its peers and detaches existingVdev when
// done. existingVdev is resolved to its guid (zc_guid) from the pool config;
// the new vdev's config goes in zc_nvlist_conf; replace maps to zc_cookie. The
// rebuild (sequential-resilver) flag zc_simple is left 0 (normal resilver).
func (h *Handle) VdevAttach(pool, existingVdev, newVdev string, replace bool) error {
	cfg, err := h.PoolStats(pool)
	if err != nil {
		return fmt.Errorf("VdevAttach %q: %w", pool, err)
	}
	guid, err := vdevGUIDByPath(cfg, existingVdev)
	if err != nil {
		return fmt.Errorf("VdevAttach %q: existing vdev: %w", pool, err)
	}

	// Build the new vdev's config as a single-leaf vdev tree. spa_vdev_attach
	// expects a root vdev containing exactly one child (the new device).
	leafType := VDEV_TYPE_FILE
	if isDevicePath(newVdev) {
		leafType = VDEV_TYPE_DISK
	}
	root := Vdev{Type: VDEV_TYPE_ROOT, Children: []Vdev{{Type: leafType, Path: newVdev}}}
	tree, err := root.nvlist()
	if err != nil {
		return fmt.Errorf("VdevAttach %q: build new vdev: %w", pool, err)
	}

	c := &zfsCmd{}
	// pool's length was already validated by the preceding PoolStats call
	// (which set the same name), so this setName cannot fail.
	_ = c.setName(pool)
	c.setU64(offZcGuid, guid)
	if replace {
		c.setU64(offZcCookie, 1)
	}
	c.setU8(offZcSimple, 0) // normal healing resilver, not sequential rebuild
	kaConf, err := c.setConf(tree)
	if err != nil {
		return err
	}
	err = h.ioctl(ZFS_IOC_VDEV_ATTACH, c)
	runtime.KeepAlive(kaConf)
	if err != nil {
		return fmt.Errorf("ZFS_IOC_VDEV_ATTACH %q (%s -> %s, replace=%v): %w",
			pool, existingVdev, newVdev, replace, err)
	}
	return nil
}

// VdevDetach detaches vdev (a device/file path) from a mirror in pool via
// ZFS_IOC_VDEV_DETACH, returning the pool to fewer replicas. vdev is resolved
// to its guid (zc_guid) from the pool config. The last remaining device of a
// non-redundant top-level vdev cannot be detached (the kernel returns EINVAL).
func (h *Handle) VdevDetach(pool, vdev string) error {
	cfg, err := h.PoolStats(pool)
	if err != nil {
		return fmt.Errorf("VdevDetach %q: %w", pool, err)
	}
	guid, err := vdevGUIDByPath(cfg, vdev)
	if err != nil {
		return fmt.Errorf("VdevDetach %q: %w", pool, err)
	}
	c := &zfsCmd{}
	// Name already validated by the preceding PoolStats call.
	_ = c.setName(pool)
	c.setU64(offZcGuid, guid)
	if err := h.ioctl(ZFS_IOC_VDEV_DETACH, c); err != nil {
		return fmt.Errorf("ZFS_IOC_VDEV_DETACH %q (%s): %w", pool, vdev, err)
	}
	return nil
}

// VdevSetState changes the state of vdev in pool via ZFS_IOC_VDEV_SET_STATE.
// newState is a vdev_state_t request value: VDEV_STATE_HEALTHY (online),
// VDEV_STATE_OFFLINE, VDEV_STATE_FAULTED, VDEV_STATE_DEGRADED, or
// VDEV_STATE_REMOVED. flags carries the ZFS_ONLINE_* / ZFS_OFFLINE_* modifier
// bits in zc_obj (0 for the common case). vdev is resolved to its guid
// (zc_guid). The requested state is written to zc_cookie; the kernel returns
// the resulting state in zc_cookie, which is returned here.
func (h *Handle) VdevSetState(pool, vdev string, newState uint64, flags uint64) (uint64, error) {
	cfg, err := h.PoolStats(pool)
	if err != nil {
		return 0, fmt.Errorf("VdevSetState %q: %w", pool, err)
	}
	guid, err := vdevGUIDByPath(cfg, vdev)
	if err != nil {
		return 0, fmt.Errorf("VdevSetState %q: %w", pool, err)
	}
	c := &zfsCmd{}
	// Name already validated by the preceding PoolStats call.
	_ = c.setName(pool)
	c.setU64(offZcGuid, guid)
	c.setU64(offZcCookie, newState)
	c.setU64(offZcObj, flags)
	if err := h.ioctl(ZFS_IOC_VDEV_SET_STATE, c); err != nil {
		return 0, fmt.Errorf("ZFS_IOC_VDEV_SET_STATE %q (%s, state=%d): %w", pool, vdev, newState, err)
	}
	return c.getU64(offZcCookie), nil
}

// VdevOnline brings vdev online in pool (VDEV_STATE_HEALTHY request) and
// returns the resulting vdev state.
func (h *Handle) VdevOnline(pool, vdev string) (uint64, error) {
	return h.VdevSetState(pool, vdev, VDEV_STATE_ONLINE, 0)
}

// VdevOffline takes vdev offline in pool (VDEV_STATE_OFFLINE request) and
// returns the resulting vdev state.
func (h *Handle) VdevOffline(pool, vdev string) (uint64, error) {
	return h.VdevSetState(pool, vdev, VDEV_STATE_OFFLINE, 0)
}

// VdevReopen drives ZFS_IOC_POOL_REOPEN (lzc_reopen), reopening every vdev in
// the pool to pick up changes (e.g. expanded backing devices). When
// scrubRestart is false, an in-progress scrub is not restarted as a side effect
// of the reopen. The innvl is { "scrub_restart": <bool> }.
func (h *Handle) VdevReopen(pool string, scrubRestart bool) error {
	innvl := Nvlist{"scrub_restart": scrubRestart}
	if _, err := h.callNewName(ZFS_IOC_POOL_REOPEN, pool, innvl); err != nil {
		return fmt.Errorf("ZFS_IOC_POOL_REOPEN %q: %w", pool, err)
	}
	return nil
}

// firstVdevErr inspects an outnvl from the trim/initialize ioctls for a nested
// per-vdev error list under key, returning the first non-zero errno as an error.
func firstVdevErr(out Nvlist, key string) error {
	if out == nil {
		return nil
	}
	errs, ok := out[key].(Nvlist)
	if !ok {
		return nil
	}
	return firstErrlistErr(errs)
}

// isDevicePath reports whether p looks like a block device (under /dev). File
// vdevs are anything else (the loopback-backed test pools use file paths).
func isDevicePath(p string) bool {
	const dev = "/dev/"
	return len(p) >= len(dev) && p[:len(dev)] == dev
}
