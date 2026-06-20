// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

// Package zfs is a pure-Go libzfs_core: it drives ZFS kernel operations by
// issuing ioctls on /dev/zfs, with no cgo, no libzfs and no shelling out to
// zpool/zfs/zdb.
//
// This file implements the NV_ENCODE_NATIVE nvlist codec used by the /dev/zfs
// ioctl interface. This is NOT the XDR encoding used by on-disk vdev labels:
// the ioctl path packs nvlists in host-endian, in-memory layout. The codec
// here mirrors OpenZFS module/nvpair/nvpair.c (nvs_native_*) byte-for-byte.
//
// Native packing rules (verified against OpenZFS 2.2.2):
//
//	Outer stream header (4 bytes):
//	    nvh_encoding  uint8   1 = NV_ENCODE_NATIVE
//	    nvh_endian    uint8   0 = big, 1 = little (host endianness)
//	    nvh_reserved  [2]uint8
//
//	nvlist_t packed header (8 bytes, from nvs_native_nvlist):
//	    nvl_version   int32
//	    nvl_nvflag    uint32
//	    (nvl_priv/nvl_flag/nvl_pad are NOT emitted at the top level)
//
//	Each nvpair is the in-memory nvpair_t copied verbatim (nvp_size bytes),
//	8-byte aligned:
//	    nvp_size       int32   total bytes of this pair (incl. header+name+value)
//	    nvp_name_sz    int16   strlen(name)+1 (includes NUL)
//	    nvp_reserve    int16   0
//	    nvp_value_elem int32   element count for array types (else 1, 0 for BOOLEAN)
//	    nvp_type       int32   DATA_TYPE_*
//	    name           nvp_name_sz bytes, NUL-terminated
//	    (pad to 8-byte boundary)
//	    value          type-dependent, NV_ALIGN(8)-padded
//
//	  NVP_VALOFF = NV_ALIGN8(16 + nvp_name_sz)
//	  nvp_size   = NV_ALIGN8(16 + nvp_name_sz) + NV_ALIGN8(value_sz)
//
//	The list (and each embedded list) is terminated by a 4-byte zero
//	(a zero nvp_size).
package zfs

import (
	"encoding/binary"
	"fmt"
)

// NV_ENCODE_* and endianness markers.
const (
	// OpenZFS sys/nvpair.h: NV_ENCODE_NATIVE = 0, NV_ENCODE_XDR = 1.
	nvEncodeNative = 0
	nvEncodeXDR    = 1

	// nvh_endian: host_endian is 1 on little-endian, 0 on big-endian.
	nvBigEndian    = 0
	nvLittleEndian = 1
)

// data_type_t values (OpenZFS sys/nvpair.h). Enum begins at
// DATA_TYPE_DONTCARE = -1, DATA_TYPE_UNKNOWN = 0.
const (
	dataTypeUnknown      = 0
	dataTypeBoolean      = 1
	dataTypeByte         = 2
	dataTypeInt16        = 3
	dataTypeUint16       = 4
	dataTypeInt32        = 5
	dataTypeUint32       = 6
	dataTypeInt64        = 7
	dataTypeUint64       = 8
	dataTypeString       = 9
	dataTypeByteArray    = 10
	dataTypeInt16Array   = 11
	dataTypeUint16Array  = 12
	dataTypeInt32Array   = 13
	dataTypeUint32Array  = 14
	dataTypeInt64Array   = 15
	dataTypeUint64Array  = 16
	dataTypeStringArray  = 17
	dataTypeHRTime       = 18
	dataTypeNVList       = 19
	dataTypeNVListArray  = 20
	dataTypeBooleanValue = 21
	dataTypeInt8         = 22
	dataTypeUint8        = 23
	dataTypeBooleanArray = 24
	dataTypeInt8Array    = 25
	dataTypeUint8Array   = 26
)

// nvpairHdrLen is sizeof(nvpair_t): nvp_size(4) + nvp_name_sz(2) +
// nvp_reserve(2) + nvp_value_elem(4) + nvp_type(4).
const nvpairHdrLen = 16

// nvlistHdrLen is the packed nvlist_t header emitted at the start of every
// (sub)list: nvl_version(4) + nvl_nvflag(4).
const nvlistHdrLen = 8

// nvAlign8 rounds up to an 8-byte boundary (NV_ALIGN).
func nvAlign8(n int) int { return (n + 7) &^ 7 }

// Value is one of the supported native value kinds. A nil-valued boolean
// (the bare name) is represented by Boolean.
type Value any

// Boolean is DATA_TYPE_BOOLEAN: a name with no value (e.g. a feature flag).
type Boolean struct{}

// Byte is DATA_TYPE_BYTE.
type Byte uint8

