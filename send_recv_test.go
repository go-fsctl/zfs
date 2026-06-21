// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import (
	"bytes"
	"testing"
)

// TestByteArrayRoundTrip verifies the DATA_TYPE_BYTE_ARRAY native codec path
// added for the ZFS_IOC_RECV_NEW "begin_record" argument: a []byte encodes to
// a byte_array nvpair (nvp_value_elem == byte count) and decodes back
// byte-for-byte.
func TestByteArrayRoundTrip(t *testing.T) {
	payload := make([]byte, sizeofDmuReplayRecord)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	in := Nvlist{
		"begin_record": payload,
		"input_fd":     int32(3),
		"snapname":     "tp2@s1",
		"force":        Boolean{},
	}
	enc, err := EncodeNative(in)
	if err != nil {
		t.Fatalf("EncodeNative: %v", err)
	}
	out, err := DecodeNative(enc)
	if err != nil {
		t.Fatalf("DecodeNative: %v", err)
	}
	got, ok := out["begin_record"].([]byte)
	if !ok {
		t.Fatalf("begin_record decoded as %T, want []byte", out["begin_record"])
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("begin_record round-trip mismatch (len got=%d want=%d)", len(got), len(payload))
	}
	if fd, ok := out["input_fd"].(int32); !ok || fd != 3 {
		t.Errorf("input_fd = %v (%T), want int32 3", out["input_fd"], out["input_fd"])
	}
	if s, ok := out["snapname"].(string); !ok || s != "tp2@s1" {
		t.Errorf("snapname = %v, want tp2@s1", out["snapname"])
	}
	if _, ok := out["force"].(Boolean); !ok {
		t.Errorf("force = %T, want Boolean (presence flag)", out["force"])
	}
}

// TestParseBeginRecord verifies the DRR_BEGIN field extraction against a
// hand-built record laid out per the OpenZFS 2.2.2 dmu_replay_record_t ABI.
func TestParseBeginRecord(t *testing.T) {
	bo, _ := nvHostOrder()
	rec := make([]byte, sizeofDmuReplayRecord)
	// drr_type = DRR_BEGIN (0) is already zero.
	bo.PutUint64(rec[offDrrBeginMagic:], dmuBackupMagic)
	bo.PutUint64(rec[offDrrBeginVerInf:], 0x1)
	bo.PutUint64(rec[offDrrBeginCtime:], 0xdeadbeef)
	bo.PutUint32(rec[offDrrBeginType:], DMU_OST_ZFS)
	bo.PutUint32(rec[offDrrBeginFlags:], 0)
	bo.PutUint64(rec[offDrrBeginToGuid:], 0x1122334455667788)
	bo.PutUint64(rec[offDrrBeginFromG:], 0)
	copy(rec[offDrrBeginToName:], "tp@s1")

	br := parseBeginRecord(rec)
	if br.Magic != dmuBackupMagic {
		t.Errorf("Magic = %#x, want %#x", br.Magic, uint64(dmuBackupMagic))
	}
	if br.Type != DMU_OST_ZFS {
		t.Errorf("Type = %d, want %d (DMU_OST_ZFS)", br.Type, DMU_OST_ZFS)
	}
	if br.ToGuid != 0x1122334455667788 {
		t.Errorf("ToGuid = %#x, want 0x1122334455667788", br.ToGuid)
	}
	if br.FromGuid != 0 {
		t.Errorf("FromGuid = %d, want 0 (full stream)", br.FromGuid)
	}
	if br.ToName != "tp@s1" {
		t.Errorf("ToName = %q, want %q", br.ToName, "tp@s1")
	}
}
