// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Drives every branch of provision_linux.go and mount_linux.go through the
// ioctlFn / unixMount / unixUnmount seams, without /dev/zfs or root. The
// kernel paths themselves are exercised by TestIntegrationProvision.

func TestCreateFilesystemWithProps(t *testing.T) {
	defer snapshotSeams()()

	// The input nvlist is lzc_create's: int32 "type" plus nested "props".
	var sawProps bool
	ioctlFn = func(_ *Handle, req uintptr, cmd *zfsCmd) error {
		if req != ZFS_IOC_CREATE {
			t.Errorf("req = %#x, want ZFS_IOC_CREATE", req)
		}
		sawProps = cmd.getU64(offZcNvlistSrcSize) > 0
		return nil
	}
	props := Nvlist{"refquota": uint64(1 << 20), "fileshare:owner": "me", "mountpoint": ZFS_MOUNTPOINT_LEGACY}
	if err := okHandle().CreateFilesystemWithProps("tank/s", props); err != nil || !sawProps {
		t.Fatalf("with props: err=%v src=%v", err, sawProps)
	}
	// Empty props: still a valid create ("props" omitted).
	if err := okHandle().CreateFilesystemWithProps("tank/s", nil); err != nil {
		t.Fatalf("without props: %v", err)
	}

	// A rejected property comes back in the outnvl and is named.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{"refquota": int32(unix.EINVAL)})
		return unix.EINVAL
	}
	err := okHandle().CreateFilesystemWithProps("tank/s", Nvlist{"refquota": "x"})
	if !errors.Is(err, unix.EINVAL) || !strings.Contains(err.Error(), "refquota") {
		t.Fatalf("rejected property: %v", err)
	}

	// Plain ioctl error, no outnvl.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return unix.EEXIST }
	if err := okHandle().CreateFilesystemWithProps("tank/s", props); !errors.Is(err, unix.EEXIST) {
		t.Fatalf("plain error: %v", err)
	}
}

func TestQuotaHelpers(t *testing.T) {
	defer snapshotSeams()()

	sets := 0
	ioctlFn = func(_ *Handle, req uintptr, cmd *zfsCmd) error {
		if req == ZFS_IOC_SET_PROP {
			sets++
			return nil
		}
		putDst(t, cmd, Nvlist{
			"quota":    Nvlist{"value": uint64(7), "source": "tank/s"},
			"refquota": Nvlist{"value": uint64(0), "source": ""},
		})
		return nil
	}
	if err := okHandle().SetQuota("tank/s", 7); err != nil || sets != 1 {
		t.Fatalf("SetQuota: %v", err)
	}
	if err := okHandle().SetRefquota("tank/s", 0); err != nil {
		t.Fatalf("SetRefquota: %v", err)
	}
	if q, err := okHandle().Quota("tank/s"); err != nil || q != 7 {
		t.Fatalf("Quota = %d, %v", q, err)
	}
	if q, err := okHandle().Refquota("tank/s"); err != nil || q != 0 {
		t.Fatalf("Refquota = %d, %v", q, err)
	}

	// Wrong type / absent.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{"quota": Nvlist{"value": "none"}})
		return nil
	}
	if _, err := okHandle().Quota("tank/s"); err == nil {
		t.Fatal("want type error")
	}
	if _, err := okHandle().Refquota("tank/s"); err == nil {
		t.Fatal("want absent error")
	}

	// ioctl error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().Quota("tank/s"); !errors.Is(err, errInjected) {
		t.Fatalf("Quota error: %v", err)
	}
}

func TestUserPropHelpers(t *testing.T) {
	defer snapshotSeams()()

	// Invalid names never reach the kernel.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error {
		t.Error("ioctl issued for an invalid name")
		return nil
	}
	if err := okHandle().SetUserProp("tank/s", "quota", "1"); err == nil {
		t.Fatal("SetUserProp: want name error")
	}
	if _, _, err := okHandle().UserProp("tank/s", "owner"); err == nil {
		t.Fatal("UserProp: want name error")
	}

	ioctlFn = func(_ *Handle, req uintptr, cmd *zfsCmd) error {
		if req == ZFS_IOC_SET_PROP {
			return nil
		}
		putDst(t, cmd, Nvlist{
			"fileshare:owner": Nvlist{"value": "prov", "source": "tank"},
			"fileshare:bad":   Nvlist{"value": uint64(1), "source": "tank"},
			"fileshare:nosrc": Nvlist{"value": "x"},
		})
		return nil
	}
	if err := okHandle().SetUserProp("tank/s", "fileshare:owner", "prov"); err != nil {
		t.Fatalf("SetUserProp: %v", err)
	}
	v, src, err := okHandle().UserProp("tank/s", "fileshare:owner")
	if err != nil || v != "prov" || src != "tank" {
		t.Fatalf("UserProp = %q, %q, %v", v, src, err)
	}
	if v, src, err := okHandle().UserProp("tank/s", "fileshare:nosrc"); err != nil || v != "x" || src != "" {
		t.Fatalf("UserProp without source = %q, %q, %v", v, src, err)
	}
	if _, _, err := okHandle().UserProp("tank/s", "fileshare:bad"); err == nil {
		t.Fatal("want value-type error")
	}
	if _, _, err := okHandle().UserProp("tank/s", "fileshare:absent"); !errors.Is(err, ErrPropNotSet) {
		t.Fatalf("absent: %v", err)
	}

	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, _, err := okHandle().UserProp("tank/s", "fileshare:owner"); !errors.Is(err, errInjected) {
		t.Fatalf("UserProp ioctl error: %v", err)
	}
}

func TestMountUnmount(t *testing.T) {
	defer snapshotSeams()()

	if err := Mount("", "/mnt", 0, ""); err == nil {
		t.Fatal("want empty dataset error")
	}
	if err := Mount("tank/s", "", 0, ""); err == nil {
		t.Fatal("want empty mountpoint error")
	}
	if err := Unmount("", 0); err == nil {
		t.Fatal("want empty mountpoint error")
	}

	var got []string
	unixMount = func(src, dst, fstype string, flags uintptr, data string) error {
		got = []string{src, dst, fstype, data}
		if flags != unix.MS_NOSUID {
			t.Errorf("flags = %#x", flags)
		}
		return nil
	}
	unixUnmount = func(dst string, flags int) error {
		if dst != "/mnt" || flags != unix.MNT_DETACH {
			t.Errorf("umount2(%q, %#x)", dst, flags)
		}
		return nil
	}
	if err := Mount("tank/s", "/mnt", unix.MS_NOSUID, "noatime"); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if strings.Join(got, "|") != "tank/s|/mnt|zfs|noatime" {
		t.Errorf("mount(2) args = %v", got)
	}
	if err := Unmount("/mnt", unix.MNT_DETACH); err != nil {
		t.Fatalf("Unmount: %v", err)
	}

	unixMount = func(string, string, string, uintptr, string) error { return unix.EPERM }
	unixUnmount = func(string, int) error { return unix.EBUSY }
	if err := Mount("tank/s", "/mnt", 0, ""); !errors.Is(err, unix.EPERM) {
		t.Fatalf("Mount error: %v", err)
	}
	if err := Unmount("/mnt", 0); !errors.Is(err, unix.EBUSY) {
		t.Fatalf("Unmount error: %v", err)
	}
}
