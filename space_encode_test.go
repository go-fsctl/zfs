// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import (
	"slices"
	"testing"
)

// ⛔ TestUserQuotaEncoding pins the shape the kernel actually demands. The
// version before it sent { "userquota@1000": uint64 } and the kernel answered
// EINVAL, because zfs_prop_set_userquota requires a dash in the name and a
// uint64 array of exactly three:
//
//	if ((dash = strchr(propname, '-')) == NULL ||
//	    nvpair_value_uint64_array(pair, &valary, &vallen) != 0 ||
//	    vallen != 3)
//		return (SET_ERROR(EINVAL));
//
// Every field below is one of the ways that check can fail, so each is
// asserted on its own rather than as one opaque comparison.
func TestUserQuotaEncoding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prop     SpaceProp
		who      string
		quota    uint64
		wantName string
		wantVal  []uint64
	}{
		{
			// The case that failed on the runner: uid 1000, 50 MiB.
			// 1000 decimal is 3e8 hex -- a decimal name would be accepted by
			// strchr and then set the quota for uid 4096.
			"uid 1000, in hex", UserQuota, "1000", 52428800,
			"userquota@3e8-", []uint64{uint64(UserQuota), 1000, 52428800},
		},
		{"uid 0", UserQuota, "0", 1024, "userquota@0-", []uint64{uint64(UserQuota), 0, 1024}},
		{
			// 0 is how a quota is removed; it must still be a three-element
			// array, not an absent value.
			"removing a quota", UserQuota, "42", 0,
			"userquota@2a-", []uint64{uint64(UserQuota), 42, 0},
		},
		{"a group", GroupQuota, "255", 1, "groupquota@ff-", []uint64{uint64(GroupQuota), 255, 1}},
		{"an object quota", UserObjQuota, "16", 9, "userobjquota@10-", []uint64{uint64(UserObjQuota), 16, 9}},
		{"a project", ProjectQuota, "4096", 7, "projectquota@1000-", []uint64{uint64(ProjectQuota), 4096, 7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, val, err := encodeUserQuota(tc.prop, tc.who, tc.quota)
			if err != nil {
				t.Fatalf("encodeUserQuota: %v", err)
			}
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			if len(val) != 3 {
				t.Fatalf("value has %d elements, and the kernel requires exactly 3", len(val))
			}
			if !slices.Equal(val, tc.wantVal) {
				t.Errorf("value = %v, want %v", val, tc.wantVal)
			}
		})
	}
}

// TestUserQuotaNameAlwaysCarriesTheDash: strchr(propname, '-') == NULL is one
// of the three EINVAL paths, and it is the one a plausible-looking name fails.
func TestUserQuotaNameAlwaysCarriesTheDash(t *testing.T) {
	for _, p := range []SpaceProp{UserQuota, GroupQuota, ProjectQuota, UserObjQuota, GroupObjQuota, ProjectObjQuota} {
		name, _, err := encodeUserQuota(p, "7", 1)
		if err != nil {
			t.Fatalf("%v: %v", p, err)
		}
		if name[len(name)-1] != '-' {
			t.Errorf("%v encoded as %q, which has no trailing dash", p, name)
		}
	}
}

// And the refusals, because a wrong identity silently setting someone else's
// quota is worse than an error.
func TestUserQuotaRefusesWhatItCannotEncode(t *testing.T) {
	if _, _, err := encodeUserQuota(UserUsed, "1000", 1); err == nil {
		t.Error("a read-only *USED property was accepted")
	}
	if _, _, err := encodeUserQuota(ProjectQuota, "notanumber", 1); err == nil {
		t.Error("a project identity that is not a number was accepted")
	}
	if _, _, err := encodeUserQuota(UserQuota, "no-such-user-here-4a7f", 1); err == nil {
		t.Error("an unknown user name was accepted")
	}
}
