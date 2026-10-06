// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import (
	"errors"
	"fmt"
)

// canmount values (sys/fs/zfs.h, enum zfs_canmount_type). canmount is an INDEX
// property, so it MUST travel as a uint64: zfs_set_prop_nvlist
// (module/zfs/zfs_ioctl.c) rejects a DATA_TYPE_STRING for any property whose
// type is not PROP_TYPE_STRING with EINVAL, so {"canmount": "noauto"} fails
// where {"canmount": uint64(ZFS_CANMOUNT_NOAUTO)} succeeds.
const (
	ZFS_CANMOUNT_OFF    = 0
	ZFS_CANMOUNT_ON     = 1
	ZFS_CANMOUNT_NOAUTO = 2
)

// ZFS_MOUNTPOINT_LEGACY is the mountpoint value (a STRING property) that hands
// mounting over to mount(2)/fstab: libzfs's zfs_is_mountable
// (lib/libzfs/libzfs_mount.c) treats a "legacy" or "none" mountpoint as not
// mountable, so `zfs mount -a` and `zpool import` leave the dataset alone.
const ZFS_MOUNTPOINT_LEGACY = "legacy"

// PropSourceReceived is the "source" the kernel reports for a property that
// came in a send stream (ZPROP_SOURCE_VAL_RECVD in sys/fs/zfs.h).
const PropSourceReceived = "$recvd"

// ErrPropNotSet is returned (wrapped) by UserProp when the property is set
// neither on the dataset nor on any ancestor.
var ErrPropNotSet = errors.New("zfs: property not set")

// validUserProp mirrors the kernel's zfs_prop_user (module/zcommon/zfs_prop.c):
// a user property name is made only of zprop_valid_char characters
// (module/zcommon/zprop_common.c: a-z, 0-9, '-', '_', '.', ':') and contains
// at least one ':'. Anything else is either a native property name (which
// zfs_set_prop_nvlist would then type-check as such) or EINVAL. The
// ZAP_MAXNAMELEN (256) bound is the one dsl_props_set_check
// (module/zfs/dsl_prop.c) answers with ENAMETOOLONG; checking it here gives
// the caller the property's name instead of a bare errno.
func validUserProp(prop string) error {
	if prop == "" || len(prop) >= 256 {
		return fmt.Errorf("zfs: user property name %q: length must be 1..255", prop)
	}
	colon := false
	for i := 0; i < len(prop); i++ {
		c := prop[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		case c == ':':
			colon = true
		default:
			return fmt.Errorf("zfs: user property name %q: invalid character %q", prop, c)
		}
	}
	if !colon {
		return fmt.Errorf("zfs: user property name %q has no ':' (that is a native property name)", prop)
	}
	return nil
}
