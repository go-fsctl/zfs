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
	ZFS_IOC_POOL_EXPORT    = zfsIocFirst + 0x03 // 0x5a03
	ZFS_IOC_POOL_CONFIGS   = zfsIocFirst + 0x04 // 0x5a04
	ZFS_IOC_POOL_STATS     = zfsIocFirst + 0x05 // 0x5a05
	ZFS_IOC_POOL_TRYIMPORT = zfsIocFirst + 0x06 // 0x5a06
	ZFS_IOC_OBJSET_STATS   = zfsIocFirst + 0x12 // 0x5a12
	ZFS_IOC_SET_PROP       = zfsIocFirst + 0x16 // 0x5a16
	ZFS_IOC_CREATE         = zfsIocFirst + 0x17 // 0x5a17
	ZFS_IOC_DESTROY        = zfsIocFirst + 0x18 // 0x5a18
	ZFS_IOC_RENAME         = zfsIocFirst + 0x1a // 0x5a1a
	ZFS_IOC_RECV           = zfsIocFirst + 0x1b // 0x5a1b (legacy)
	ZFS_IOC_SEND           = zfsIocFirst + 0x1c // 0x5a1c (legacy)
	ZFS_IOC_SNAPSHOT       = zfsIocFirst + 0x23 // 0x5a23
	ZFS_IOC_POOL_GET_PROPS = zfsIocFirst + 0x27 // 0x5a27
	ZFS_IOC_SEND_NEW       = zfsIocFirst + 0x40 // 0x5a40 (lzc_send)
	ZFS_IOC_SEND_SPACE     = zfsIocFirst + 0x41 // 0x5a41
	ZFS_IOC_RECV_NEW       = zfsIocFirst + 0x46 // 0x5a46 (lzc_receive)
)

// ZPOOL_CONFIG_* and VDEV_TYPE_* string keys used to build the pool
// configuration / vdev-tree nvlist passed to ZFS_IOC_POOL_CREATE and
// returned by the import/config ioctls. Names verified against the 2.2.2
// headers (include/sys/fs/zfs.h) in the target guest.
const (
	ZPOOL_CONFIG_VERSION    = "version"
	ZPOOL_CONFIG_POOL_NAME  = "name"
	ZPOOL_CONFIG_POOL_GUID  = "pool_guid"
	ZPOOL_CONFIG_VDEV_TREE  = "vdev_tree"
	ZPOOL_CONFIG_TYPE       = "type"
	ZPOOL_CONFIG_CHILDREN   = "children"
	ZPOOL_CONFIG_GUID       = "guid"
	ZPOOL_CONFIG_PATH       = "path"
	ZPOOL_CONFIG_ASHIFT     = "ashift"
	ZPOOL_CONFIG_WHOLE_DISK = "whole_disk"
	ZPOOL_CONFIG_IS_LOG     = "is_log"

	VDEV_TYPE_ROOT   = "root"
	VDEV_TYPE_MIRROR = "mirror"
	VDEV_TYPE_RAIDZ  = "raidz"
	VDEV_TYPE_DISK   = "disk"
	VDEV_TYPE_FILE   = "file"
)

// SPA_VERSION is the current on-disk SPA version (SPA_VERSION_5000) for
// OpenZFS 2.2.x; feature-flagged pools advertise this version number.
const SPA_VERSION = 5000

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

// DMU send/receive stream constants and the dmu_replay_record_t layout, all
// confirmed by compiling the exact OpenZFS 2.2.2 structs (include/sys/
// zfs_ioctl.h) against the headers in the target guest:
//
//	sizeof(dmu_replay_record_t)  == 312
//	sizeof(struct drr_begin)     == 304
//	offsetof(.., drr_payloadlen) == 4
//	offsetof(.., drr_u)          == 8
//	drr_begin.drr_magic          @ 8
//	drr_begin.drr_toguid         @ 40
//	drr_begin.drr_toname         @ 56  (char[MAXNAMELEN])
//	DMU_BACKUP_MAGIC             == 0x2f5bacbac
//
// The receive ioctl (ZFS_IOC_RECV_NEW) takes the leading dmu_replay_record_t
// of the stream — a DRR_BEGIN record — verbatim as a byte_array nvpair, so we
// only need to parse enough of it to validate the magic/type and report the
// target name/guid; the kernel consumes the rest of the stream from the fd.
const (
	// sizeofDmuReplayRecord is sizeof(dmu_replay_record_t).
	sizeofDmuReplayRecord = 312

	// dmuBackupMagic is the DRR_BEGIN drr_magic value (host-endian on the
	// wire for a native stream). A foreign-endian stream carries the
	// byte-swapped magic; we detect and report that rather than guess.
	dmuBackupMagic = 0x2f5bacbac

	// drrTypeBegin is DRR_BEGIN (the first value of dmu_replay_record_type).
	drrTypeBegin = 0

	// dmu_replay_record_t field offsets (within the 312-byte record).
	offDrrType        = 0  // uint32 (enum)
	offDrrPayloadLen  = 4  // uint32
	offDrrBeginMagic  = 8  // uint64 drr_begin.drr_magic
	offDrrBeginVerInf = 16 // uint64 drr_begin.drr_versioninfo
	offDrrBeginCtime  = 24 // uint64 drr_begin.drr_creation_time
	offDrrBeginType   = 32 // uint32 drr_begin.drr_type (dmu_objset_type_t)
	offDrrBeginFlags  = 36 // uint32 drr_begin.drr_flags
	offDrrBeginToGuid = 40 // uint64 drr_begin.drr_toguid
	offDrrBeginFromG  = 48 // uint64 drr_begin.drr_fromguid
	offDrrBeginToName = 56 // char[MAXNAMELEN] drr_begin.drr_toname
)
