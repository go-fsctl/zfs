// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import "testing"

// TestZfsCmdSizeAndOffsets pins the zfs_cmd_t ABI for OpenZFS 2.2.2 (64-bit),
// matching the values produced by compiling the exact struct (from the 2.2.2
// source) in the target guest:
//
//	sizeof_zfs_cmd_t 13744
//	off_zc_name 0
//	off_zc_nvlist_src 4096 / _size 4104
//	off_zc_nvlist_dst 4112 / _size 4120 / _filled 4128
//	off_zc_history 4136
//	off_zc_value 4144  (char[MAXPATHLEN*2])
//	off_zc_string 12336
//	off_zc_guid 12592
//	off_zc_nvlist_conf 12600 / _size 12608
//	off_zc_cookie 12616
//	off_zc_objset_type 12624
//	off_zc_obj 12656
//	off_zc_iflags 12664
//	off_zc_defer_destroy 13648 / off_zc_flags 13652
//	off_zc_cleanup_fd 13664
//	off_zc_zoneid 13736
func TestZfsCmdSizeAndOffsets(t *testing.T) {
	if sizeofZfsCmd != 13744 {
		t.Fatalf("sizeofZfsCmd = %d, want 13744", sizeofZfsCmd)
	}
	type oc struct {
		name string
		got  int
		want int
	}
	for _, c := range []oc{
		{"zc_name", offZcName, 0},
		{"zc_nvlist_src", offZcNvlistSrc, 4096},
		{"zc_nvlist_src_size", offZcNvlistSrcSize, 4104},
		{"zc_nvlist_dst", offZcNvlistDst, 4112},
		{"zc_nvlist_dst_size", offZcNvlistDstSize, 4120},
		{"zc_nvlist_dst_filled", offZcNvlistDstFilled, 4128},
		{"zc_history", offZcHistory, 4136},
		{"zc_value", offZcValue, 4144},
		{"zc_string", offZcString, 12336},
		{"zc_guid", offZcGuid, 12592},
		{"zc_nvlist_conf", offZcNvlistConf, 12600},
		{"zc_nvlist_conf_size", offZcNvlistConfSize, 12608},
		{"zc_cookie", offZcCookie, 12616},
		{"zc_objset_type", offZcObjsetType, 12624},
		{"zc_obj", offZcObj, 12656},
		{"zc_iflags", offZcIflags, 12664},
		{"zc_defer_destroy", offZcDeferDestroy, 13648},
		{"zc_flags", offZcFlags, 13652},
		{"zc_cleanup_fd", offZcCleanupFd, 13664},
		{"zc_zoneid", offZcZoneid, 13736},
	} {
		if c.got != c.want {
			t.Errorf("offset %s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

// TestIocNumbers pins the ZFS_IOC_* request numbers (Linux uses the raw enum
// value as the ioctl cmd; base ZFS_IOC_FIRST = 'Z'<<8 = 0x5a00).
func TestIocNumbers(t *testing.T) {
	for _, c := range []struct {
		name string
		got  int
		want int
	}{
		{"POOL_CREATE", ZFS_IOC_POOL_CREATE, 0x5a00},
		{"POOL_DESTROY", ZFS_IOC_POOL_DESTROY, 0x5a01},
		{"POOL_IMPORT", ZFS_IOC_POOL_IMPORT, 0x5a02},
		{"POOL_EXPORT", ZFS_IOC_POOL_EXPORT, 0x5a03},
		{"POOL_CONFIGS", ZFS_IOC_POOL_CONFIGS, 0x5a04},
		{"POOL_STATS", ZFS_IOC_POOL_STATS, 0x5a05},
		{"POOL_TRYIMPORT", ZFS_IOC_POOL_TRYIMPORT, 0x5a06},
		{"OBJSET_STATS", ZFS_IOC_OBJSET_STATS, 0x5a12},
		{"SET_PROP", ZFS_IOC_SET_PROP, 0x5a16},
		{"CREATE", ZFS_IOC_CREATE, 0x5a17},
		{"DESTROY", ZFS_IOC_DESTROY, 0x5a18},
		{"RENAME", ZFS_IOC_RENAME, 0x5a1a},
		{"RECV", ZFS_IOC_RECV, 0x5a1b},
		{"SEND", ZFS_IOC_SEND, 0x5a1c},
		{"ROLLBACK", ZFS_IOC_ROLLBACK, 0x5a19},
		{"DATASET_LIST_NEXT", ZFS_IOC_DATASET_LIST_NEXT, 0x5a14},
		{"SNAPSHOT", ZFS_IOC_SNAPSHOT, 0x5a23},
		{"POOL_GET_PROPS", ZFS_IOC_POOL_GET_PROPS, 0x5a27},
		{"HOLD", ZFS_IOC_HOLD, 0x5a30},
		{"RELEASE", ZFS_IOC_RELEASE, 0x5a31},
		{"GET_HOLDS", ZFS_IOC_GET_HOLDS, 0x5a32},
		{"SEND_NEW", ZFS_IOC_SEND_NEW, 0x5a40},
		{"SEND_SPACE", ZFS_IOC_SEND_SPACE, 0x5a41},
		{"CLONE", ZFS_IOC_CLONE, 0x5a42},
		{"BOOKMARK", ZFS_IOC_BOOKMARK, 0x5a43},
		{"GET_BOOKMARKS", ZFS_IOC_GET_BOOKMARKS, 0x5a44},
		{"DESTROY_BOOKMARKS", ZFS_IOC_DESTROY_BOOKMARKS, 0x5a45},
		{"RECV_NEW", ZFS_IOC_RECV_NEW, 0x5a46},
	} {
		if c.got != c.want {
			t.Errorf("%s = %#x, want %#x", c.name, c.got, c.want)
		}
	}
}

// TestDmuReplayRecordLayout pins the dmu_replay_record_t / drr_begin layout for
// OpenZFS 2.2.2, matching values produced by compiling the exact structs
// (include/sys/zfs_ioctl.h) in the target guest:
//
//	sizeof(dmu_replay_record_t)  312
//	offsetof(.., drr_payloadlen) 4
//	offsetof(.., drr_u)          8   (== drr_begin.drr_magic)
//	drr_begin.drr_toguid         40
//	drr_begin.drr_toname         56
//	DMU_BACKUP_MAGIC             0x2f5bacbac
func TestDmuReplayRecordLayout(t *testing.T) {
	for _, c := range []struct {
		name string
		got  int
		want int
	}{
		{"sizeofDmuReplayRecord", sizeofDmuReplayRecord, 312},
		{"offDrrType", offDrrType, 0},
		{"offDrrPayloadLen", offDrrPayloadLen, 4},
		{"offDrrBeginMagic", offDrrBeginMagic, 8},
		{"offDrrBeginVerInf", offDrrBeginVerInf, 16},
		{"offDrrBeginCtime", offDrrBeginCtime, 24},
		{"offDrrBeginType", offDrrBeginType, 32},
		{"offDrrBeginFlags", offDrrBeginFlags, 36},
		{"offDrrBeginToGuid", offDrrBeginToGuid, 40},
		{"offDrrBeginFromG", offDrrBeginFromG, 48},
		{"offDrrBeginToName", offDrrBeginToName, 56},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if dmuBackupMagic != 0x2f5bacbac {
		t.Errorf("dmuBackupMagic = %#x, want 0x2f5bacbac", dmuBackupMagic)
	}
}
