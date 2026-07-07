// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import "fmt"

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