// Nvlist is an ordered map of name to Value. Order is preserved on encode via
// the parallel keys slice when present; PoolConfigs etc. return plain maps.
type Nvlist map[string]Value

// nvHostOrder is the encoding endianness for this host.
var nvHostOrder = func() (binary.ByteOrder, byte) {
	var x uint16 = 1
	if *(*byte)(ptrOfUint16(&x)) == 1 {
		return binary.LittleEndian, nvLittleEndian
	}
	return binary.BigEndian, nvBigEndian
}

// nvEncoder packs an Nvlist into NV_ENCODE_NATIVE bytes.
type nvEncoder struct {
	bo  binary.ByteOrder
	end byte
	buf []byte
}

// EncodeNative serializes nv as a top-level NV_ENCODE_NATIVE stream including
// the 4-byte outer header. This is the form the /dev/zfs ioctl expects in
// zc_nvlist_src.
func EncodeNative(nv Nvlist) ([]byte, error) {
	bo, end := nvHostOrder()
	e := &nvEncoder{bo: bo, end: end}
	// Outer 4-byte header.
	e.buf = append(e.buf, nvEncodeNative, end, 0, 0)
	if err := e.encodeList(nv, 0, 1); err != nil {
		return nil, err
	}
	return e.buf, nil
}

// encodeList writes the top-level nvlist_t header (version + nvflag) then the
// body. version/nvflag mirror what the kernel produces (0 / 1 =
// NV_UNIQUE_NAME). Embedded lists do NOT use this — their version/nvflag live
// in the preceding 24-byte packed nvlist_t value slot (see encodeEmbedded).
func (e *nvEncoder) encodeList(nv Nvlist, version int32, nvflag uint32) error {
	var hdr [nvlistHdrLen]byte
	e.bo.PutUint32(hdr[0:4], uint32(version))
	e.bo.PutUint32(hdr[4:8], nvflag)
	e.buf = append(e.buf, hdr[:]...)
	return e.encodeBody(nv)
}

// encodeBody writes every pair followed by the 4-byte zero terminator
// (nvp_size == 0). This is the shared pair stream used by both top-level and
// embedded lists.
func (e *nvEncoder) encodeBody(nv Nvlist) error {
	// Stable order: sort keys for determinism in tests; the kernel does not
	// require a particular order.
	for _, name := range sortedKeys(nv) {
		if err := e.encodePair(name, nv[name]); err != nil {
			return err
		}
	}
	e.buf = append(e.buf, 0, 0, 0, 0)
	return nil
}

func (e *nvEncoder) encodePair(name string, v Value) error {
	nameSz := len(name) + 1 // includes NUL
	valOff := nvAlign8(nvpairHdrLen + nameSz)

	val, trailer, typ, nelem, err := e.encodeValue(v)
	if err != nil {
		return fmt.Errorf("nvpair %q: %w", name, err)
	}
	valSz := nvAlign8(len(val))
	size := valOff + valSz // nvp_size covers ONLY the in-pair value

	start := len(e.buf)
	e.buf = append(e.buf, make([]byte, size)...)
	b := e.buf[start : start+size]
	e.bo.PutUint32(b[0:4], uint32(size))   // nvp_size
	e.bo.PutUint16(b[4:6], uint16(nameSz)) // nvp_name_sz
	e.bo.PutUint16(b[6:8], 0)              // nvp_reserve
	e.bo.PutUint32(b[8:12], uint32(nelem)) // nvp_value_elem
	e.bo.PutUint32(b[12:16], uint32(typ))  // nvp_type
	copy(b[16:16+len(name)], name)         // name (NUL already zero)
	copy(b[valOff:valOff+len(val)], val)   // value
	// For embedded (nv)lists, the nested pair stream(s) follow the pair
	// inline in the parent buffer — they are NOT counted in nvp_size.
	if len(trailer) > 0 {
		e.buf = append(e.buf, trailer...)
	}
	return nil
}

