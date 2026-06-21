// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import (
	"fmt"
	"syscall"
)

// poolOf returns the pool component of a dataset/snapshot/bookmark name (the
// substring before the first '/', '@' or '#'). It is the value the lzc clone /
// hold / bookmark ioctls expect in zc_name.
func poolOf(name string) string {
	for i := 0; i < len(name); i++ {
		switch name[i] {
		case '/', '@', '#':
			return name[:i]
		}
	}
	return name
}

// firstErrlistErr inspects an lzc-style outnvl that may carry per-item errors.
// The bookmark/hold/clone/release ioctls return any failures as an nvlist
// mapping the offending name to an errno (int32 on the wire); we surface the
// first non-zero one as an error.
func firstErrlistErr(out Nvlist) error {
	for name, v := range out {
		var e int32
		switch t := v.(type) {
		case int32:
			e = t
		case uint64:
			e = int32(t)
		default:
			continue
		}
		if e != 0 {
			return fmt.Errorf("%s: %w", name, syscall.Errno(e))
		}
	}
	return nil
}
