// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"fmt"
	"io"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

// Diff reports the changes between two snapshots (or a snapshot and the live
// head) of the same dataset, via ZFS_IOC_DIFF. It returns one DiffEntry per
// changed path, mirroring `zfs diff <fromSnap> <toSnapOrFs>`.
//
// The kernel diff ioctl streams fixed 24-byte dmu_diff_record_t ranges (of
// freed or in-use DMU objects) over a pipe whose write end is passed in
// zc_cookie; zc_value carries the "from" snapshot and zc_name the "to"
// snapshot/filesystem (note the kernel's dmu_diff(tosnap, fromsnap) order). We
// read the records from the pipe in a goroutine — exactly the
// pipe+goroutine pattern Send uses, but with the kernel as the writer — then
// resolve every object to a path and stat in both snapshots via
// ZFS_IOC_OBJ_TO_STATS / ZFS_IOC_NEXT_OBJ and classify the change with the same
// generation/link-count rules as libzfs's write_inuse_diffs_one / describe_free.
//
// Both snapshots must be of the same dataset (or fromSnap a snapshot and
// toSnapOrFs the live filesystem). Resolving paths requires the snapshots'
// keys to be loaded for an encrypted dataset (the kernel returns EACCES
// otherwise).
func (h *Handle) Diff(fromSnap, toSnapOrFs string) ([]DiffEntry, error) {
	if !strings.ContainsRune(fromSnap, '@') {
		return nil, fmt.Errorf("Diff: from %q is not a snapshot (missing '@')", fromSnap)
	}

	rPipe, wPipe, err := osPipe()
	if err != nil {
		return nil, fmt.Errorf("Diff: pipe: %w", err)
	}
	// Read the raw diff-record stream from the read end concurrently with the
	// ioctl, so a full pipe buffer never deadlocks the kernel writer.
	type readResult struct {
		recs []diffRange
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		recs, rerr := readDiffRanges(rPipe)
		_ = rPipe.Close()
		done <- readResult{recs, rerr}
	}()

	cmd := &zfsCmd{}
	// zc_name = tosnap, zc_value = fromsnap (the libzfs ordering).
	if err := cmd.setName(toSnapOrFs); err != nil {
		_ = wPipe.Close()
		_ = rPipe.Close()
		return nil, err
	}
	if err := cmd.setValue(fromSnap); err != nil {
		_ = wPipe.Close()
		_ = rPipe.Close()
		return nil, err
	}
	cmd.setU64(offZcCookie, uint64(wPipe.Fd()))

	ioErr := h.ioctl(ZFS_IOC_DIFF, cmd)
	runtime.KeepAlive(wPipe)
	// Close the write end so the reader sees EOF; the kernel has finished
	// writing by the time the ioctl returns.
	_ = wPipe.Close()
	res := <-done

	if ioErr != nil {
		return nil, fmt.Errorf("ZFS_IOC_DIFF %q -> %q: %w", fromSnap, toSnapOrFs, ioErr)
	}
	if res.err != nil {
		return nil, fmt.Errorf("Diff %q -> %q: read records: %w", fromSnap, toSnapOrFs, res.err)
	}

	return h.classifyDiff(fromSnap, toSnapOrFs, res.recs)
}

// diffRange is a decoded dmu_diff_record_t: a [first,last] object range of a
// given type (DDR_INUSE or DDR_FREE).
type diffRange struct {
	typ   uint64
	first uint64
	last  uint64
}

// readDiffRanges reads fixed-size dmu_diff_record_t records from r until EOF,
// decoding each into a diffRange. A clean EOF at a record boundary ends the
// stream; a partial trailing record is an error (matching libzfs's EPIPE).
func readDiffRanges(r io.Reader) ([]diffRange, error) {
	var out []diffRange
	rec := make([]byte, sizeofDiffRecord)
	for {
		n, err := io.ReadFull(r, rec)
		if err == io.EOF && n == 0 {
			return out, nil // clean end at a record boundary
		}
		if err != nil {
			return out, fmt.Errorf("short diff record (%d of %d bytes): %w",
				n, sizeofDiffRecord, err)
		}
		out = append(out, diffRange{
			typ:   hostBO.Uint64(rec[offDdrType : offDdrType+8]),
			first: hostBO.Uint64(rec[offDdrFirst : offDdrFirst+8]),
			last:  hostBO.Uint64(rec[offDdrLast : offDdrLast+8]),
		})
	}
}

