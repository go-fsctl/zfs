// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import (
	"bytes"
	"reflect"
	"testing"
)

// rawKey is a deterministic 32-byte raw wrapping key used across the crypto
// round-trip tests (keyformat=raw expects exactly 32 bytes).
var rawKey = func() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i + 1)
	}
	return b
}()

// TestUint8ArrayType pins the on-wire encoding of Uint8Array: it must carry the
// DATA_TYPE_UINT8_ARRAY tag (26) — NOT DATA_TYPE_BYTE_ARRAY (10) — with the raw
// bytes verbatim and nvp_value_elem == the element count. The crypto ioctls
// reject the wrong type, so this is the load-bearing distinction.
func TestUint8ArrayType(t *testing.T) {
	nv := Nvlist{"wkeydata": Uint8Array(rawKey)}
	b, err := EncodeNative(nv)
	if err != nil {
		t.Fatalf("EncodeNative: %v", err)
	}
	bo, _ := nvHostOrder()

	// Walk the single pair: 4-byte outer header + 8-byte nvlist_t header
	// (version + nvflag), then the first pair.
	// nvpair header: size(4) name_sz(2) reserve(2) value_elem(4) type(4).
	off := 4 + nvlistHdrLen
	nameSz := int(bo.Uint16(b[off+4 : off+6]))
	nelem := int(int32(bo.Uint32(b[off+8 : off+12])))
	typ := int(int32(bo.Uint32(b[off+12 : off+16])))
	if typ != dataTypeUint8Array {
		t.Errorf("wkeydata type = %d, want %d (UINT8_ARRAY)", typ, dataTypeUint8Array)
	}
	if typ == dataTypeByteArray {
		t.Errorf("wkeydata wrongly encoded as BYTE_ARRAY (%d)", dataTypeByteArray)
	}
	if nelem != len(rawKey) {
		t.Errorf("wkeydata nelem = %d, want %d", nelem, len(rawKey))
	}
	// Value bytes follow the 8-aligned (header+name).
	valOff := off + nvAlign8(nvpairHdrLen+nameSz)
	if !bytes.Equal(b[valOff:valOff+len(rawKey)], rawKey) {
		t.Errorf("wkeydata value bytes not preserved verbatim")
	}
}

// TestUint8ArrayRoundTrip checks Uint8Array survives encode/decode as a
// Uint8Array (and is therefore distinguishable from a []byte byte array).
func TestUint8ArrayRoundTrip(t *testing.T) {
	in := Nvlist{
		"wkeydata": Uint8Array(rawKey),
		"plain":    []byte{0xde, 0xad, 0xbe, 0xef}, // stays BYTE_ARRAY
	}
	b, err := EncodeNative(in)
	if err != nil {
		t.Fatalf("EncodeNative: %v", err)
	}
	out, err := DecodeNative(b)
	if err != nil {
		t.Fatalf("DecodeNative: %v", err)
	}
	if _, ok := out["wkeydata"].(Uint8Array); !ok {
		t.Fatalf("wkeydata decoded as %T, want Uint8Array", out["wkeydata"])
	}
	if _, ok := out["plain"].([]byte); !ok {
		t.Fatalf("plain decoded as %T, want []byte", out["plain"])
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip mismatch:\n in=%#v\nout=%#v", in, out)
	}
}

// TestLoadKeyArgsRoundTrip pins the ZFS_IOC_LOAD_KEY input nvlist: a nested
// hidden_args carrying wkeydata as a uint8 array, plus an optional "noop"
// presence flag for the dry-run/nomount path (lzc_load_key).
func TestLoadKeyArgsRoundTrip(t *testing.T) {
	in := Nvlist{
		ZPOOL_HIDDEN_ARGS: hiddenArgs(rawKey),
		"noop":            Boolean{},
	}
	out := roundTrip(t, in)
	ha, ok := out[ZPOOL_HIDDEN_ARGS].(Nvlist)
	if !ok {
		t.Fatalf("hidden_args decoded as %T, want Nvlist", out[ZPOOL_HIDDEN_ARGS])
	}
	wk, ok := ha["wkeydata"].(Uint8Array)
	if !ok {
		t.Fatalf("wkeydata decoded as %T, want Uint8Array", ha["wkeydata"])
	}
	if !bytes.Equal(wk, rawKey) {
		t.Errorf("wkeydata = %x, want %x", wk, rawKey)
	}
	if _, ok := out["noop"].(Boolean); !ok {
		t.Errorf("noop = %T, want Boolean (presence flag)", out["noop"])
	}
}

