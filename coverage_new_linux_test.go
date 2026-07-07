// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// This file drives every branch of channel_linux.go, space_linux.go and
// diff_linux.go through the ioctlFn / osPipe seams, fault-injecting both the
// success and each error path WITHOUT a real /dev/zfs or root — the same
// approach as coverage_linux_test.go. The root-only integration tests exercise
// the genuine kernel paths and skip when /dev/zfs is absent.

// ---- channel_linux: ChannelProgram / ListSnapshotsZCP ----

func TestChannelProgram(t *testing.T) {
	defer snapshotSeams()()

	// empty script.
	if _, err := okHandle().ChannelProgram("tank", "", ChannelProgramOptions{}); err == nil {
		t.Fatal("want empty-script error")
	}

	// ioctl runtime error with an "error" entry in outnvl -> wrapped with detail.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{ZCP_RET_ERROR: "boom"})
		return unix.ECHRNG
	}
	if _, err := okHandle().ChannelProgram("tank", "error('boom')", ChannelProgramOptions{}); err == nil {
		t.Fatal("want runtime error with detail")
	}

	// ioctl error with NO outnvl filled -> plain wrapped error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().ChannelProgram("tank", "return 1", ChannelProgramOptions{
		InstrLimit: 5, MemLimit: 7, Sync: true, Args: Nvlist{"x": uint64(1)},
	}); err == nil {
		t.Fatal("want plain ioctl error")
	}

	// success with a "return" nvlist.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{ZCP_RET_RETURN: Nvlist{"42": uint64(42)}})
		return nil
	}
	out, err := okHandle().ChannelProgram("tank", "return 42", ChannelProgramOptions{})
	if err != nil {
		t.Fatalf("ChannelProgram: %v", err)
	}
	if out["42"] != uint64(42) {
		t.Errorf("return = %v", out)
	}

	// success with NO "return" (or a non-nvlist return) -> empty result.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{ZCP_RET_RETURN: uint64(7)})
		return nil
	}
	out, err = okHandle().ChannelProgram("tank", "return 7", ChannelProgramOptions{})
	if err != nil || len(out) != 0 {
		t.Fatalf("non-nvlist return: out=%v err=%v", out, err)
	}
}

func TestListSnapshotsZCP(t *testing.T) {
	defer snapshotSeams()()

	// ioctl error path.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().ListSnapshotsZCP("tank/ds"); err == nil {
		t.Fatal("want error")
	}

	// success: zcp returns an array-style table { "1": name1, "2": name2 }.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{ZCP_RET_RETURN: Nvlist{
			"1": "tank/ds@a",
			"2": "tank/ds@b",
		}})
		return nil
	}
	snaps, err := okHandle().ListSnapshotsZCP("tank/ds")
	if err != nil {
		t.Fatalf("ListSnapshotsZCP: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("got %d snapshots, want 2: %v", len(snaps), snaps)
	}
}

// ---- space_linux: UserSpace / UserSpaceByID / SetUserQuota ----

// putUseracct packs n zfs_useracct_t entries into the captured dst buffer and
// reports the byte count via zc_nvlist_dst_size, emulating USERSPACE_MANY.
func putUseracct(t *testing.T, cmd *zfsCmd, entries []SpaceEntry) {
	t.Helper()
	if lastDst == nil {
		t.Fatal("putUseracct: no dst buffer captured")
	}
	for i := range lastDst {
		lastDst[i] = 0
	}
	for i, e := range entries {
		off := i * sizeofUseracct
		copy(lastDst[off+offZuDomain:], e.Domain)
		hostBO.PutUint32(lastDst[off+offZuRid:off+offZuRid+4], e.RID)
		hostBO.PutUint64(lastDst[off+offZuSpace:off+offZuSpace+8], e.Value)
	}
	cmd.setU64(offZcNvlistDstSize, uint64(len(entries)*sizeofUseracct))
}