// classifyDiff turns the raw object ranges into DiffEntry rows, resolving each
// object's path and stat in both snapshots. DDR_INUSE ranges are walked object
// by object (write_inuse_diffs); DDR_FREE ranges are walked with NEXT_OBJ over
// the "from" snapshot to find the objects that existed there and are now gone
// (write_free_diffs / describe_free).
func (h *Handle) classifyDiff(fromSnap, toSnap string, recs []diffRange) ([]DiffEntry, error) {
	var out []DiffEntry
	for _, dr := range recs {
		switch dr.typ {
		case DDR_INUSE:
			for o := dr.first; o <= dr.last; o++ {
				es, err := h.classifyInuseObj(fromSnap, toSnap, o)
				if err != nil {
					return nil, err
				}
				out = append(out, es...)
			}
		case DDR_FREE:
			es, err := h.classifyFreeRange(fromSnap, dr)
			if err != nil {
				return nil, err
			}
			out = append(out, es...)
		default:
			return nil, fmt.Errorf("Diff: bad diff record type %#x", dr.typ)
		}
	}
	return out, nil
}

// objStat is the per-snapshot lookup result for one object: its path and stat,
// plus whether the object was present (absent => ENOENT/ENOTSUP in that snap).
type objStat struct {
	path    string
	gen     uint64
	mode    uint64
	links   uint64
	ctime0  uint64
	ctime1  uint64
	present bool
}

// classifyInuseObj resolves object o in both snapshots and emits the resulting
// DiffEntry rows, applying the same rules as libzfs write_inuse_diffs_one:
//   - present only in "to"   => Added (or a link change)
//   - present only in "from" => Removed (or a link change)
//   - present in both, same gen, same ctime => no change
//   - same gen, different ctime, same path  => Modified
//   - same gen, different ctime, diff path   => Renamed
//   - different gen                          => Removed(from) + Added(to)
func (h *Handle) classifyInuseObj(fromSnap, toSnap string, o uint64) ([]DiffEntry, error) {
	// Unlike a naive "object number < N" filter, libzfs's write_inuse_diffs
	// processes every object in the range and relies on ZFS_IOC_OBJ_TO_STATS to
	// report which objects are real ZPL paths: a ZPL system object (master node,
	// delete queue, shares dir, …) resolves to no path (ENOENT/ENOTSUP/ESTALE),
	// surfacing here as present=false, and is skipped below. User files can have
	// very low object numbers (2, 3, …), so we must NOT skip on object number.
	fsb, ferr := h.objToStats(fromSnap, o)
	if ferr != nil {
		return nil, ferr
	}
	tsb, terr := h.objToStats(toSnap, o)
	if terr != nil {
		return nil, terr
	}
	// Unallocated object sharing the meta-dnode block in both snapshots.
	if !fsb.present && !tsb.present {
		return nil, nil
	}

	// An object present on only one side is a clean add or remove: the absent
	// side has zero links, which (per the guard below) always forces a zero
	// link-delta, so libzfs's "link change" sub-case cannot fire here.
	if !fsb.present {
		return []DiffEntry{fileEntry(Added, tsb)}, nil
	}
	if !tsb.present {
		return []DiffEntry{fileEntry(Removed, fsb)}, nil
	}

	fmode := fsb.mode & sIFMT
	tmode := tsb.mode & sIFMT
	change := int64(0)
	if !(fmode == sIFDIR || tmode == sIFDIR || fsb.links == 0 || tsb.links == 0) {
		change = int64(tsb.links) - int64(fsb.links)
	}

	// Force a generational difference when only the type changed.
	tgen := tsb.gen
	if fmode != tmode && fsb.gen == tgen {
		tgen++
	}

	if fsb.gen == tgen {
		if fsb.ctime0 == tsb.ctime0 && fsb.ctime1 == tsb.ctime1 {
			return nil, nil // no apparent change
		}
		if change != 0 {
			if change > 0 {
				return []DiffEntry{linkChangeEntry(change, fsb)}, nil
			}
			return []DiffEntry{linkChangeEntry(change, tsb)}, nil
		}
		if fsb.path == tsb.path {
			return []DiffEntry{fileEntry(Modified, tsb)}, nil
		}
		return []DiffEntry{renameEntry(fsb, tsb)}, nil
	}
	// Re-created or object re-used: removed then added.
	return []DiffEntry{fileEntry(Removed, fsb), fileEntry(Added, tsb)}, nil
}

