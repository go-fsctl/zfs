// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import "testing"

func TestDiffChangeString(t *testing.T) {
	for _, c := range []struct {
		ch   DiffChange
		want string
	}{
		{Added, "+"}, {Removed, "-"}, {Modified, "M"}, {Renamed, "R"},
	} {
		if c.ch.String() != c.want {
			t.Errorf("%v.String() = %q, want %q", byte(c.ch), c.ch.String(), c.want)
		}
	}
}

func TestFileTypeFromMode(t *testing.T) {
	for _, c := range []struct {
		mode uint64
		want FileType
	}{
		{sIFREG | 0o644, TypeFile},
		{sIFDIR | 0o755, TypeDir},
		{sIFLNK | 0o777, TypeSymlink},
		{sIFIFO, TypeFIFO},
		{sIFSOCK, TypeSocket},
		{sIFBLK, TypeBlockDev},
		{sIFCHR, TypeCharDev},
		{0, TypeUnknown},
	} {
		if got := fileTypeFromMode(c.mode); got != c.want {
			t.Errorf("fileTypeFromMode(%#o) = %q, want %q", c.mode, byte(got), byte(c.want))
		}
	}
}
