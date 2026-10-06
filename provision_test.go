// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import "testing"

func TestValidUserProp(t *testing.T) {
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'a'
	}
	long[0] = ':'
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"fileshare:owner", true},
		{"org.example:a-b_c.d", true},
		{":", true},
		{string(long[:255]), true},
		{"", false},
		{string(long), false},       // 256 >= ZAP_MAXNAMELEN
		{"quota", false},            // native name, no ':'
		{"Fileshare:owner", false},  // upper case is not a zprop_valid_char
		{"fileshare:own er", false}, // space
	} {
		if err := validUserProp(c.name); (err == nil) != c.ok {
			t.Errorf("validUserProp(%q) = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}