// encodeValue returns the in-pair value bytes (unpadded), an optional trailer
// (embedded pair streams written after the pair), the data_type_t, and
// nvp_value_elem.
func (e *nvEncoder) encodeValue(v Value) (val, trailer []byte, typ, nelem int, err error) {
	switch x := v.(type) {
	case Boolean:
		return nil, nil, dataTypeBoolean, 0, nil
	case bool:
		b := make([]byte, 4) // boolean_t == int
		if x {
			e.bo.PutUint32(b, 1)
		}
		return b, nil, dataTypeBooleanValue, 1, nil
	case Byte:
		return []byte{byte(x)}, nil, dataTypeByte, 1, nil
	case uint64:
		b := make([]byte, 8)
		e.bo.PutUint64(b, x)
		return b, nil, dataTypeUint64, 1, nil
	case int64:
		b := make([]byte, 8)
		e.bo.PutUint64(b, uint64(x))
		return b, nil, dataTypeInt64, 1, nil
	case uint32:
		b := make([]byte, 4)
		e.bo.PutUint32(b, x)
		return b, nil, dataTypeUint32, 1, nil
	case int32:
		b := make([]byte, 4)
		e.bo.PutUint32(b, uint32(x))
		return b, nil, dataTypeInt32, 1, nil
	case string:
		b := make([]byte, len(x)+1) // NUL-terminated
		copy(b, x)
		return b, nil, dataTypeString, 1, nil
	case []uint64:
		b := make([]byte, 8*len(x))
		for i, u := range x {
			e.bo.PutUint64(b[i*8:], u)
		}
		return b, nil, dataTypeUint64Array, len(x), nil
	case []string:
		// value = nelem*8 zeroed pointer slots, then concatenated
		// NUL-terminated strings.
		ptrs := len(x) * 8
		var strs []byte
		for _, s := range x {
			strs = append(strs, s...)
			strs = append(strs, 0)
		}
		b := make([]byte, ptrs+len(strs))
		copy(b[ptrs:], strs)
		return b, nil, dataTypeStringArray, len(x), nil
	case Nvlist:
		v, tr, err := e.encodeEmbedded(x)
		return v, tr, dataTypeNVList, 1, err
	case []Nvlist:
		v, tr, err := e.encodeEmbeddedArray(x)
		return v, tr, dataTypeNVListArray, len(x), err
	default:
		return nil, nil, 0, 0, fmt.Errorf("unsupported value type %T", v)
	}
}

// encodeEmbedded packs a single nested nvlist. The pair's in-value region is
// exactly the 24-byte packed nvlist_t (carrying version + nvflag); this is all
// that nvp_size covers. The nested list's pair body is returned as the
// trailer, which the caller appends to the parent buffer AFTER the pair — the
// native format inlines embedded pairs into the enclosing stream rather than
// nesting them inside the pair's value (verified against kernel output).
func (e *nvEncoder) encodeEmbedded(nv Nvlist) (val, trailer []byte, err error) {
	val = packedNvlistT(e.bo)
	sub := &nvEncoder{bo: e.bo, end: e.end}
	if err := sub.encodeBody(nv); err != nil {
		return nil, nil, err
	}
	return val, sub.buf, nil
}

// nvlistTSize is NV_ALIGN(sizeof(nvlist_t)) on 64-bit: int32 version + uint32
// nvflag + uint64 priv + uint32 flag + int32 pad = 24 bytes.
const nvlistTSize = 24

// packedNvlistT builds the 24-byte packed nvlist_t value slot for an embedded
// list: nvl_version=0 (NV_VERSION), nvl_nvflag=1 (NV_UNIQUE_NAME), the rest
// (priv/flag/pad) zero — exactly what the kernel emits.
func packedNvlistT(bo binary.ByteOrder) []byte {
	b := make([]byte, nvlistTSize)
	bo.PutUint32(b[0:4], 0) // nvl_version
	bo.PutUint32(b[4:8], 1) // nvl_nvflag = NV_UNIQUE_NAME
	return b
}

// encodeEmbeddedArray packs an array of nested nvlists. The in-value region is
// nelem zeroed pointer slots followed by nelem packed nvlist_t structs (24
// bytes each); the nested pair bodies are returned as the trailer (appended
// after the pair), in element order.
func (e *nvEncoder) encodeEmbeddedArray(lists []Nvlist) (val, trailer []byte, err error) {
	n := len(lists)
	val = make([]byte, n*8) // zeroed pointer array
	for range lists {
		val = append(val, packedNvlistT(e.bo)...)
	}
	for _, nv := range lists {
		sub := &nvEncoder{bo: e.bo, end: e.end}
		if err := sub.encodeBody(nv); err != nil {
			return nil, nil, err
		}
		trailer = append(trailer, sub.buf...)
	}
	return val, trailer, nil
}

// ---- Decoder ----

type nvDecoder struct {
	bo  binary.ByteOrder
	buf []byte
	pos int
}

// DecodeNative parses an NV_ENCODE_NATIVE stream (including the 4-byte outer
// header) into an Nvlist. This is the form the kernel writes into
// zc_nvlist_dst.
func DecodeNative(b []byte) (Nvlist, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("nvlist: short buffer (%d bytes)", len(b))
	}
	enc := b[0]
	end := b[1]
	if enc != nvEncodeNative {
		return nil, fmt.Errorf("nvlist: not native encoding (got %d)", enc)
	}
	var bo binary.ByteOrder = binary.LittleEndian
	if end == nvBigEndian {
		bo = binary.BigEndian
	}
	d := &nvDecoder{bo: bo, buf: b, pos: 4}
	return d.decodeList()
}

