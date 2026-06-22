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

	ZFS_IOC_POOL_CREATE       = zfsIocFirst + 0x00 // 0x5a00
	ZFS_IOC_POOL_DESTROY      = zfsIocFirst + 0x01 // 0x5a01
	ZFS_IOC_POOL_IMPORT       = zfsIocFirst + 0x02 // 0x5a02
	ZFS_IOC_POOL_EXPORT       = zfsIocFirst + 0x03 // 0x5a03
	ZFS_IOC_POOL_CONFIGS      = zfsIocFirst + 0x04 // 0x5a04
	ZFS_IOC_POOL_STATS        = zfsIocFirst + 0x05 // 0x5a05
	ZFS_IOC_POOL_TRYIMPORT    = zfsIocFirst + 0x06 // 0x5a06
	ZFS_IOC_POOL_SCAN         = zfsIocFirst + 0x07 // 0x5a07 (scrub/resilver)
	ZFS_IOC_VDEV_SET_STATE    = zfsIocFirst + 0x0d // 0x5a0d (online/offline)
	ZFS_IOC_VDEV_ATTACH       = zfsIocFirst + 0x0e // 0x5a0e (attach/replace)
	ZFS_IOC_VDEV_DETACH       = zfsIocFirst + 0x0f // 0x5a0f
	ZFS_IOC_OBJSET_STATS      = zfsIocFirst + 0x12 // 0x5a12
	ZFS_IOC_DATASET_LIST_NEXT = zfsIocFirst + 0x14 // 0x5a14
	ZFS_IOC_SET_PROP          = zfsIocFirst + 0x16 // 0x5a16
	ZFS_IOC_OBJ_TO_PATH       = zfsIocFirst + 0x25 // 0x5a25 (diff: obj -> path)
	ZFS_IOC_OBJ_TO_STATS      = zfsIocFirst + 0x38 // 0x5a38 (diff: obj -> zfs_stat_t)
	ZFS_IOC_NEXT_OBJ          = zfsIocFirst + 0x35 // 0x5a35 (diff: next allocated obj)
	ZFS_IOC_USERSPACE_MANY    = zfsIocFirst + 0x2e // 0x5a2e (user/group/project space)
	ZFS_IOC_USERSPACE_UPGRADE = zfsIocFirst + 0x2f // 0x5a2f
	ZFS_IOC_DIFF              = zfsIocFirst + 0x36 // 0x5a36 (zfs diff over a pipe fd)
	ZFS_IOC_CHANNEL_PROGRAM   = zfsIocFirst + 0x48 // 0x5a48 (lzc_channel_program)
	ZFS_IOC_CREATE            = zfsIocFirst + 0x17 // 0x5a17
	ZFS_IOC_DESTROY           = zfsIocFirst + 0x18 // 0x5a18
	ZFS_IOC_ROLLBACK          = zfsIocFirst + 0x19 // 0x5a19 (lzc_rollback)
	ZFS_IOC_RENAME            = zfsIocFirst + 0x1a // 0x5a1a
	ZFS_IOC_RECV              = zfsIocFirst + 0x1b // 0x5a1b (legacy)
	ZFS_IOC_SEND              = zfsIocFirst + 0x1c // 0x5a1c (legacy)
	ZFS_IOC_PROMOTE           = zfsIocFirst + 0x22 // 0x5a22 (legacy)
	ZFS_IOC_SNAPSHOT          = zfsIocFirst + 0x23 // 0x5a23
	ZFS_IOC_POOL_GET_PROPS    = zfsIocFirst + 0x27 // 0x5a27
	ZFS_IOC_INHERIT_PROP      = zfsIocFirst + 0x2b // 0x5a2b (legacy)
	ZFS_IOC_HOLD              = zfsIocFirst + 0x30 // 0x5a30 (lzc_hold)
	ZFS_IOC_RELEASE           = zfsIocFirst + 0x31 // 0x5a31 (lzc_release)
	ZFS_IOC_GET_HOLDS         = zfsIocFirst + 0x32 // 0x5a32 (lzc_get_holds)
	ZFS_IOC_SEND_NEW          = zfsIocFirst + 0x40 // 0x5a40 (lzc_send)
	ZFS_IOC_SEND_SPACE        = zfsIocFirst + 0x41 // 0x5a41
	ZFS_IOC_CLONE             = zfsIocFirst + 0x42 // 0x5a42 (lzc_clone)
	ZFS_IOC_BOOKMARK          = zfsIocFirst + 0x43 // 0x5a43 (lzc_bookmark)
	ZFS_IOC_GET_BOOKMARKS     = zfsIocFirst + 0x44 // 0x5a44 (lzc_get_bookmarks)
	ZFS_IOC_DESTROY_BOOKMARKS = zfsIocFirst + 0x45 // 0x5a45 (lzc_destroy_bookmarks)
	ZFS_IOC_RECV_NEW          = zfsIocFirst + 0x46 // 0x5a46 (lzc_receive)
	ZFS_IOC_LOAD_KEY          = zfsIocFirst + 0x49 // 0x5a49 (lzc_load_key)
	ZFS_IOC_UNLOAD_KEY        = zfsIocFirst + 0x4a // 0x5a4a (lzc_unload_key)
	ZFS_IOC_CHANGE_KEY        = zfsIocFirst + 0x4b // 0x5a4b (lzc_change_key)
	ZFS_IOC_POOL_REOPEN       = zfsIocFirst + 0x3d // 0x5a3d (lzc_reopen)
	ZFS_IOC_POOL_INITIALIZE   = zfsIocFirst + 0x4f // 0x5a4f (lzc_initialize)
	ZFS_IOC_POOL_TRIM         = zfsIocFirst + 0x50 // 0x5a50 (lzc_trim)
)