// TestChangeKeyArgsRoundTrip pins the ZFS_IOC_CHANGE_KEY input nvlist:
// crypt_cmd (uint64 DCP_CMD_NEW_KEY), hidden_args.wkeydata, and a props
// sub-nvlist (lzc_change_key).
func TestChangeKeyArgsRoundTrip(t *testing.T) {
	in := Nvlist{
		"crypt_cmd":       uint64(DCP_CMD_NEW_KEY),
		ZPOOL_HIDDEN_ARGS: hiddenArgs(rawKey),
		"props":           Nvlist{"keylocation": "prompt"},
	}
	out := roundTrip(t, in)
	if v, _ := out["crypt_cmd"].(uint64); v != DCP_CMD_NEW_KEY {
		t.Errorf("crypt_cmd = %v, want %d", out["crypt_cmd"], DCP_CMD_NEW_KEY)
	}
	ha, _ := out[ZPOOL_HIDDEN_ARGS].(Nvlist)
	if wk, _ := ha["wkeydata"].(Uint8Array); !bytes.Equal(wk, rawKey) {
		t.Errorf("changed wkeydata mismatch")
	}
}

// TestCreateEncryptedArgsRoundTrip pins the ZFS_IOC_CREATE input nvlist for an
// encrypted dataset: int32 "type", a "props" nvlist, and hidden_args.wkeydata
// (lzc_create). The "type" pair must be DATA_TYPE_INT32.
func TestCreateEncryptedArgsRoundTrip(t *testing.T) {
	in := Nvlist{
		"type": int32(DMU_OST_ZFS),
		"props": Nvlist{
			"encryption": uint64(ZIO_CRYPT_AES_256_GCM),
			"keyformat":  uint64(ZFS_KEYFORMAT_RAW),
		},
		ZPOOL_HIDDEN_ARGS: Nvlist{"wkeydata": Uint8Array(rawKey)},
	}
	out := roundTrip(t, in)
	if v, ok := out["type"].(int32); !ok || v != DMU_OST_ZFS {
		t.Errorf("type = %v (%T), want int32 %d", out["type"], out["type"], DMU_OST_ZFS)
	}
	props, _ := out["props"].(Nvlist)
	if v, _ := props["encryption"].(uint64); v != ZIO_CRYPT_AES_256_GCM {
		t.Errorf("encryption prop = %v, want %d", props["encryption"], ZIO_CRYPT_AES_256_GCM)
	}
}

// TestDcpCmdValues pins the dcp_cmd_t constants used for crypt_cmd against the
// 2.2.2 sys/dsl_crypt.h enum ordering.
func TestDcpCmdValues(t *testing.T) {
	for _, c := range []struct {
		name string
		got  int
		want int
	}{
		{"DCP_CMD_NONE", DCP_CMD_NONE, 0},
		{"DCP_CMD_RAW_RECV", DCP_CMD_RAW_RECV, 1},
		{"DCP_CMD_NEW_KEY", DCP_CMD_NEW_KEY, 2},
		{"DCP_CMD_INHERIT", DCP_CMD_INHERIT, 3},
		{"DCP_CMD_FORCE_NEW_KEY", DCP_CMD_FORCE_NEW_KEY, 4},
		{"DCP_CMD_FORCE_INHERIT", DCP_CMD_FORCE_INHERIT, 5},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}