func TestUserSpace(t *testing.T) {
	defer snapshotSeams()()

	// setName error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if _, err := okHandle().UserSpace(string(make([]byte, maxPathLen)), UserUsed); err == nil {
		t.Fatal("want setName error")
	}

	// ioctl error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().UserSpace("tank/ds", UserUsed); err == nil {
		t.Fatal("want ioctl error")
	}

	// success: first call returns two entries, second returns none (loop ends).
	calls := 0
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		calls++
		if calls == 1 {
			putUseracct(t, cmd, []SpaceEntry{
				{Domain: "", RID: 1000, Value: 4096},
				{Domain: "", RID: 0, Value: 8192},
			})
			cmd.setU64(offZcCookie, 99) // advance cursor
			return nil
		}
		putUseracct(t, cmd, nil) // zero entries -> terminate
		return nil
	}
	entries, err := okHandle().UserSpace("tank/ds", UserUsed)
	if err != nil {
		t.Fatalf("UserSpace: %v", err)
	}
	if len(entries) != 2 || entries[0].RID != 1000 || entries[0].Value != 4096 {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestUserSpaceByID(t *testing.T) {
	defer snapshotSeams()()

	// error path propagates.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, _, err := okHandle().UserSpaceByID("tank/ds", UserUsed, 1000); err == nil {
		t.Fatal("want error")
	}

	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		if cmd.getU64(offZcCookie) == 0 {
			putUseracct(t, cmd, []SpaceEntry{{RID: 1000, Value: 4096}})
			cmd.setU64(offZcCookie, 1)
			return nil
		}
		putUseracct(t, cmd, nil)
		return nil
	}
	// found.
	v, ok, err := okHandle().UserSpaceByID("tank/ds", UserUsed, 1000)
	if err != nil || !ok || v != 4096 {
		t.Fatalf("byID found: v=%d ok=%v err=%v", v, ok, err)
	}
	// not found.
	_, ok, err = okHandle().UserSpaceByID("tank/ds", UserUsed, 4242)
	if err != nil || ok {
		t.Fatalf("byID missing: ok=%v err=%v", ok, err)
	}
}

func TestSetUserQuota(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }

	// non-settable prop rejected.
	if err := okHandle().SetUserQuota("tank/ds", UserUsed, "1000", 10); err == nil {
		t.Fatal("want non-settable-prop error")
	}
	// empty who rejected.
	if err := okHandle().SetUserQuota("tank/ds", UserQuota, "", 10); err == nil {
		t.Fatal("want empty-who error")
	}
	// success.
	if err := okHandle().SetUserQuota("tank/ds", UserQuota, "1000", 1<<20); err != nil {
		t.Fatalf("SetUserQuota: %v", err)
	}
	// SetProp ioctl error surfaces.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().SetUserQuota("tank/ds", GroupQuota, "0", 0); err == nil {
		t.Fatal("want SetProp error")
	}
}

// ---- diff_linux: Diff / readDiffRanges / classify* / objToStats / nextObj ----

// writeDiffRecords writes the given ranges to the pipe write fd carried in the
// command's zc_cookie, emulating the kernel's ZFS_IOC_DIFF record stream.
func writeDiffRecords(cmd *zfsCmd, ranges []diffRange) {
	fd := int(cmd.getU64(offZcCookie))
	buf := make([]byte, len(ranges)*sizeofDiffRecord)
	for i, r := range ranges {
		off := i * sizeofDiffRecord
		hostBO.PutUint64(buf[off+offDdrType:], r.typ)
		hostBO.PutUint64(buf[off+offDdrFirst:], r.first)
		hostBO.PutUint64(buf[off+offDdrLast:], r.last)
	}
	_, _ = unix.Write(fd, buf)
}

// putStat writes a zfs_stat_t + path into the command, emulating OBJ_TO_STATS.
func putStat(cmd *zfsCmd, gen, mode, links, ctime0 uint64, path string) {
	cmd.setU64(offZsGen, gen)
	cmd.setU64(offZsMode, mode)
	cmd.setU64(offZsLinks, links)
	cmd.setU64(offZsCtime0, ctime0)
	cmd.setU64(offZsCtime1, 0)
	_ = cmd.setValue(path)
}

func TestDiffValidation(t *testing.T) {
	defer snapshotSeams()()
	// from is not a snapshot.
	if _, err := okHandle().Diff("tank/ds", "tank/ds@s2"); err == nil {
		t.Fatal("want from-not-snapshot error")
	}
}

func TestDiffPipeError(t *testing.T) {
	defer snapshotSeams()()
	osPipe = func() (*os.File, *os.File, error) { return nil, nil, errInjected }
	if _, err := okHandle().Diff("tank/ds@s1", "tank/ds@s2"); err == nil {
		t.Fatal("want pipe error")
	}
}

func TestDiffSetNameValueErrors(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }

	// setName(toSnapOrFs) too long.
	long := string(make([]byte, maxPathLen))
	if _, err := okHandle().Diff("tank/ds@s1", long); err == nil {
		t.Fatal("want setName error")
	}
	// setValue(fromSnap) too long: fromSnap must contain '@' and exceed
	// MAXPATHLEN*2. Build a long valid-ish snapshot name.
	longFrom := "tank/ds@" + string(make([]byte, maxPathLen*2))
	if _, err := okHandle().Diff(longFrom, "tank/ds@s2"); err == nil {
		t.Fatal("want setValue error")
	}
}

