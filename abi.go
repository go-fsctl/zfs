// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import "unsafe"

// ptrOfUint16 is a tiny unsafe helper used for host-endianness detection in
// nvlist.go. Kept here so nvlist.go stays import-light.
func ptrOfUint16(p *uint16) unsafe.Pointer { return unsafe.Pointer(p) }

// ZFS_IOC_* request numbers for OpenZFS on Linux.
//
// On Linux, OpenZFS registers /dev/zfs as a misc character device and uses the
// zfs_ioc_t enum value DIRECTLY as the ioctl request number (it is not
// _IOWR-encoded — the kernel's zfsdev_ioctl computes vecnum = cmd -
// ZFS_IOC_FIRST and dispatches). ZFS_IOC_FIRST = ('Z' << 8) = 0x5a00, and the
// enum is contiguous from there.
//
// Values verified against the 2.2.2 source (include/sys/fs/zfs.h) installed in
// the target guest; the comments below are the upstream-annotated hex values.
const (
	zfsIocFirst = 'Z' << 8 // 0x5a00

	ZFS_IOC_POOL_CREATE    = zfsIocFirst + 0x00 // 0x5a00
	ZFS_IOC_POOL_DESTROY   = zfsIocFirst + 0x01 // 0x5a01
	ZFS_IOC_POOL_IMPORT    = zfsIocFirst + 0x02 // 0x5a02
	ZFS_IOC_POOL_CONFIGS   = zfsIocFirst + 0x04 // 0x5a04
	ZFS_IOC_POOL_STATS     = zfsIocFirst + 0x05 // 0x5a05
	ZFS_IOC_OBJSET_STATS   = zfsIocFirst + 0x12 // 0x5a12
	ZFS_IOC_CREATE         = zfsIocFirst + 0x17 // 0x5a17
	ZFS_IOC_DESTROY        = zfsIocFirst + 0x18 // 0x5a18
	ZFS_IOC_SNAPSHOT       = zfsIocFirst + 0x23 // 0x5a23
	ZFS_IOC_POOL_GET_PROPS = zfsIocFirst + 0x27 // 0x5a27
)

// ZFS path-length constants from the 2.2.2 headers (Linux).
const (
	maxPathLen           = 4096 // MAXPATHLEN
	maxNameLen           = 256  // MAXNAMELEN
	zfsMaxDatasetNameLen = 256  // ZFS_MAX_DATASET_NAME_LEN
)

// dmu_objset_type_t values (sys/fs/zfs.h, enum dmu_objset_type).
const (
	DMU_OST_NONE  = 0
	DMU_OST_META  = 1
	DMU_OST_ZFS   = 2 // filesystem
	DMU_OST_ZVOL  = 3 // volume
	DMU_OST_OTHER = 4
	DMU_OST_ANY   = 5
)

// sizeofZfsCmd is sizeof(zfs_cmd_t) for OpenZFS 2.2.2 on a 64-bit kernel.
// Confirmed by compiling the exact struct (from the 2.2.2 source) in the
// target guest: sizeof == 13744, with the field offsets recorded in
// abi_offsets below. zfsCmd (in cmd_linux.go) is sized to match exactly.
const sizeofZfsCmd = 13744

// Field byte offsets within zfs_cmd_t, captured from the C ABI probe against
// the 2.2.2 source headers in the guest. Used by cmd_linux.go to read/write
// the packed struct without relying on Go's struct layout for the large,
// alignment-sensitive trailing members.
const (
	offZcName            = 0
	offZcNvlistSrc       = 4096
	offZcNvlistSrcSize   = 4104
	offZcNvlistDst       = 4112
	offZcNvlistDstSize   = 4120
	offZcNvlistDstFilled = 4128
	offZcHistory         = 4136
	offZcValue           = 4144  // [MAXPATHLEN*2]
	offZcString          = 12336 // [MAXNAMELEN]
	offZcGuid            = 12592
	offZcNvlistConf      = 12600
	offZcNvlistConfSize  = 12608
	offZcCookie          = 12616
	offZcObjsetType      = 12624
	offZcObj             = 12656
	offZcIflags          = 12664
	offZcDeferDestroy    = 13648
	offZcFlags           = 13652
	offZcCleanupFd       = 13664
	offZcZoneid          = 13736
)