// zc_simple is a uint8 field (offset 13668 in the 2.2.2 zfs_cmd_t). It is the
// "rebuild" flag read by zfs_ioc_vdev_attach (sequential resilver). We default
// it to 0 (normal healing resilver). Confirmed via offsetof on the guest.
const offZcSimple = 13668

// pool_scan_func_t (sys/fs/zfs.h) — written into zc_cookie for
// ZFS_IOC_POOL_SCAN. POOL_SCAN_NONE cancels an in-progress scan.
const (
	POOL_SCAN_NONE     = 0
	POOL_SCAN_SCRUB    = 1
	POOL_SCAN_RESILVER = 2
)

// pool_scrub_cmd_t (sys/fs/zfs.h) — written into zc_flags for
// ZFS_IOC_POOL_SCAN. NORMAL begins/resumes; PAUSE pauses an active scrub.
const (
	POOL_SCRUB_NORMAL = 0
	POOL_SCRUB_PAUSE  = 1
)

// dsl_scan_state_t (sys/fs/zfs.h) — the pss_state field of pool_scan_stat_t.
const (
	DSS_NONE     = 0
	DSS_SCANNING = 1
	DSS_FINISHED = 2
	DSS_CANCELED = 3
)

// pool_initialize_func_t (sys/fs/zfs.h) — the ZPOOL_INITIALIZE_COMMAND value.
const (
	POOL_INITIALIZE_START   = 0
	POOL_INITIALIZE_CANCEL  = 1
	POOL_INITIALIZE_SUSPEND = 2
	POOL_INITIALIZE_UNINIT  = 3
)

// pool_trim_func_t (sys/fs/zfs.h) — the ZPOOL_TRIM_COMMAND value.
const (
	POOL_TRIM_START   = 0
	POOL_TRIM_CANCEL  = 1
	POOL_TRIM_SUSPEND = 2
)

// vdev_state_t (sys/fs/zfs.h) — written into zc_cookie for
// ZFS_IOC_VDEV_SET_STATE to request a new state. VDEV_STATE_HEALTHY is the
// "online" request value (VDEV_STATE_ONLINE is an alias for it).
const (
	VDEV_STATE_UNKNOWN   = 0
	VDEV_STATE_CLOSED    = 1
	VDEV_STATE_OFFLINE   = 2
	VDEV_STATE_REMOVED   = 3
	VDEV_STATE_CANT_OPEN = 4
	VDEV_STATE_FAULTED   = 5
	VDEV_STATE_DEGRADED  = 6
	VDEV_STATE_HEALTHY   = 7
	VDEV_STATE_ONLINE    = VDEV_STATE_HEALTHY
)

// innvl key names for the new-style pool ops (sys/fs/zfs.h).
const (
	ZPOOL_INITIALIZE_COMMAND = "initialize_command"
	ZPOOL_INITIALIZE_VDEVS   = "initialize_vdevs"

	ZPOOL_TRIM_COMMAND = "trim_command"
	ZPOOL_TRIM_VDEVS   = "trim_vdevs"
	ZPOOL_TRIM_RATE    = "trim_rate"
	ZPOOL_TRIM_SECURE  = "trim_secure"
)

