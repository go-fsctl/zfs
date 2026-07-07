// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import "testing"

// TestSpacePropValues pins the zfs_userquota_prop_t enum order written into
// zc_objset_type for ZFS_IOC_USERSPACE_MANY.
func TestSpacePropValues(t *testing.T) {
	for _, c := range []struct {
		p    SpaceProp
		want uint64
	}{
		{UserUsed, 0}, {UserQuota, 1}, {GroupUsed, 2}, {GroupQuota, 3},
		{UserObjUsed, 4}, {UserObjQuota, 5}, {GroupObjUsed, 6}, {GroupObjQuota, 7},
		{ProjectUsed, 8}, {ProjectQuota, 9}, {ProjectObjUsed, 10}, {ProjectObjQuota, 11},
	} {
		if uint64(c.p) != c.want {
			t.Errorf("%s = %d, want %d", c.p, uint64(c.p), c.want)
		}
	}
}

// TestSpacePropPrefixes pins the userquota@-style prefixes against the kernel's
// zfs_userquota_prop_prefixes table.
func TestSpacePropPrefixes(t *testing.T) {
	for _, c := range []struct {
		p    SpaceProp
		want string
	}{
		{UserUsed, "userused@"}, {UserQuota, "userquota@"},
		{GroupUsed, "groupused@"}, {GroupQuota, "groupquota@"},
		{UserObjUsed, "userobjused@"}, {UserObjQuota, "userobjquota@"},
		{GroupObjUsed, "groupobjused@"}, {GroupObjQuota, "groupobjquota@"},
		{ProjectUsed, "projectused@"}, {ProjectQuota, "projectquota@"},
		{ProjectObjUsed, "projectobjused@"}, {ProjectObjQuota, "projectobjquota@"},
	} {
		got, ok := c.p.quotaPrefix()
		if !ok || got != c.want {
			t.Errorf("%s prefix = %q ok=%v, want %q", c.p, got, ok, c.want)
		}
		if c.p.String() != c.want[:len(c.want)-1] {
			t.Errorf("%s String() = %q", c.p, c.p.String())
		}
	}
	// Unknown prop: no prefix, numeric String.
	var bad SpaceProp = 99
	if _, ok := bad.quotaPrefix(); ok {
		t.Error("unknown prop returned a prefix")
	}
	if bad.String() != "SpaceProp(99)" {
		t.Errorf("bad.String() = %q", bad.String())
	}
}
