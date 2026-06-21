// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

// hiddenArgs wraps the wrapping-key material in the nested "hidden_args"
// nvlist the crypto ioctls (LOAD_KEY / CREATE / CHANGE_KEY) expect. wkeydata
// MUST be a DATA_TYPE_UINT8_ARRAY — the kernel looks it up with
// nvlist_lookup_uint8_array, which is type-strict — so a plain []byte
// (DATA_TYPE_BYTE_ARRAY) would be rejected. The in-memory wire form is the raw
// key bytes, 8-byte padded, with nvp_value_elem == len(key). Mirrors the
// libzfs_core idiom:
//
//	hidden_args = fnvlist_alloc();
//	fnvlist_add_uint8_array(hidden_args, "wkeydata", wkeydata, wkeylen);
//	fnvlist_add_nvlist(args, ZPOOL_HIDDEN_ARGS, hidden_args);
//
// It is defined here (no build tag) so unit tests on any platform can build the
// crypto input nvlists for round-trip verification.
func hiddenArgs(key []byte) Nvlist {
	return Nvlist{"wkeydata": Uint8Array(key)}
}