// ZPOOL_CONFIG_SCAN_STATS is the vdev_tree-root nvlist key under which the
// kernel reports the pool_scan_stat_t as a uint64 array (read via
// nvlist_lookup_uint64_array). It is populated on the config returned by
// ZFS_IOC_POOL_CONFIGS / ZFS_IOC_POOL_STATS. ZPOOL_CONFIG_VDEV_STATS is the
// per-vdev vdev_stat_t (also a uint64 array). Verified against the guest's
// sys/fs/zfs.h.
const (
	ZPOOL_CONFIG_SCAN_STATS = "scan_stats"
	ZPOOL_CONFIG_VDEV_STATS = "vdev_stats"
)

// ZPOOL_HIDDEN_ARGS is the nested-nvlist key (in the regular zc_nvlist_src
// input nvlist) under which sensitive arguments — notably the wrapping key
// material "wkeydata" — are carried so the kernel can strip them before the
// ioctl is logged to the pool history. Verified against the 2.2.2 headers in
// the target guest (include/sys/fs/zfs.h) and the libzfs_core lzc_load_key /
// lzc_create / lzc_change_key sources.
const ZPOOL_HIDDEN_ARGS = "hidden_args"

// dcp_cmd_t values for ZFS_IOC_CHANGE_KEY's "crypt_cmd" field (sys/dsl_crypt.h
// in the target guest). Only the ones we exercise are named.
const (
	DCP_CMD_NONE          = 0 // no specific command
	DCP_CMD_RAW_RECV      = 1 // raw receive
	DCP_CMD_NEW_KEY       = 2 // rewrap key as an encryption root
	DCP_CMD_INHERIT       = 3 // rewrap key with parent's wrapping key
	DCP_CMD_FORCE_NEW_KEY = 4 // change to encryption root without rewrap
	DCP_CMD_FORCE_INHERIT = 5 // inherit parent's key without rewrap
)

// Encryption-property NUMERIC values. Unlike ordinary dataset properties (whose
// string values the kernel's property layer parses for us), the crypto create /
// change-key path reads "encryption" and "keyformat" out of the props nvlist
// directly with nvlist_lookup_uint64 (see dsl_crypto_params_create_nvlist), so
// they MUST be supplied as uint64 enum values, not strings. "keylocation" by
// contrast is read as a string. Values are the enum positions from
// include/sys/fs/zfs.h in the target guest.
//
// enum zio_encrypt — values for the "encryption" property.
const (
	ZIO_CRYPT_INHERIT     = 0
	ZIO_CRYPT_ON          = 1
	ZIO_CRYPT_OFF         = 2
	ZIO_CRYPT_AES_128_CCM = 3
	ZIO_CRYPT_AES_192_CCM = 4
	ZIO_CRYPT_AES_256_CCM = 5
	ZIO_CRYPT_AES_128_GCM = 6
	ZIO_CRYPT_AES_192_GCM = 7
	ZIO_CRYPT_AES_256_GCM = 8 // the value `zfs` prints as "aes-256-gcm"
)

// zfs_keyformat_t — values for the "keyformat" property.
const (
	ZFS_KEYFORMAT_NONE       = 0
	ZFS_KEYFORMAT_RAW        = 1 // a raw WRAPPING_KEY_LEN-byte key
	ZFS_KEYFORMAT_HEX        = 2
	ZFS_KEYFORMAT_PASSPHRASE = 3
)

// WRAPPING_KEY_LEN is the exact wkeydata length the kernel requires for
// keyformat=raw / hex (and the length of a PBKDF2-derived passphrase key).
// The crypto ioctls reject any other length with EINVAL.
const WRAPPING_KEY_LEN = 32

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

// Channel-program (ZFS_IOC_CHANNEL_PROGRAM) innvl/outnvl keys and limits,
// verified against the guest's include/sys/fs/zfs.h (ZCP_ARG_* / ZCP_RET_* /
// ZCP_DEFAULT_* macros) and the libzfs_core lzc_channel_program_impl(), which
// builds the innvl as { program(string), arg(any), sync(bool), instrlimit
// (uint64), memlimit(uint64) } and reads the result from outnvl["return"] (or
// outnvl["error"] on a runtime error). The kernel's zfs_keys_channel_program
// marks "program" and "arg" required, the rest optional.
const (
	ZCP_ARG_PROGRAM    = "program"
	ZCP_ARG_ARGLIST    = "arg"
	ZCP_ARG_SYNC       = "sync"
	ZCP_ARG_INSTRLIMIT = "instrlimit"
	ZCP_ARG_MEMLIMIT   = "memlimit"

	ZCP_RET_RETURN = "return"
	ZCP_RET_ERROR  = "error"

	// ZCP_DEFAULT_INSTRLIMIT / ZCP_DEFAULT_MEMLIMIT match the kernel macros
	// (10,000,000 Lua instructions / 10 MiB). Used when the caller passes 0.
	ZCP_DEFAULT_INSTRLIMIT = 10 * 1000 * 1000
	ZCP_DEFAULT_MEMLIMIT   = 10 * 1024 * 1024
)