// decodeList reads the top-level [version,nvflag] header then the pair body.
func (d *nvDecoder) decodeList() (Nvlist, error) {
	if d.pos+nvlistHdrLen > len(d.buf) {
		return nil, fmt.Errorf("nvlist: truncated header at %d", d.pos)
	}
	d.pos += nvlistHdrLen // skip nvl_version, nvl_nvflag
	return d.decodeBody()
}

// decodeBody reads pairs until the 4-byte zero terminator. Used by both the
// top-level list (after its header) and embedded lists (after their 24-byte
// packed nvlist_t value slot).
func (d *nvDecoder) decodeBody() (Nvlist, error) {
	out := make(Nvlist)
	for {
		if d.pos+4 > len(d.buf) {
			return nil, fmt.Errorf("nvlist: truncated at %d", d.pos)
		}
		size := int(int32(d.bo.Uint32(d.buf[d.pos : d.pos+4])))
		if size == 0 {
			d.pos += 4 // consume terminator
			return out, nil
		}
		if size < nvpairHdrLen || d.pos+size > len(d.buf) {
			return nil, fmt.Errorf("nvlist: bad nvp_size %d at %d", size, d.pos)
		}
		name, v, err := d.decodePair(size)
		if err != nil {
			return nil, err
		}
		out[name] = v
	}
}

// decodePair reads the pair header+value at d.pos (advancing d.pos by size),
// then, for embedded (nv)list types, recursively consumes the nested pair
// stream(s) that follow inline in the same buffer (advancing d.pos further).
func (d *nvDecoder) decodePair(size int) (string, Value, error) {
	b := d.buf[d.pos : d.pos+size]
	nameSz := int(d.bo.Uint16(b[4:6]))
	nelem := int(int32(d.bo.Uint32(b[8:12])))
	typ := int(int32(d.bo.Uint32(b[12:16])))
	if nameSz < 1 || nvpairHdrLen+nameSz > size {
		return "", nil, fmt.Errorf("nvlist: bad name_sz %d", nameSz)
	}
	name := string(b[16 : 16+nameSz-1]) // strip NUL
	valOff := nvAlign8(nvpairHdrLen + nameSz)
	if valOff > size {
		return "", nil, fmt.Errorf("nvlist %q: value offset %d > size %d", name, valOff, size)
	}
	val := b[valOff:size]
	d.pos += size // consume the pair itself

	switch typ {
	case dataTypeNVList:
		// The pair value is only the 24-byte nvlist_t struct; the nested
		// pairs follow inline in the parent buffer at d.pos.
		l, err := d.decodeBody()
		if err != nil {
			return "", nil, fmt.Errorf("nvpair %q: %w", name, err)
		}
		return name, l, nil
	case dataTypeNVListArray:
		// nelem inlined pair streams follow.
		out := make([]Nvlist, nelem)
		for i := 0; i < nelem; i++ {
			l, err := d.decodeBody()
			if err != nil {
				return "", nil, fmt.Errorf("nvpair %q[%d]: %w", name, i, err)
			}
			out[i] = l
		}
		return name, out, nil
	default:
		v, err := d.decodeScalar(typ, nelem, val)
		if err != nil {
			return "", nil, fmt.Errorf("nvpair %q: %w", name, err)
		}
		return name, v, nil
	}
}

func (d *nvDecoder) decodeScalar(typ, nelem int, val []byte) (Value, error) {
	switch typ {
	case dataTypeBoolean:
		return Boolean{}, nil
	case dataTypeBooleanValue:
		return d.bo.Uint32(val[:4]) != 0, nil
	case dataTypeByte:
		return Byte(val[0]), nil
	case dataTypeUint64:
		return d.bo.Uint64(val[:8]), nil
	case dataTypeInt64:
		return int64(d.bo.Uint64(val[:8])), nil
	case dataTypeUint32:
		return d.bo.Uint32(val[:4]), nil
	case dataTypeInt32:
		return int32(d.bo.Uint32(val[:4])), nil
	case dataTypeString:
		return cstr(val), nil
	case dataTypeUint64Array:
		out := make([]uint64, nelem)
		for i := 0; i < nelem; i++ {
			out[i] = d.bo.Uint64(val[i*8 : i*8+8])
		}
		return out, nil
	case dataTypeStringArray:
		// nelem*8 ptr slots then concatenated NUL strings.
		out := make([]string, nelem)
		off := nelem * 8
		for i := 0; i < nelem; i++ {
			s := cstr(val[off:])
			out[i] = s
			off += len(s) + 1
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported decode type %d", typ)
	}
}

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func sortedKeys(nv Nvlist) []string {
	keys := make([]string, 0, len(nv))
	for k := range nv {
		keys = append(keys, k)
	}
	// simple insertion sort to avoid importing sort for a tiny map; keeps
	// encode deterministic for round-trip tests.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