func TestDiffIoctlError(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(_ *Handle, req uintptr, cmd *zfsCmd) error {
		return errInjected
	}
	if _, err := okHandle().Diff("tank/ds@s1", "tank/ds@s2"); err == nil {
		t.Fatal("want ioctl error")
	}
}

// diffFake dispatches the DIFF / OBJ_TO_STATS / NEXT_OBJ ioctls for the
// classification tests. It writes the supplied ranges to the pipe on DIFF and
// answers per-object stats from the from/to maps; NEXT_OBJ walks fromObjs.
type diffFake struct {
	t       *testing.T
	ranges  []diffRange
	from    map[uint64]*objStat // object -> stat in fromSnap (nil/absent => ENOENT)
	to      map[uint64]*objStat // object -> stat in toSnap
	fromSeq []uint64            // sorted allocated objects in fromSnap for NEXT_OBJ
}

func (d *diffFake) ioctl(_ *Handle, req uintptr, cmd *zfsCmd) error {
	switch req {
	case ZFS_IOC_DIFF:
		writeDiffRecords(cmd, d.ranges)
		return nil
	case ZFS_IOC_OBJ_TO_STATS:
		obj := cmd.getU64(offZcObj)
		name := cstr(cmd.buf[offZcName : offZcName+maxNameLen])
		var m map[uint64]*objStat
		if name == "tank/ds@s2" {
			m = d.to
		} else {
			m = d.from
		}
		sb, ok := m[obj]
		if !ok || sb == nil {
			return unix.ENOENT
		}
		putStat(cmd, sb.gen, sb.mode, sb.links, sb.ctime0, sb.path)
		return nil
	case ZFS_IOC_NEXT_OBJ:
		after := cmd.getU64(offZcObj)
		for _, o := range d.fromSeq {
			if o > after {
				cmd.setU64(offZcObj, o)
				return nil
			}
		}
		return unix.ESRCH
	default:
		d.t.Fatalf("unexpected ioctl %#x", req)
		return nil
	}
}

func TestDiffClassifyInuse(t *testing.T) {
	defer snapshotSeams()()
	const reg = sIFREG | 0o644
	const dir = sIFDIR | 0o755
	df := &diffFake{
		t: t,
		// object 5 has no stat (resolves to absent -> skipped); 20..33 are user objects.
		ranges: []diffRange{{typ: DDR_INUSE, first: 5, last: 33}},
		from: map[uint64]*objStat{
			20: {gen: 1, mode: reg, links: 1, ctime0: 100, path: "/a"},     // modified (ctime differs, same path)
			21: {gen: 1, mode: reg, links: 1, ctime0: 100, path: "/old"},   // renamed (path differs)
			22: {gen: 1, mode: reg, links: 1, ctime0: 100, path: "/gone"},  // removed (absent in to)
			23: {gen: 5, mode: reg, links: 1, ctime0: 100, path: "/re"},    // gen differs -> removed+added
			24: {gen: 1, mode: reg, links: 3, ctime0: 100, path: "/lk"},    // link change present both, change>0 -> fsb
			25: {gen: 1, mode: reg, links: 1, ctime0: 100, path: "/same"},  // no change at all
			27: {gen: 1, mode: dir, links: 1, ctime0: 100, path: "/d"},     // dir, ctime change -> modified (change forced 0)
			28: {gen: 1, mode: reg, links: 1, ctime0: 100, path: "/typ"},   // type change -> rem+add (gen bumped)
			29: {gen: 1, mode: reg, links: 5, ctime0: 100, path: "/lkneg"}, // link change<0 present-both -> tsb path
		},
		to: map[uint64]*objStat{
			20: {gen: 1, mode: reg, links: 1, ctime0: 200, path: "/a"},
			21: {gen: 1, mode: reg, links: 1, ctime0: 200, path: "/new"},
			23: {gen: 6, mode: reg, links: 1, ctime0: 200, path: "/re"},
			24: {gen: 1, mode: reg, links: 5, ctime0: 200, path: "/lk"}, // links 3->5 (>0)
			25: {gen: 1, mode: reg, links: 1, ctime0: 100, path: "/same"},
			27: {gen: 1, mode: dir, links: 1, ctime0: 200, path: "/d"},
			28: {gen: 1, mode: dir, links: 1, ctime0: 200, path: "/typ"},   // mode changed reg->dir
			29: {gen: 1, mode: reg, links: 2, ctime0: 200, path: "/lkneg"}, // links 5->2 (<0)
			31: {gen: 9, mode: reg, links: 1, ctime0: 200, path: "/added"}, // added (absent in from)
		},
	}
	ioctlFn = df.ioctl
	entries, err := okHandle().Diff("tank/ds@s1", "tank/ds@s2")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	count := map[DiffChange]int{}
	for _, e := range entries {
		count[e.Change]++
	}
	if count[Renamed] != 1 {
		t.Errorf("want 1 Renamed, got %d (%+v)", count[Renamed], entries)
	}
	if count[Added] < 2 {
		t.Errorf("want >=2 Added, got %d", count[Added])
	}
	if count[Removed] < 2 {
		t.Errorf("want >=2 Removed, got %d", count[Removed])
	}
	if count[Modified] < 2 {
		t.Errorf("want >=2 Modified, got %d", count[Modified])
	}
}

