// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"fmt"
)

// Mount mounts the ZFS filesystem `dataset` (e.g. "tank/shares/a") on the
// existing directory `mountpoint` with mount(2) and filesystem type "zfs" —
// what libzfs's do_mount (lib/libzfs/os/linux/libzfs_mount_os.c) does when
// ZFS_MOUNT_HELPER is unset: mount(src, mntpt, MNTTYPE_ZFS, mntflags, opts).
// `flags` are the MS_* mount flags (unix.MS_RDONLY, unix.MS_NOSUID,
// unix.MS_NODEV, unix.MS_NOEXEC, ...); `data` is the comma-separated option
// string the zpl parses (zpl_super.c: e.g. "noatime", "noxattr"; "" for
// none). It does not create `mountpoint`.
//
// The "zfsutil" option need not be passed: zpl_super.c accepts it and ignores
// it. The rule that a non-legacy dataset can only be mounted by zfs(8) lives
// in the mount.zfs helper (cmd/mount_zfs.c), not in the kernel, so mount(2)
// mounts a dataset whatever its mountpoint property says.
//
// To keep a dataset mounted ONLY where the caller put it, create it with
// "mountpoint": ZFS_MOUNTPOINT_LEGACY (or "canmount":
// uint64(ZFS_CANMOUNT_NOAUTO)). Neither the kernel nor ZFS_IOC_CREATE ever
// mounts a dataset; automatic mounts come from userspace. zfs(8) create mounts
// when canmount=on (zfs_mount_and_share, cmd/zfs/zfs_main.c). `zpool import`
// mounts through zpool_enable_datasets, whose zfs_iter_cb skips
// canmount=noauto and whose zfs_is_mountable refuses a legacy or none
// mountpoint (lib/libzfs/libzfs_mount.c); man7/zfsprops.7 states the same
// for `zfs mount -a`, which zfs-mount.service runs at boot
// (etc/systemd/system/zfs-mount.service.in). A plain path mountpoint would
// have those tools mount the dataset a second time, at that path.
func Mount(dataset, mountpoint string, flags uintptr, data string) error {
	if dataset == "" || mountpoint == "" {
		return fmt.Errorf("Mount: empty dataset (%q) or mountpoint (%q)", dataset, mountpoint)
	}
	if err := unixMount(dataset, mountpoint, "zfs", flags, data); err != nil {
		return fmt.Errorf("mount %q on %q: %w", dataset, mountpoint, err)
	}
	return nil
}

// Unmount unmounts the filesystem mounted on `mountpoint` with umount2(2),
// as libzfs's do_unmount does. `flags` are umount2 flags (unix.MNT_FORCE,
// unix.MNT_DETACH, ...); a busy mount fails with EBUSY unless forced.
func Unmount(mountpoint string, flags int) error {
	if mountpoint == "" {
		return fmt.Errorf("Unmount: empty mountpoint")
	}
	if err := unixUnmount(mountpoint, flags); err != nil {
		return fmt.Errorf("umount %q: %w", mountpoint, err)
	}
	return nil
}
