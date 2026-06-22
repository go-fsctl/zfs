// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"os"

	"golang.org/x/sys/unix"
)

// Indirection seams over the operating-system and ioctl primitives this package
// drives. They exist so the success and error branches of every kernel call —
// which only trigger against a live /dev/zfs that is impractical (and here
// impossible: the test host has no zfs module) to provoke — can be exercised
// deterministically by fault-injecting fakes in tests, without a real device or
// root. Production code uses the real implementations assigned here; tests swap
// a var, run, and restore it.
//
// ioctlFn is the single choke point every ZFS_IOC_* call funnels through
// (h.ioctl delegates to it). A fake can both return any errno AND mutate the
// supplied *zfsCmd buffer to emulate the kernel's write-back (e.g. the decoded
// dst nvlist, zc_cookie for the list iterator, or zc_string for PROMOTE),
// making the whole library coverable purely with fakes.
var (
	osOpenFile = os.OpenFile
	unixAccess = unix.Access

	ioctlFn = realIoctl

	// encodeNative is the nvlist packer used by the zfs_cmd_t src/conf helpers.
	// Seamed so the (otherwise input-validated, hence hard-to-provoke) encode
	// failure branch of each builder is fault-injectable.
	encodeNative = EncodeNative
)

// dstHook, when non-nil, is invoked with the freshly-allocated zc_nvlist_dst
// buffer at each call site that issues a dst-returning ioctl. It is nil in
// production (zero cost); tests set it so a fake ioctlFn can emulate the
// kernel's nvlist write-back into the very slice the caller will decode,
// without reconstructing a pointer from the packed zfs_cmd_t (which would be
// an unsafe uintptr round-trip). See coverage_linux_test.go.
var dstHook func([]byte)

func noteDst(dst []byte) {
	if dstHook != nil {
		dstHook(dst)
	}
}
