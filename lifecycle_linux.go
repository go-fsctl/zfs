// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Clone creates a new filesystem `newFs` cloned from `snapshot` via
// ZFS_IOC_CLONE (the libzfs_core lzc_clone path). zc_name carries the new
// dataset name; the input nvlist mirrors lzc_clone(fsname, origin, props):
//
//	{
//	  "origin": <string snapshot "pool/ds@snap">,
//	  "props":  { ... }   // optional, omitted when empty
//	}
//
// The origin snapshot and the clone must live in the same pool. Per-property
// errors (if any) are returned by the kernel in the outnvl and surfaced in the
// error. Mirrors OpenZFS 2.2.2 zfs_ioc_clone().
func (h *Handle) Clone(snapshot, newFs string, props Nvlist) error {
	if !strings.ContainsRune(snapshot, '@') {
		return fmt.Errorf("Clone: origin %q is not a snapshot (missing '@')", snapshot)
	}
	innvl := Nvlist{"origin": snapshot}
	if len(props) > 0 {
		innvl["props"] = props
	}
	out, err := h.callNewName(ZFS_IOC_CLONE, newFs, innvl)
	if err != nil {
		return fmt.Errorf("ZFS_IOC_CLONE %q from %q: %w", newFs, snapshot, err)
	}
	if e := firstErrlistErr(out); e != nil {
		return fmt.Errorf("ZFS_IOC_CLONE %q from %q: %w", newFs, snapshot, e)
	}
	return nil
}

// Rollback rolls the filesystem `fs` back to its most recent snapshot via
// ZFS_IOC_ROLLBACK (the libzfs_core lzc_rollback path), discarding all changes
// made since. It returns the full name of the snapshot rolled back to (the
// kernel reports it in outnvl["target"]). All snapshots and bookmarks created
// after that snapshot must already be destroyed, and the dataset must be
// unmounted (the kernel forces this). Mirrors OpenZFS 2.2.2 zfs_ioc_rollback()
// with a NULL "target" (roll to latest). For an explicit target, use
// RollbackTo.
func (h *Handle) Rollback(fs string) (string, error) {
	return h.rollback(fs, "")
}

// RollbackTo rolls `fs` back to the named snapshot `target` ("pool/ds@snap" or
// the bare "snap" in fs) via ZFS_IOC_ROLLBACK with innvl["target"] set. The
// kernel still requires that snapshots newer than `target` have been destroyed.
func (h *Handle) RollbackTo(fs, target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("RollbackTo: empty target snapshot")
	}
	return h.rollback(fs, target)
}

func (h *Handle) rollback(fs, target string) (string, error) {
	var innvl Nvlist
	if target != "" {
		innvl = Nvlist{"target": target}
	}
	out, err := h.callNewName(ZFS_IOC_ROLLBACK, fs, innvl)
	if err != nil {
		return "", fmt.Errorf("ZFS_IOC_ROLLBACK %q: %w", fs, err)
	}
	if out != nil {
		if t, ok := out["target"].(string); ok {
			return t, nil
		}
	}
	return "", nil
}

// Hold places a user hold tagged `tag` on `snapshot` via ZFS_IOC_HOLD (the
// libzfs_core lzc_hold path). A held snapshot cannot be destroyed (the destroy
// fails with EBUSY) until every hold is released. When recursive is true, the
// same-named snapshot is also held on every descendant filesystem of the
// snapshot's dataset that has it (matching `zfs hold -r`); the enumeration is
// performed here and all holds are applied in a single atomic ioctl.
//
// The input nvlist mirrors lzc_hold(holds, cleanup_fd, errlist):
//
//	{
//	  "holds": { "<pool/ds@snap>": "<tag>", ... },  // nvlist of snap->tag
//	}
//
// zc_name carries the pool. We pass no cleanup_fd, so the holds are persistent
// (not tied to the lifetime of an open /dev/zfs fd). Mirrors OpenZFS 2.2.2
// zfs_ioc_hold().
func (h *Handle) Hold(snapshot, tag string, recursive bool) error {
	if tag == "" {
		return fmt.Errorf("Hold: empty tag")
	}
	at := strings.IndexByte(snapshot, '@')
	if at < 0 {
		return fmt.Errorf("Hold: %q is not a snapshot (missing '@')", snapshot)
	}
	holds := Nvlist{snapshot: tag}
	if recursive {
		fs := snapshot[:at]
		snapName := snapshot[at:] // includes leading '@'
		kids, err := h.descendantFilesystems(fs)
		if err != nil {
			return fmt.Errorf("Hold %q recursive: enumerate descendants: %w", snapshot, err)
		}
		for _, kid := range kids {
			ks := kid + snapName
			// Only hold descendants that actually have the snapshot.
			if _, err := h.ObjsetStats(ks); err == nil {
				holds[ks] = tag
			}
		}
	}
	src := Nvlist{"holds": holds}
	out, err := h.callNewName(ZFS_IOC_HOLD, poolOf(snapshot), src)
	if err != nil {
		return fmt.Errorf("ZFS_IOC_HOLD %q tag %q: %w", snapshot, tag, err)
	}
	if e := firstErrlistErr(out); e != nil {
		return fmt.Errorf("ZFS_IOC_HOLD %q tag %q: %w", snapshot, tag, e)
	}
	return nil
}