// TestDiffBothAbsent covers the "unallocated in both snapshots" branch: an
// object reported in an INUSE range that resolves to absent in both snaps
// produces no entry.
func TestDiffBothAbsent(t *testing.T) {
	defer snapshotSeams()()
	df := &diffFake{
		t:      t,
		ranges: []diffRange{{typ: DDR_INUSE, first: 20, last: 20}},
		from:   map[uint64]*objStat{},
		to:     map[uint64]*objStat{},
	}
	ioctlFn = df.ioctl
	entries, err := okHandle().Diff("tank/ds@s1", "tank/ds@s2")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("want no entries, got %+v", entries)
	}
}

func TestDiffClassifyFree(t *testing.T) {
	defer snapshotSeams()()
	const reg = sIFREG | 0o644
	df := &diffFake{
		t:       t,
		ranges:  []diffRange{{typ: DDR_FREE, first: 20, last: 30}},
		fromSeq: []uint64{21, 25, 40}, // 40 is past last -> stops; 21,25 reported
		from: map[uint64]*objStat{
			21: {gen: 1, mode: reg, links: 1, ctime0: 1, path: "/freed1"},
			25: {gen: 1, mode: reg, links: 1, ctime0: 1, path: "/freed2"},
		},
		to: map[uint64]*objStat{},
	}
	ioctlFn = df.ioctl
	entries, err := okHandle().Diff("tank/ds@s1", "tank/ds@s2")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	removed := 0
	for _, e := range entries {
		if e.Change == Removed {
			removed++
		}
	}
	if removed != 2 {
		t.Fatalf("want 2 removed, got %d (%+v)", removed, entries)
	}
}

func TestDiffBadRecordType(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(_ *Handle, req uintptr, cmd *zfsCmd) error {
		if req == ZFS_IOC_DIFF {
			writeDiffRecords(cmd, []diffRange{{typ: 0x99, first: 20, last: 20}})
		}
		return nil
	}
	if _, err := okHandle().Diff("tank/ds@s1", "tank/ds@s2"); err == nil {
		t.Fatal("want bad-record-type error")
	}
}

func TestDiffObjToStatsError(t *testing.T) {
	defer snapshotSeams()()
	// DIFF writes one INUSE range; OBJ_TO_STATS returns a non-benign errno.
	ioctlFn = func(_ *Handle, req uintptr, cmd *zfsCmd) error {
		switch req {
		case ZFS_IOC_DIFF:
			writeDiffRecords(cmd, []diffRange{{typ: DDR_INUSE, first: 20, last: 20}})
			return nil
		case ZFS_IOC_OBJ_TO_STATS:
			return unix.EIO
		default:
			return nil
		}
	}
	if _, err := okHandle().Diff("tank/ds@s1", "tank/ds@s2"); err == nil {
		t.Fatal("want OBJ_TO_STATS error")
	}
}

func TestDiffNextObjError(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(_ *Handle, req uintptr, cmd *zfsCmd) error {
		switch req {
		case ZFS_IOC_DIFF:
			writeDiffRecords(cmd, []diffRange{{typ: DDR_FREE, first: 20, last: 30}})
			return nil
		case ZFS_IOC_NEXT_OBJ:
			return unix.EIO
		default:
			return nil
		}
	}
	if _, err := okHandle().Diff("tank/ds@s1", "tank/ds@s2"); err == nil {
		t.Fatal("want NEXT_OBJ error")
	}
}

// TestDiffReaderError covers the res.err path: the kernel writes a partial
// diff record then the ioctl returns success, so the record reader fails.
func TestDiffReaderError(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(_ *Handle, req uintptr, cmd *zfsCmd) error {
		if req == ZFS_IOC_DIFF {
			fd := int(cmd.getU64(offZcCookie))
			_, _ = unix.Write(fd, []byte{1, 2, 3}) // partial record
		}
		return nil
	}
	if _, err := okHandle().Diff("tank/ds@s1", "tank/ds@s2"); err == nil {
		t.Fatal("want reader (short-record) error")
	}
}