// classifyFreeRange handles a DDR_FREE range: it walks the "from" snapshot with
// ZFS_IOC_NEXT_OBJ from first-1, emitting a Removed entry for each allocated
// object in the range (objects freed between the snapshots). Mirrors libzfs
// write_free_diffs / describe_free.
func (h *Handle) classifyFreeRange(fromSnap string, dr diffRange) ([]DiffEntry, error) {
	var out []DiffEntry
	obj := dr.first - 1
	for obj < dr.last {
		next, err := h.nextObj(fromSnap, obj)
		if err == unix.ESRCH {
			break // no more allocated objects
		}
		if err != nil {
			return nil, fmt.Errorf("ZFS_IOC_NEXT_OBJ %q (> %d): %w", fromSnap, obj, err)
		}
		obj = next
		if obj > dr.last {
			break
		}
		sb, serr := h.objToStats(fromSnap, obj)
		if serr != nil {
			return nil, serr
		}
		// Object on the delete queue (ESTALE) or already gone (absent) is not
		// reported, matching describe_free.
		if !sb.present {
			continue
		}
		out = append(out, fileEntry(Removed, sb))
	}
	return out, nil
}

// objToStats issues ZFS_IOC_OBJ_TO_STATS for object o in dataset ds, returning
// the object's path + stat. A missing object (ENOENT / ENOTSUP / ESTALE — the
// errnos libzfs treats as "not present in this snapshot") yields present=false
// with no error; any other errno is returned.
func (h *Handle) objToStats(ds string, o uint64) (objStat, error) {
	cmd := &zfsCmd{}
	if err := cmd.setName(ds); err != nil {
		return objStat{}, err
	}
	cmd.setU64(offZcObj, o)
	err := h.ioctl(ZFS_IOC_OBJ_TO_STATS, cmd)

	// The kernel fills zc_stat regardless of whether it could resolve a path.
	sb := objStat{
		gen:    cmd.getU64(offZsGen),
		mode:   cmd.getU64(offZsMode),
		links:  cmd.getU64(offZsLinks),
		ctime0: cmd.getU64(offZsCtime0),
		ctime1: cmd.getU64(offZsCtime1),
	}
	if err == nil {
		sb.path = cstr(cmd.buf[offZcValue : offZcValue+maxPathLen*2])
		sb.present = true
		return sb, nil
	}
	switch err {
	case unix.ENOENT, unix.ENOTSUP, unix.ESTALE:
		// Not present in this snapshot (or a non-ZPL object); not an error.
		return objStat{}, nil
	default:
		return objStat{}, fmt.Errorf("ZFS_IOC_OBJ_TO_STATS %q obj %d: %w", ds, o, err)
	}
}

// nextObj issues ZFS_IOC_NEXT_OBJ for dataset ds, returning the number of the
// next allocated object strictly after `after`. ESRCH (no more objects) is
// returned verbatim so the caller can stop.
func (h *Handle) nextObj(ds string, after uint64) (uint64, error) {
	cmd := &zfsCmd{}
	if err := cmd.setName(ds); err != nil {
		return 0, err
	}
	cmd.setU64(offZcObj, after)
	if err := h.ioctl(ZFS_IOC_NEXT_OBJ, cmd); err != nil {
		return 0, err
	}
	return cmd.getU64(offZcObj), nil
}

// fileEntry builds a single-path DiffEntry of the given change from a stat.
func fileEntry(change DiffChange, sb objStat) DiffEntry {
	return DiffEntry{
		Change: change,
		Type:   fileTypeFromMode(sb.mode),
		Path:   sb.path,
		Object: 0, // object number is per-range; left 0 for file entries
	}
}

// renameEntry builds a Renamed DiffEntry from the from/to stats.
func renameEntry(fsb, tsb objStat) DiffEntry {
	return DiffEntry{
		Change:  Renamed,
		Type:    fileTypeFromMode(tsb.mode),
		Path:    tsb.path,
		OldPath: fsb.path,
	}
}

// linkChangeEntry builds the DiffEntry for a hard-link count change. ZFS diff
// renders these as a Modified ('M') on the object whose link set changed; we
// report Modified to keep the marker set aligned with `zfs diff` (which prints
// 'M' for link-count deltas on a name).
func linkChangeEntry(change int64, sb objStat) DiffEntry {
	return DiffEntry{
		Change: Modified,
		Type:   fileTypeFromMode(sb.mode),
		Path:   sb.path,
	}
}