// Release removes the hold tagged `tag` from `snapshot` via ZFS_IOC_RELEASE
// (the libzfs_core lzc_release path). Once the last hold is released the
// snapshot can be destroyed again (and, if it was destroyed with deferred
// destruction pending, it is freed). The input nvlist mirrors
// lzc_release(holds, errlist):
//
//	{ "<pool/ds@snap>": { "<tag>": <bool> } }
//
// zc_name carries the pool. Mirrors OpenZFS 2.2.2 zfs_ioc_release().
func (h *Handle) Release(snapshot, tag string) error {
	if tag == "" {
		return fmt.Errorf("Release: empty tag")
	}
	if !strings.ContainsRune(snapshot, '@') {
		return fmt.Errorf("Release: %q is not a snapshot (missing '@')", snapshot)
	}
	src := Nvlist{snapshot: Nvlist{tag: true}}
	out, err := h.callNewName(ZFS_IOC_RELEASE, poolOf(snapshot), src)
	if err != nil {
		return fmt.Errorf("ZFS_IOC_RELEASE %q tag %q: %w", snapshot, tag, err)
	}
	if e := firstErrlistErr(out); e != nil {
		return fmt.Errorf("ZFS_IOC_RELEASE %q tag %q: %w", snapshot, tag, e)
	}
	return nil
}

// Holds returns the user holds on `snapshot` as a map of tag -> creation time
// (seconds since the Unix epoch) via ZFS_IOC_GET_HOLDS (the libzfs_core
// lzc_get_holds path). zc_name carries the snapshot; the kernel returns
// outnvl = { "<tag>": <uint64 timestamp> }. Mirrors OpenZFS 2.2.2
// zfs_ioc_get_holds().
func (h *Handle) Holds(snapshot string) (map[string]uint64, error) {
	if !strings.ContainsRune(snapshot, '@') {
		return nil, fmt.Errorf("Holds: %q is not a snapshot (missing '@')", snapshot)
	}
	out, err := h.callNewName(ZFS_IOC_GET_HOLDS, snapshot, nil)
	if err != nil {
		return nil, fmt.Errorf("ZFS_IOC_GET_HOLDS %q: %w", snapshot, err)
	}
	res := make(map[string]uint64, len(out))
	for tag, v := range out {
		if ts, ok := v.(uint64); ok {
			res[tag] = ts
		}
	}
	return res, nil
}

// Bookmark creates a bookmark `bookmark` of `snapshot` via ZFS_IOC_BOOKMARK
// (the libzfs_core lzc_bookmark path). `snapshot` is "pool/ds@snap" and
// `bookmark` is "pool/ds#bmark" in the same dataset. A bookmark is a lightweight
// marker (just a creation txg / guid) that lets the snapshot it was created
// from be destroyed while still serving as the "from" of an incremental send.
//
// The input nvlist mirrors lzc_bookmark(bookmarks, errlist):
//
//	{ "<pool/ds#bmark>": "<pool/ds@snap>" }   // newbookmark -> source
//
// zc_name carries the pool. The kernel applies all entries atomically and
// returns any per-entry errors in the outnvl. Mirrors OpenZFS 2.2.2
// zfs_ioc_bookmark().
func (h *Handle) Bookmark(snapshot, bookmark string) error {
	if !strings.ContainsRune(snapshot, '@') {
		return fmt.Errorf("Bookmark: source %q is not a snapshot (missing '@')", snapshot)
	}
	if !strings.ContainsRune(bookmark, '#') {
		return fmt.Errorf("Bookmark: target %q is not a bookmark (missing '#')", bookmark)
	}
	src := Nvlist{bookmark: snapshot}
	out, err := h.callNewName(ZFS_IOC_BOOKMARK, poolOf(bookmark), src)
	if err != nil {
		return fmt.Errorf("ZFS_IOC_BOOKMARK %q -> %q: %w", snapshot, bookmark, err)
	}
	if e := firstErrlistErr(out); e != nil {
		return fmt.Errorf("ZFS_IOC_BOOKMARK %q -> %q: %w", snapshot, bookmark, e)
	}
	return nil
}