// TestDiffToSnapStatError covers classifyInuseObj's terr (toSnap stat) error:
// fromSnap resolves but toSnap returns a non-benign errno.
func TestDiffToSnapStatError(t *testing.T) {
	defer snapshotSeams()()
	const reg = sIFREG | 0o644
	ioctlFn = func(_ *Handle, req uintptr, cmd *zfsCmd) error {
		switch req {
		case ZFS_IOC_DIFF:
			writeDiffRecords(cmd, []diffRange{{typ: DDR_INUSE, first: 20, last: 20}})
			return nil
		case ZFS_IOC_OBJ_TO_STATS:
			name := cstr(cmd.buf[offZcName : offZcName+maxNameLen])
			if name == "tank/ds@s2" { // toSnap
				return unix.EIO
			}
			putStat(cmd, 1, reg, 1, 100, "/x") // fromSnap OK
			return nil
		default:
			return nil
		}
	}
	if _, err := okHandle().Diff("tank/ds@s1", "tank/ds@s2"); err == nil {
		t.Fatal("want toSnap stat error")
	}
}

// TestDiffFreeRangeBranches covers the DDR_FREE loop: two absent objects
// (skipped because OBJ_TO_STATS returns ENOENT), a reported removal, and
// finally ESRCH ending the walk.
func TestDiffFreeRangeBranches(t *testing.T) {
	defer snapshotSeams()()
	const reg = sIFREG | 0o644
	// NEXT_OBJ yields 8 (absent), 19 (absent), 22 (removed), then ESRCH.
	seq := []uint64{8, 19, 22}
	idx := 0
	ioctlFn = func(_ *Handle, req uintptr, cmd *zfsCmd) error {
		switch req {
		case ZFS_IOC_DIFF:
			writeDiffRecords(cmd, []diffRange{{typ: DDR_FREE, first: 5, last: 40}})
			return nil
		case ZFS_IOC_NEXT_OBJ:
			if idx < len(seq) {
				cmd.setU64(offZcObj, seq[idx])
				idx++
				return nil
			}
			return unix.ESRCH
		case ZFS_IOC_OBJ_TO_STATS:
			obj := cmd.getU64(offZcObj)
			if obj == 22 {
				putStat(cmd, 1, reg, 1, 1, "/freed")
				return nil
			}
			return unix.ENOENT // 19 absent
		default:
			return nil
		}
	}
	entries, err := okHandle().Diff("tank/ds@s1", "tank/ds@s2")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(entries) != 1 || entries[0].Change != Removed || entries[0].Path != "/freed" {
		t.Fatalf("want one Removed /freed, got %+v", entries)
	}
}

// TestDiffFreeRangeStatError covers classifyFreeRange's objToStats error path.
func TestDiffFreeRangeStatError(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(_ *Handle, req uintptr, cmd *zfsCmd) error {
		switch req {
		case ZFS_IOC_DIFF:
			writeDiffRecords(cmd, []diffRange{{typ: DDR_FREE, first: 20, last: 30}})
			return nil
		case ZFS_IOC_NEXT_OBJ:
			cmd.setU64(offZcObj, 22)
			return nil
		case ZFS_IOC_OBJ_TO_STATS:
			return unix.EIO
		default:
			return nil
		}
	}
	if _, err := okHandle().Diff("tank/ds@s1", "tank/ds@s2"); err == nil {
		t.Fatal("want free-range stat error")
	}
}

// TestDiffSetNameInObjOps covers the setName-too-long error inside objToStats
// and nextObj. The DIFF ioctl runs against a valid name, but the diff records
// reference object ranges; we make the snapshot name passed to the per-object
// ops overflow by using a from/to name that is valid for zc_name (<=MAXPATHLEN)
// yet... since the same name is reused, instead we unit-test the helpers
// directly with an over-long dataset name.
func TestDiffObjOpHelpersNameError(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	long := string(make([]byte, maxPathLen))
	if _, err := okHandle().objToStats(long, 20); err == nil {
		t.Fatal("want objToStats setName error")
	}
	if _, err := okHandle().nextObj(long, 20); err == nil {
		t.Fatal("want nextObj setName error")
	}
}

func TestReadDiffRangesShort(t *testing.T) {
	// A partial trailing record is an error.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = w.Write([]byte{1, 2, 3}) // < sizeofDiffRecord
		_ = w.Close()
	}()
	if _, err := readDiffRanges(r); err == nil {
		t.Fatal("want short-record error")
	}
	_ = r.Close()
}
