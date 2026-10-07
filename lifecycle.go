// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import (
	"errors"
	"fmt"
	"sort"
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
		if e := errlistErrno(v); e != 0 {
			return fmt.Errorf("%s: %w", name, syscall.Errno(e))
		}
	}
	return nil
}

// allErrlistErrs is firstErrlistErr for an errlist where every entry matters:
// zfs_set_prop_nvlist (module/zfs/zfs_ioctl.c) is best effort, keeps going
// past a failed property and records EACH failure in the errlist with
// fnvlist_add_int32(errlist, propname, err). It returns every non-zero entry,
// sorted by name so the message is stable, joined with errors.Join (so
// errors.Is matches any of the per-item errnos); nil when there is none.
func allErrlistErrs(out Nvlist) error {
	names := make([]string, 0, len(out))
	for name := range out {
		names = append(names, name)
	}
	sort.Strings(names)
	var errs []error
	for _, name := range names {
		if e := errlistErrno(out[name]); e != 0 {
			errs = append(errs, fmt.Errorf("%s: %w", name, syscall.Errno(e)))
		}
	}
	return errors.Join(errs...)
}

// errlistErrno reads one errlist value: an int32 errno on the wire, tolerated
// as uint64; anything else is not an errno and reads as 0.
func errlistErrno(v Value) int32 {
	switch t := v.(type) {
	case int32:
		return t
	case uint64:
		return int32(t)
	}
	return 0
}