// GetBookmarks returns the bookmarks of filesystem `fs` via
// ZFS_IOC_GET_BOOKMARKS (the libzfs_core lzc_get_bookmarks path). The result
// maps each bookmark's short name (the part after '#') to its properties
// nvlist; the standard props (guid, createtxg, creation) are always requested.
// zc_name carries the filesystem; the input nvlist is the set of property names
// to fetch (each a presence flag). Mirrors OpenZFS 2.2.2 zfs_ioc_get_bookmarks().
func (h *Handle) GetBookmarks(fs string) (map[string]Nvlist, error) {
	// Request the common bookmark properties (presence-only keys).
	props := Nvlist{
		"guid":      Boolean{},
		"createtxg": Boolean{},
		"creation":  Boolean{},
	}
	out, err := h.callNewName(ZFS_IOC_GET_BOOKMARKS, fs, props)
	if err != nil {
		return nil, fmt.Errorf("ZFS_IOC_GET_BOOKMARKS %q: %w", fs, err)
	}
	res := make(map[string]Nvlist, len(out))
	for name, v := range out {
		if sub, ok := v.(Nvlist); ok {
			res[name] = sub
		}
	}
	return res, nil
}

// DestroyBookmarks destroys the named bookmarks via ZFS_IOC_DESTROY_BOOKMARKS
// (the libzfs_core lzc_destroy_bookmarks path). Each name must be a full
// "pool/ds#bmark"; all must be in the same pool. Bookmarks that do not exist are
// silently ignored (matching lzc semantics). The input nvlist is a set of
// bookmark names (each a presence flag); zc_name carries the pool. Mirrors
// OpenZFS 2.2.2 zfs_ioc_destroy_bookmarks().
func (h *Handle) DestroyBookmarks(bookmarks ...string) error {
	if len(bookmarks) == 0 {
		return fmt.Errorf("DestroyBookmarks: no bookmarks given")
	}
	src := make(Nvlist, len(bookmarks))
	for _, b := range bookmarks {
		if !strings.ContainsRune(b, '#') {
			return fmt.Errorf("DestroyBookmarks: %q is not a bookmark (missing '#')", b)
		}
		src[b] = Boolean{}
	}
	out, err := h.callNewName(ZFS_IOC_DESTROY_BOOKMARKS, poolOf(bookmarks[0]), src)
	if err != nil {
		return fmt.Errorf("ZFS_IOC_DESTROY_BOOKMARKS %v: %w", bookmarks, err)
	}
	if e := firstErrlistErr(out); e != nil {
		return fmt.Errorf("ZFS_IOC_DESTROY_BOOKMARKS %v: %w", bookmarks, e)
	}
	return nil
}

// descendantFilesystems returns fs plus every descendant filesystem/volume of
// fs (depth-first), using the legacy ZFS_IOC_DATASET_LIST_NEXT iterator. The
// iterator returns one immediate child per call in zc_name with zc_cookie as
// the resumable offset; ESRCH marks the end of the children of one parent. This
// is the same enumeration `zfs hold -r` performs in userland before issuing the
// hold ioctl.
func (h *Handle) descendantFilesystems(fs string) ([]string, error) {
	out := []string{fs}
	// Breadth-first over the dataset tree; each level lists immediate children.
	queue := []string{fs}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		kids, err := h.listChildren(parent)
		if err != nil {
			return nil, err
		}
		for _, k := range kids {
			out = append(out, k)
			queue = append(queue, k)
		}
	}
	return out, nil
}

// listChildren returns the immediate child datasets of parent via repeated
// ZFS_IOC_DATASET_LIST_NEXT calls.
func (h *Handle) listChildren(parent string) ([]string, error) {
	var kids []string
	var cookie uint64
	for {
		cmd := &zfsCmd{}
		if err := cmd.setName(parent); err != nil {
			return nil, err
		}
		cmd.setU64(offZcCookie, cookie)
		// Provide a dst buffer: the kernel fills zc_objset_stats props into
		// zc_nvlist_dst; we ignore it but must supply space so the ioctl
		// succeeds for datasets that have properties to report.
		dst := make([]byte, 64*1024)
		cmd.setU64(offZcNvlistDst, uint64(uintptr(unsafe.Pointer(&dst[0]))))
		cmd.setU64(offZcNvlistDstSize, uint64(len(dst)))

		err := h.ioctl(ZFS_IOC_DATASET_LIST_NEXT, cmd)
		runtime.KeepAlive(dst)
		if err == unix.ESRCH {
			break // no more children
		}
		if err != nil {
			return nil, fmt.Errorf("ZFS_IOC_DATASET_LIST_NEXT %q: %w", parent, err)
		}
		name := cstr(cmd.buf[offZcName : offZcName+maxNameLen])
		cookie = cmd.getU64(offZcCookie)
		// Skip snapshots (names containing '@'); list_next yields only
		// filesystems/volumes here, but guard anyway.
		if name == "" || strings.ContainsRune(name, '@') {
			continue
		}
		kids = append(kids, name)
	}
	return kids, nil
}
