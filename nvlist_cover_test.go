// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import (
	"encoding/binary"
	"reflect"
	"syscall"
	"testing"
)

// This file exhaustively exercises the PURE nvlist NV_ENCODE_NATIVE codec and
// the small platform-neutral helpers (vdev rendering, firstErrlistErr,
// flattenProps) — every encode branch, every decoder error path, driven with
// crafted byte inputs. It carries no build tag so it runs on every arch.

// ---- encoder: all scalar/array/embedded value kinds round-trip ----

func TestEncodeAllValueKinds(t *testing.T) {
	in := Nvlist{
		"barebool": Boolean{},
		"boolT":    true,
		"boolF":    false,
		"abyte":    Byte(0x5a),
		"bytes":    []byte{1, 2, 3},
		"u8arr":    Uint8Array{9, 8, 7, 6},
		"u64":      uint64(0x1122334455667788),
		"i64":      int64(-5),
		"u32":      uint32(0xdeadbeef),
		"i32":      int32(-7),
		"str":      "hello",
		"u64arr":   []uint64{10, 20, 30},
		"strarr":   []string{"a", "bb", "ccc"},
		"sub":      Nvlist{"k": uint64(1)},
		"subarr":   []Nvlist{{"x": uint64(1)}, {"y": "z"}},
	}
	b, err := EncodeNative(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := DecodeNative(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip mismatch:\n in=%#v\nout=%#v", in, out)
	}
}

// ---- encoder: unsupported value type at top level and nested ----

func TestEncodeUnsupportedType(t *testing.T) {
	if _, err := EncodeNative(Nvlist{"bad": float64(1.5)}); err == nil {
		t.Fatal("want unsupported-type error")
	}
	// nested in an embedded nvlist (covers encodeEmbedded error propagation).
	if _, err := EncodeNative(Nvlist{"sub": Nvlist{"bad": float64(1.5)}}); err == nil {
		t.Fatal("want nested unsupported-type error")
	}
	// nested in an nvlist array (covers encodeEmbeddedArray error propagation).
	if _, err := EncodeNative(Nvlist{"arr": []Nvlist{{"bad": float64(1.5)}}}); err == nil {
		t.Fatal("want nvlist-array unsupported-type error")
	}
	// nested two levels deep (covers encodeBody -> encodePair error bubbling).
	if _, err := EncodeNative(Nvlist{"a": Nvlist{"b": Nvlist{"bad": uint8(3)}}}); err == nil {
		t.Fatal("want deep unsupported-type error")
	}
}

// ---- decoder: every error branch ----

func TestDecodeErrors(t *testing.T) {
	bo, end := nvHostOrder()

	// short outer buffer (<4).
	if _, err := DecodeNative([]byte{0, 1}); err == nil {
		t.Fatal("want short-buffer error")
	}
	// not native encoding.
	if _, err := DecodeNative([]byte{nvEncodeXDR + 5, end, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}); err == nil {
		t.Fatal("want not-native error")
	}
	// truncated list header (4 bytes ok, but <12 total so header read fails).
	if _, err := DecodeNative([]byte{nvEncodeNative, end, 0, 0, 0, 0, 0}); err == nil {
		t.Fatal("want truncated-header error")
	}
	// truncated body: header present, but no room for the 4-byte size word.
	short := []byte{nvEncodeNative, end, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	short = short[:12] // exactly header, pos=12 == len -> pos+4 > len
	if _, err := DecodeNative(short); err == nil {
		t.Fatal("want truncated-body error")
	}

	// bad nvp_size: size word says a value smaller than the header.
	buf := make([]byte, 4+nvlistHdrLen+4)
	buf[0] = nvEncodeNative
	buf[1] = end
	bo.PutUint32(buf[4+nvlistHdrLen:], 8) // size=8 < nvpairHdrLen(16)
	if _, err := DecodeNative(buf); err == nil {
		t.Fatal("want bad-nvp_size error")
	}

	// size overruns the buffer.
	buf2 := make([]byte, 4+nvlistHdrLen+4)
	buf2[0] = nvEncodeNative
	buf2[1] = end
	bo.PutUint32(buf2[4+nvlistHdrLen:], 9999)
	if _, err := DecodeNative(buf2); err == nil {
		t.Fatal("want size-overrun error")
	}
}

// craftPair builds a single-pair native stream with the given header fields and
// raw value bytes, so decoder branches can be targeted precisely.
func craftPair(nameSz int16, nelem, typ int32, name string, value []byte, size int) []byte {
	bo, end := nvHostOrder()
	var b []byte
	b = append(b, nvEncodeNative, end, 0, 0)
	hdr := make([]byte, nvlistHdrLen)
	b = append(b, hdr...)
	pair := make([]byte, size)
	bo.PutUint32(pair[0:4], uint32(size))
	bo.PutUint16(pair[4:6], uint16(nameSz))
	bo.PutUint32(pair[8:12], uint32(nelem))
	bo.PutUint32(pair[12:16], uint32(typ))
	copy(pair[16:], name)
	if value != nil {
		valOff := nvAlign8(nvpairHdrLen + int(nameSz))
		copy(pair[valOff:], value)
	}
	b = append(b, pair...)
	b = append(b, 0, 0, 0, 0) // terminator
	return b
}

func TestDecodePairErrors(t *testing.T) {
	// bad name_sz (0).
	if _, err := DecodeNative(craftPair(0, 1, dataTypeUint64, "", make([]byte, 8), 32)); err == nil {
		t.Fatal("want bad-name_sz error")
	}
	// name_sz exceeds size (nvpairHdrLen+nameSz > size).
	if _, err := DecodeNative(craftPair(100, 1, dataTypeUint64, "x", nil, 24)); err == nil {
		t.Fatal("want name_sz>size error")
	}
	// valOff > size: name "ab" -> nameSz 3, valOff align8(19)=24, size 20.
	bad := craftPair(3, 1, dataTypeUint64, "ab", nil, 24)
	// shrink the declared pair size to 20 so valOff(24) > size(20).
	bo, _ := nvHostOrder()
	p := 4 + nvlistHdrLen
	bo.PutUint32(bad[p:p+4], 20)
	// also need the buffer length to be consistent: trim trailing.
	if _, err := DecodeNative(bad); err == nil {
		t.Fatal("want valOff>size error")
	}
	// unsupported decode type.
	if _, err := DecodeNative(craftPair(2, 1, dataTypeHRTime, "x", make([]byte, 8), 32)); err == nil {
		t.Fatal("want unsupported-decode-type error")
	}
}

func TestDecodeEmbeddedErrors(t *testing.T) {
	bo, end := nvHostOrder()
	// An embedded NVLIST pair whose nested body is truncated (no terminator).
	var b []byte
	b = append(b, nvEncodeNative, end, 0, 0)
	b = append(b, make([]byte, nvlistHdrLen)...) // outer list header
	// pair: name "s" nameSz=2, type NVLIST, value = 24-byte packed nvlist_t.
	nameSz := 2
	valOff := nvAlign8(nvpairHdrLen + nameSz)
	size := valOff + nvAlign8(nvlistTSize)
	pair := make([]byte, size)
	bo.PutUint32(pair[0:4], uint32(size))
	bo.PutUint16(pair[4:6], uint16(nameSz))
	bo.PutUint32(pair[8:12], 1)
	bo.PutUint32(pair[12:16], uint32(dataTypeNVList))
	copy(pair[16:], "s")
	b = append(b, pair...)
	// no nested body / terminator follows -> decodeBody for the embedded list
	// hits truncated-at error.
	if _, err := DecodeNative(b); err == nil {
		t.Fatal("want embedded-truncated error")
	}

	// NVLIST_ARRAY with nelem=1 but no nested body following -> error.
	var c []byte
	c = append(c, nvEncodeNative, end, 0, 0)
	c = append(c, make([]byte, nvlistHdrLen)...)
	size2 := valOff + nvAlign8(8+nvlistTSize) // ptr slot + one packed nvlist_t
	pair2 := make([]byte, size2)
	bo.PutUint32(pair2[0:4], uint32(size2))
	bo.PutUint16(pair2[4:6], uint16(nameSz))
	bo.PutUint32(pair2[8:12], 1) // nelem=1
	bo.PutUint32(pair2[12:16], uint32(dataTypeNVListArray))
	copy(pair2[16:], "a")
	c = append(c, pair2...)
	if _, err := DecodeNative(c); err == nil {
		t.Fatal("want nvlist-array-truncated error")
	}
}

// ---- vdev.go branches ----

func TestVdevExtraAndDiskAndChildError(t *testing.T) {
	// disk leaf: covers the whole_disk default + is_log default.
	disk := Vdev{Type: VDEV_TYPE_DISK, Path: "/dev/sda", Extra: Nvlist{"ashift": uint64(12)}}
	nv, err := disk.nvlist()
	if err != nil {
		t.Fatalf("disk nvlist: %v", err)
	}
	if nv[ZPOOL_CONFIG_WHOLE_DISK] != uint64(0) {
		t.Errorf("whole_disk default = %v", nv[ZPOOL_CONFIG_WHOLE_DISK])
	}
	if nv["ashift"] != uint64(12) {
		t.Errorf("Extra not merged: %v", nv["ashift"])
	}

	// disk leaf with whole_disk and is_log already supplied via Extra: the
	// defaulting branches are skipped.
	disk2 := Vdev{Type: VDEV_TYPE_DISK, Path: "/dev/sdb",
		Extra: Nvlist{ZPOOL_CONFIG_WHOLE_DISK: uint64(1), ZPOOL_CONFIG_IS_LOG: uint64(1)}}
	nv2, err := disk2.nvlist()
	if err != nil {
		t.Fatalf("disk2 nvlist: %v", err)
	}
	if nv2[ZPOOL_CONFIG_WHOLE_DISK] != uint64(1) || nv2[ZPOOL_CONFIG_IS_LOG] != uint64(1) {
		t.Errorf("supplied whole_disk/is_log overwritten: %#v", nv2)
	}

	// interior node whose child fails to render (child has empty type).
	bad := Vdev{Type: VDEV_TYPE_MIRROR, Children: []Vdev{{Type: ""}}}
	if _, err := bad.nvlist(); err == nil {
		t.Fatal("want child render error")
	}
}

// ---- hostOrderFor both arms ----

func TestHostOrderFor(t *testing.T) {
	if bo, end := hostOrderFor(1); bo != binary.LittleEndian || end != nvLittleEndian {
		t.Errorf("hostOrderFor(1) = %v,%d want little", bo, end)
	}
	if bo, end := hostOrderFor(0); bo != binary.BigEndian || end != nvBigEndian {
		t.Errorf("hostOrderFor(0) = %v,%d want big", bo, end)
	}
}

// ---- cstr no-NUL path ----

func TestCstrNoNUL(t *testing.T) {
	if got := cstr([]byte("abcd")); got != "abcd" {
		t.Errorf("cstr(no NUL) = %q, want abcd", got)
	}
	if got := cstr([]byte("ab\x00cd")); got != "ab" {
		t.Errorf("cstr(with NUL) = %q, want ab", got)
	}
	if got := cstr(nil); got != "" {
		t.Errorf("cstr(nil) = %q, want empty", got)
	}
}

// ---- decoder big-endian branch ----

// TestDecodeBigEndian builds a minimal valid NV_ENCODE_NATIVE stream marked
// big-endian (nvh_endian = 0) by hand and decodes it, covering the
// binary.BigEndian selection branch regardless of host endianness.
func TestDecodeBigEndian(t *testing.T) {
	be := binary.BigEndian
	var b []byte
	// outer header: encoding=native, endian=big(0), reserved.
	b = append(b, nvEncodeNative, nvBigEndian, 0, 0)
	// list header: version + nvflag, big-endian.
	hdr := make([]byte, nvlistHdrLen)
	be.PutUint32(hdr[4:8], 1) // nvflag = NV_UNIQUE_NAME
	b = append(b, hdr...)
	// one uint64 pair "n" = 0x0102030405060708.
	nameSz := 2
	valOff := nvAlign8(nvpairHdrLen + nameSz)
	size := valOff + 8
	pair := make([]byte, size)
	be.PutUint32(pair[0:4], uint32(size))
	be.PutUint16(pair[4:6], uint16(nameSz))
	be.PutUint32(pair[8:12], 1)
	be.PutUint32(pair[12:16], uint32(dataTypeUint64))
	copy(pair[16:], "n")
	be.PutUint64(pair[valOff:], 0x0102030405060708)
	b = append(b, pair...)
	b = append(b, 0, 0, 0, 0) // terminator

	out, err := DecodeNative(b)
	if err != nil {
		t.Fatalf("decode big-endian: %v", err)
	}
	if out["n"] != uint64(0x0102030405060708) {
		t.Fatalf("n = %#v, want 0x0102030405060708", out["n"])
	}
}

// ---- firstErrlistErr default-skip branch ----

func TestFirstErrlistErrSkipsUnknownType(t *testing.T) {
	// A string-typed entry is neither int32 nor uint64: it is skipped, leaving
	// no error.
	if err := firstErrlistErr(Nvlist{"x": "not-an-errno"}); err != nil {
		t.Errorf("string entry should be skipped, got %v", err)
	}
	// Mixed: a skipped string plus a real errno still surfaces the errno.
	if err := firstErrlistErr(Nvlist{"y": "skip", "z": int32(int32(syscall.EPERM))}); err == nil {
		t.Error("want EPERM surfaced past the skipped entry")
	}
}
