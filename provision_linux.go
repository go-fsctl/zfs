// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"fmt"
)

// CreateFilesystemWithProps creates the filesystem `name` with the properties
// `props` applied by the SAME ZFS_IOC_CREATE call, mirroring
// lzc_create(fsname, LZC_DATSET_TYPE_ZFS, props, NULL, 0)
// (lib/libzfs_core/libzfs_core.c). The input nvlist is
//
//	{ "type": <int32 DMU_OST_ZFS>, "props": { ... } }   // "props" omitted when empty
//
// Values use the kernel's native type per property, as for SetProp: uint64 for
// NUMBER properties ("quota", "refquota") and for INDEX properties
// ("canmount": uint64(ZFS_CANMOUNT_NOAUTO)), string for STRING properties
// ("mountpoint": ZFS_MOUNTPOINT_LEGACY) and for user properties
// ("fileshare:owner": "..."; see validUserProp for the name rules).
//
// What "one ioctl" does and does not buy, per zfs_ioc_create
// (module/zfs/zfs_ioctl.c):
//
//   - The kernel creates the objset (dmu_objset_create) and THEN applies the
//     properties in a second step (zfs_set_prop_nvlist with
//     ZPROP_SRC_LOCAL); the source itself carries the comment that it would
//     be nice to do this atomically. A concurrent observer can therefore see
//     the dataset for an instant without its properties. The kernel never
//     mounts it in that window: mounting is done in userspace (zfs(8) create
//     calls zfs_mount_and_share, cmd/zfs/zfs_main.c), and this call does not.
//   - If ANY property fails, the kernel destroys the new dataset
//     (dsl_destroy_head) before returning the error, so on error the caller
//     never holds an untagged or unquota'd dataset — unless that destroy
//     itself failed, which zfs_ioc_create does not report.
//   - The per-property errors come back in the outnvl as {prop: int32 errno}
//     (zfs_set_prop_nvlist fills the errlist); one of them is named in the
//     returned error alongside the ioctl's errno.
//   - "Create-time-only" properties (normalization, utf8only,
//     casesensitivity) are honoured here too: zfs_fill_zplprops reads them from
//     the same nvlist before the objset exists.
//
// CreateFilesystem(name) is CreateFilesystemWithProps(name, nil) without the
// per-property error decoding.
func (h *Handle) CreateFilesystemWithProps(name string, props Nvlist) error {
	innvl := Nvlist{"type": int32(DMU_OST_ZFS)}
	if len(props) > 0 {
		innvl["props"] = props
	}
	out, err := h.callNewName(ZFS_IOC_CREATE, name, innvl)
	if err != nil {
		if perr := firstErrlistErr(out); perr != nil {
			return fmt.Errorf("ZFS_IOC_CREATE %q: %w (rejected property %v; the kernel destroyed the dataset)", name, err, perr)
		}
		return fmt.Errorf("ZFS_IOC_CREATE %q: %w", name, err)
	}
	return nil
}

// SetQuota sets the "quota" property of `fs` to `bytes` (0 = none). quota
// limits the space the dataset AND its descendants (file systems and
// snapshots) may consume (man7/zfsprops.7). The kernel applies it through
// dsl_dir_set_quota (zfs_prop_set_special, module/zfs/zfs_ioctl.c) and
// refuses a value below the space already used.
func (h *Handle) SetQuota(fs string, bytes uint64) error {
	return h.SetProp(fs, Nvlist{"quota": bytes})
}

// Quota returns the "quota" property of `fs` in bytes (0 = none).
func (h *Handle) Quota(fs string) (uint64, error) {
	return h.uint64Prop(fs, "quota")
}

// SetRefquota sets the "refquota" property of `fs` to `bytes` (0 = none).
// Unlike quota, refquota does NOT count descendants or snapshots: it bounds
// the space the dataset itself references (man7/zfsprops.7) — the limit a
// share's users actually hit, with EDQUOT. The kernel applies it through
// dsl_dataset_set_refquota.
func (h *Handle) SetRefquota(fs string, bytes uint64) error {
	return h.SetProp(fs, Nvlist{"refquota": bytes})
}

// Refquota returns the "refquota" property of `fs` in bytes (0 = none).
func (h *Handle) Refquota(fs string) (uint64, error) {
	return h.uint64Prop(fs, "refquota")
}

// uint64Prop reads one NUMBER property from ZFS_IOC_OBJSET_STATS. quota and
// refquota are always present there for a filesystem: dsl_dir_stats
// (module/zfs/dsl_dir.c) and dsl_dataset_stats (module/zfs/dsl_dataset.c) add
// them with dsl_prop_nvlist_add_uint64 whatever their source.
func (h *Handle) uint64Prop(fs, prop string) (uint64, error) {
	props, err := h.GetProps(fs)
	if err != nil {
		return 0, err
	}
	v, ok := props[prop].(uint64)
	if !ok {
		return 0, fmt.Errorf("%s of %q: got %T, want uint64", prop, fs, props[prop])
	}
	return v, nil
}

// SetUserProp sets the user property `prop` (a name containing ':', e.g.
// "fileshare:owner") of `fs` to `value`. zfs_set_prop_nvlist requires a user
// property's value to be a string. User properties are ALWAYS inherited by
// descendants (man7/zfsprops.7): tagging a parent tags every child that does
// not set its own value — check UserProp's source to tell the two apart.
func (h *Handle) SetUserProp(fs, prop, value string) error {
	if err := validUserProp(prop); err != nil {
		return err
	}
	return h.SetProp(fs, Nvlist{prop: value})
}

// UserProp returns the value of the user property `prop` on `fs` and its
// source: the name of the dataset the value is set on (== fs when set locally,
// an ancestor when inherited), or PropSourceReceived when it came in a send
// stream — the "source" dsl_prop_get_all_impl (module/zfs/dsl_prop.c) records
// as the setpoint. A property set nowhere yields an error wrapping
// ErrPropNotSet.
//
// An ownership check therefore wants `source == fs`, not just a matching
// value: a child of a tagged dataset reports the parent's value.
func (h *Handle) UserProp(fs, prop string) (value, source string, err error) {
	if err := validUserProp(prop); err != nil {
		return "", "", err
	}
	nv, err := h.ObjsetStats(fs)
	if err != nil {
		return "", "", err
	}
	entry, ok := nv[prop].(Nvlist)
	if !ok {
		return "", "", fmt.Errorf("%s of %q: %w", prop, fs, ErrPropNotSet)
	}
	value, ok = entry["value"].(string)
	if !ok {
		return "", "", fmt.Errorf("%s of %q: value is %T, want string", prop, fs, entry["value"])
	}
	source, _ = entry["source"].(string)
	return value, source, nil
}
