// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import (
	"reflect"
	"syscall"
	"testing"
)

// TestPoolOf checks the pool-name extraction used to fill zc_name for the lzc
// clone/hold/bookmark ioctls.
func TestPoolOf(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"tank", "tank"},
		{"tank/ds", "tank"},
		{"tank/ds/sub", "tank"},
		{"tank/ds@snap", "tank"},
		{"tank@snap", "tank"},
		{"tank/ds#bmark", "tank"},
		{"tank#bmark", "tank"},
	} {
		if got := poolOf(c.in); got != c.want {
			t.Errorf("poolOf(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFirstErrlistErr verifies the per-item error nvlist decoding used by the
// clone/hold/bookmark ioctls.
func TestFirstErrlistErr(t *testing.T) {
	if err := firstErrlistErr(nil); err != nil {
		t.Errorf("nil outnvl: got %v, want nil", err)
	}
	if err := firstErrlistErr(Nvlist{"x": int32(0)}); err != nil {
		t.Errorf("zero errno: got %v, want nil", err)
	}
	err := firstErrlistErr(Nvlist{"tank/ds#b": int32(int32(syscall.EEXIST))})
	if err == nil {
		t.Fatal("expected error for EEXIST entry")
	}
	// uint64-typed errno (some kernels pack it that way) is also surfaced.
	if err := firstErrlistErr(Nvlist{"tank@s": uint64(uint64(syscall.EBUSY))}); err == nil {
		t.Error("expected error for uint64 EBUSY entry")
	}
}

// TestCloneArgsRoundTrip pins the ZFS_IOC_CLONE input nvlist shape: an "origin"
// string plus an optional "props" sub-nvlist (lzc_clone).
func TestCloneArgsRoundTrip(t *testing.T) {
	in := Nvlist{
		"origin": "tank/src@s1",
		"props":  Nvlist{"compression": "lz4", "quota": uint64(1 << 20)},
	}
	roundTrip(t, in)
}

// TestHoldArgsRoundTrip pins the ZFS_IOC_HOLD input nvlist: a "holds" nvlist
// mapping full snapshot names to their tag strings (lzc_hold).
func TestHoldArgsRoundTrip(t *testing.T) {
	in := Nvlist{
		"holds": Nvlist{
			"tank/a@s1": "keep",
			"tank/b@s1": "keep",
		},
	}
	out := roundTrip(t, in)
	holds, ok := out["holds"].(Nvlist)
	if !ok {
		t.Fatalf("holds decoded as %T, want Nvlist", out["holds"])
	}
	if tag, _ := holds["tank/a@s1"].(string); tag != "keep" {
		t.Errorf("holds[tank/a@s1] = %v, want \"keep\"", holds["tank/a@s1"])
	}
}

// TestReleaseArgsRoundTrip pins the ZFS_IOC_RELEASE input nvlist: snapshot ->
// { tag -> bool } (lzc_release).
func TestReleaseArgsRoundTrip(t *testing.T) {
	in := Nvlist{
		"tank/a@s1": Nvlist{"keep": true},
	}
	out := roundTrip(t, in)
	sub, ok := out["tank/a@s1"].(Nvlist)
	if !ok {
		t.Fatalf("release entry decoded as %T, want Nvlist", out["tank/a@s1"])
	}
	if v, _ := sub["keep"].(bool); !v {
		t.Errorf("release tag presence = %v, want true", sub["keep"])
	}
}

// TestBookmarkArgsRoundTrip pins the ZFS_IOC_BOOKMARK input nvlist: newbookmark
// (#name) -> source snapshot (@name) string (lzc_bookmark).
func TestBookmarkArgsRoundTrip(t *testing.T) {
	in := Nvlist{"tank/ds#bm1": "tank/ds@s1"}
	out := roundTrip(t, in)
	if v, _ := out["tank/ds#bm1"].(string); v != "tank/ds@s1" {
		t.Errorf("bookmark source = %v, want tank/ds@s1", out["tank/ds#bm1"])
	}
}

// TestDestroyBookmarksArgsRoundTrip pins the ZFS_IOC_DESTROY_BOOKMARKS /
// GET_BOOKMARKS input nvlist: a set of presence-only keys (lzc).
func TestDestroyBookmarksArgsRoundTrip(t *testing.T) {
	in := Nvlist{
		"tank/ds#bm1": Boolean{},
		"tank/ds#bm2": Boolean{},
	}
	out := roundTrip(t, in)
	if _, ok := out["tank/ds#bm1"].(Boolean); !ok {
		t.Errorf("bm1 = %T, want Boolean (presence flag)", out["tank/ds#bm1"])
	}
}

// TestRollbackArgsRoundTrip pins the ZFS_IOC_ROLLBACK input nvlist: an optional
// "target" snapshot string (lzc_rollback_to). The latest-snapshot form passes
// no innvl, which the helpers handle as a nil source.
func TestRollbackArgsRoundTrip(t *testing.T) {
	in := Nvlist{"target": "tank/ds@s2"}
	out := roundTrip(t, in)
	if v, _ := out["target"].(string); v != "tank/ds@s2" {
		t.Errorf("rollback target = %v, want tank/ds@s2", out["target"])
	}
}

// roundTrip encodes nv with the native codec and decodes it back, failing the
// test on any error, and returns the decoded nvlist.
func roundTrip(t *testing.T, nv Nvlist) Nvlist {
	t.Helper()
	b, err := EncodeNative(nv)
	if err != nil {
		t.Fatalf("EncodeNative: %v", err)
	}
	out, err := DecodeNative(b)
	if err != nil {
		t.Fatalf("DecodeNative: %v", err)
	}
	if !reflect.DeepEqual(nv, out) {
		t.Fatalf("round-trip mismatch:\n in=%#v\nout=%#v", nv, out)
	}
	return out
}
