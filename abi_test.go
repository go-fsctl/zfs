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
		{"POOL_CONFIGS", ZFS_IOC_POOL_CONFIGS, 0x5a04},
		{"POOL_STATS", ZFS_IOC_POOL_STATS, 0x5a05},
		{"OBJSET_STATS", ZFS_IOC_OBJSET_STATS, 0x5a12},
		{"CREATE", ZFS_IOC_CREATE, 0x5a17},
		{"DESTROY", ZFS_IOC_DESTROY, 0x5a18},
		{"SNAPSHOT", ZFS_IOC_SNAPSHOT, 0x5a23},
	} {
		if c.got != c.want {
			t.Errorf("%s = %#x, want %#x", c.name, c.got, c.want)
		}
	}
}
