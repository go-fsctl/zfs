// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import (
	"fmt"
	"os/user"
	"strconv"
)

// SpaceProp selects which userspace/quota property ZFS_IOC_USERSPACE_MANY
// reports. It maps directly onto zfs_userquota_prop_t and is written into
// zc_objset_type. The "*USED" variants report consumed bytes (or object
// counts, for the *OBJ* variants) per identity; the "*QUOTA" variants report
// the configured quota per identity.
type SpaceProp uint64

const (
	// UserUsed reports bytes used per user id (ZFS_PROP_USERUSED).
	UserUsed SpaceProp = ZFS_PROP_USERUSED
	// UserQuota reports the byte quota per user id (ZFS_PROP_USERQUOTA).
	UserQuota SpaceProp = ZFS_PROP_USERQUOTA
	// GroupUsed reports bytes used per group id (ZFS_PROP_GROUPUSED).
	GroupUsed SpaceProp = ZFS_PROP_GROUPUSED
	// GroupQuota reports the byte quota per group id (ZFS_PROP_GROUPQUOTA).
	GroupQuota SpaceProp = ZFS_PROP_GROUPQUOTA
	// UserObjUsed reports object counts used per user id (ZFS_PROP_USEROBJUSED).
	UserObjUsed SpaceProp = ZFS_PROP_USEROBJUSED
	// UserObjQuota reports the object-count quota per user id.
	UserObjQuota SpaceProp = ZFS_PROP_USEROBJQUOTA
	// GroupObjUsed reports object counts used per group id.
	GroupObjUsed SpaceProp = ZFS_PROP_GROUPOBJUSED
	// GroupObjQuota reports the object-count quota per group id.
	GroupObjQuota SpaceProp = ZFS_PROP_GROUPOBJQUOTA
	// ProjectUsed reports bytes used per project id (ZFS_PROP_PROJECTUSED).
	ProjectUsed SpaceProp = ZFS_PROP_PROJECTUSED
	// ProjectQuota reports the byte quota per project id.
	ProjectQuota SpaceProp = ZFS_PROP_PROJECTQUOTA
	// ProjectObjUsed reports object counts used per project id.
	ProjectObjUsed SpaceProp = ZFS_PROP_PROJECTOBJUSED
	// ProjectObjQuota reports the object-count quota per project id.
	ProjectObjQuota SpaceProp = ZFS_PROP_PROJECTOBJQUOTA
)

// quotaPrefix returns the userquota@-style property prefix for a SpaceProp,
// matching zfs_userquota_prop_prefixes in the kernel. The full property name
// for a SetUserQuota / GetProps lookup is prefix + "<who>" (e.g.
// "userquota@1000"). Only the quota (settable) variants have meaningful
// prefixes here.
func (p SpaceProp) quotaPrefix() (string, bool) {
	switch p {
	case UserUsed:
		return "userused@", true
	case UserQuota:
		return "userquota@", true
	case GroupUsed:
		return "groupused@", true
	case GroupQuota:
		return "groupquota@", true
	case UserObjUsed:
		return "userobjused@", true
	case UserObjQuota:
		return "userobjquota@", true
	case GroupObjUsed:
		return "groupobjused@", true
	case GroupObjQuota:
		return "groupobjquota@", true
	case ProjectUsed:
		return "projectused@", true
	case ProjectQuota:
		return "projectquota@", true
	case ProjectObjUsed:
		return "projectobjused@", true
	case ProjectObjQuota:
		return "projectobjquota@", true
	default:
		return "", false
	}
}

// String renders the SpaceProp as its property prefix without the trailing '@'
// (e.g. "userused"), or a numeric fallback for an unknown value.
func (p SpaceProp) String() string {
	if pre, ok := p.quotaPrefix(); ok {
		return pre[:len(pre)-1]
	}
	return fmt.Sprintf("SpaceProp(%d)", uint64(p))
}

// SpaceEntry is one row of a ZFS_IOC_USERSPACE_MANY result: the consumed (or
// quota) value for a single identity. For POSIX users/groups the Domain is
// empty and RID is the numeric uid/gid; for SMB identities Domain carries the
// SID domain and RID the relative id. Value is bytes for the byte properties
// and an object count for the *OBJ* properties.
type SpaceEntry struct {
	Domain string // SID domain ("" for a plain POSIX uid/gid/project id)
	RID    uint32 // relative id: the uid/gid/project id for POSIX identities
	Value  uint64 // bytes used / quota, or object count for *OBJ* props
}

// encodeUserQuota builds the property name and the three-element value the
// kernel expects for a userquota@-family property. It is separate from
// SetUserQuota so that the encoding -- which is the whole defect -- can be
// checked without a pool.
//
// who is a decimal uid/gid/project id, or a user or group NAME. Resolving a
// name is libzfs's job, not the kernel's: userquota_propname_decode calls
// getpwnam before the ioctl, and nothing downstream of here would. Project
// ids have no name space, so a non-numeric project identity is refused rather
// than looked up in the wrong table.
func encodeUserQuota(prop SpaceProp, who string, quota uint64) (string, []uint64, error) {
	// ⛔ quotaPrefix answers for the read-only *USED properties too -- they
	// have names, they are just not settable -- so it cannot be the settable
	// check. That check used to live in SetUserQuota and this encoder was
	// written trusting the prefix; a test caught it immediately. One
	// definition, here, where the name is built.
	switch prop {
	case UserQuota, GroupQuota, ProjectQuota,
		UserObjQuota, GroupObjQuota, ProjectObjQuota:
	default:
		return "", nil, fmt.Errorf("%s is not a settable quota property", prop)
	}
	if who == "" {
		return "", nil, fmt.Errorf("empty identity")
	}
	// The switch above admits only the six settable properties, and every one
	// of them has a prefix -- so this cannot fail, and a branch nothing can
	// reach is a branch no test can cover. This package gates at 100%.
	prefix, _ := prop.quotaPrefix()
	rid, err := resolveIdentity(prop, who)
	if err != nil {
		return "", nil, err
	}
	// "%s%llx-%s": the rid in hex, then the dash, then the domain -- empty for
	// a POSIX identity, and the dash is NOT optional: strchr(propname, '-')
	// returning NULL is one of the three ways the kernel answers EINVAL.
	name := prefix + strconv.FormatUint(rid, 16) + "-"
	return name, []uint64{uint64(prop), rid, quota}, nil
}

func resolveIdentity(prop SpaceProp, who string) (uint64, error) {
	if rid, err := strconv.ParseUint(who, 10, 64); err == nil {
		return rid, nil
	}
	switch prop {
	case UserQuota, UserObjQuota:
		u, err := user.Lookup(who)
		if err != nil {
			return 0, fmt.Errorf("no such user %q: %w", who, err)
		}
		return strconv.ParseUint(u.Uid, 10, 64)
	case GroupQuota, GroupObjQuota:
		g, err := user.LookupGroup(who)
		if err != nil {
			return 0, fmt.Errorf("no such group %q: %w", who, err)
		}
		return strconv.ParseUint(g.Gid, 10, 64)
	}
	return 0, fmt.Errorf("project identity %q is not a number, and project ids have no names", who)
}