// zfs_userquota_prop_t (sys/fs/zfs.h) — the property selector written into
// zc_objset_type for ZFS_IOC_USERSPACE_MANY. The enum order is exactly as in
// the guest header.
const (
	ZFS_PROP_USERUSED        = 0
	ZFS_PROP_USERQUOTA       = 1
	ZFS_PROP_GROUPUSED       = 2
	ZFS_PROP_GROUPQUOTA      = 3
	ZFS_PROP_USEROBJUSED     = 4
	ZFS_PROP_USEROBJQUOTA    = 5
	ZFS_PROP_GROUPOBJUSED    = 6
	ZFS_PROP_GROUPOBJQUOTA   = 7
	ZFS_PROP_PROJECTUSED     = 8
	ZFS_PROP_PROJECTQUOTA    = 9
	ZFS_PROP_PROJECTOBJUSED  = 10
	ZFS_PROP_PROJECTOBJQUOTA = 11
)

// zfs_useracct_t layout (include/sys/zfs_ioctl.h), confirmed against the 2.2.2
// source on the guest: { char zu_domain[256]; uint32 zu_rid; uint32 zu_pad;
// uint64 zu_space; } == 272 bytes. ZFS_IOC_USERSPACE_MANY writes a packed
// array of these into zc_nvlist_dst (a raw struct buffer, NOT an nvlist), with
// zc_nvlist_dst_size = bytes filled and zc_cookie carrying the resumable ZAP
// cursor across calls.
const (
	sizeofUseracct  = 272
	offZuDomain     = 0   // char[256]
	offZuRid        = 256 // uint32 (uid_t)
	offZuSpace      = 264 // uint64 (8-aligned after rid+pad)
	useracctDomainN = 256
)

// dmu_diff_record_t layout (include/sys/zfs_ioctl.h): three uint64s
// { ddr_type, ddr_first, ddr_last } == 24 bytes. ZFS_IOC_DIFF streams these
// over the write fd in zc_cookie (zc_name=fromsnap, zc_value=tosnap). ddr_type
// distinguishes a range of FREE'd objects from a range of in-use (DATA)
// objects; userland resolves each object to a path and stat to classify the
// change, exactly as libzfs's zfs_show_diffs does.
const (
	sizeofDiffRecord = 24
	offDdrType       = 0  // uint64
	offDdrFirst      = 8  // uint64
	offDdrLast       = 16 // uint64

	// dmu_diff_record ddr_type values — bit flags from include/sys/zfs_ioctl.h
	// (enum: DDR_NONE 0x1, DDR_INUSE 0x2, DDR_FREE 0x4). The kernel only ever
	// writes DDR_INUSE or DDR_FREE records to the stream.
	DDR_NONE  = 0x1
	DDR_INUSE = 0x2
	DDR_FREE  = 0x4
)

// zfs_stat_t layout (include/sys/zfs_stat.h): { uint64 zs_gen; uint64 zs_mode;
// uint64 zs_links; uint64 zs_ctime[2]; } == 40 bytes, returned in the zfs_cmd_t
// zc_stat field by ZFS_IOC_OBJ_TO_STATS (with the path in zc_value). zc_stat
// sits immediately before zc_zoneid (offset 13736); 13736-40 == 13696, which is
// 8-aligned. Verified against the 2.2.2 zfs_cmd_t in the guest source.
const (
	offZcStat   = 13696
	offZsGen    = offZcStat + 0  // uint64 generation (txg the object was created)
	offZsMode   = offZcStat + 8  // uint64 mode (includes S_IF* type bits)
	offZsLinks  = offZcStat + 16 // uint64 link count
	offZsCtime0 = offZcStat + 24 // uint64 ctime seconds
	offZsCtime1 = offZcStat + 32 // uint64 ctime nanoseconds
)

// The metadata-object number boundary: the kernel diff stream reports object
// numbers; the first user object is ZDIFF_OBJECT_MIN. Objects below it are ZPL
// metadata and are not reported as path changes (matching libzfs).
const ZDIFF_OBJECT_MIN = 16
