// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

// SendOptions controls a ZFS send stream. The boolean flags map to the
// presence-only nvlist keys the kernel checks in zfs_ioc_send_new
// (largeblockok/embedok/compressok/rawok): each is added to the input nvlist
// only when true. Derived from OpenZFS 2.2.2 zfs_ioc_send_new() and
// lzc_send_flags.
type SendOptions struct {
	// FromSnap, if non-empty, requests an incremental stream from this
	// snapshot (the "fromsnap" key). It may be a short snapshot name (e.g.
	// "s0") in the same dataset as the sent snapshot, or a full
	// "pool/ds@snap" / bookmark name.
	FromSnap string

	// LargeBlocks enables the large_blocks feature ("largeblockok"),
	// allowing block sizes above 128 KiB to be sent without splitting.
	LargeBlocks bool

	// EmbedData enables WRITE_EMBEDDED records ("embedok") for blocks stored
	// embedded in block pointers.
	EmbedData bool

	// Compress sends compressed blocks as-is ("compressok") rather than
	// decompressing them first.
	Compress bool

	// Raw sends an encrypted dataset's blocks without decrypting ("rawok").
	Raw bool
}

// RecvOptions controls a ZFS receive. Derived from OpenZFS 2.2.2
// zfs_ioc_recv_new().
type RecvOptions struct {
	// Origin names the clone origin snapshot for an incremental receive of a
	// clone ("origin"). Optional.
	Origin string

	// Force discards changes to the destination before receiving ("force",
	// equivalent to `zfs recv -F`).
	Force bool

	// Resumable marks a partially-received dataset resumable on failure
	// ("resumable", equivalent to `zfs recv -s`).
	Resumable bool
}

// BeginRecord summarizes the leading DRR_BEGIN dmu_replay_record of a stream.
// It is returned by Receive for diagnostics; the raw 312-byte record is what
// the kernel actually consumes.
type BeginRecord struct {
	Magic        uint64 // must equal dmuBackupMagic for a native-endian stream
	VersionInfo  uint64
	CreationTime uint64
	Type         uint32 // dmu_objset_type_t of the sent dataset
	Flags        uint32
	ToGuid       uint64
	FromGuid     uint64 // nonzero for an incremental stream
	ToName       string // drr_toname: the source snapshot's full name
}

// parseBeginRecord decodes the fixed fields of a leading dmu_replay_record_t
// (a DRR_BEGIN record) into a BeginRecord. The stream is host-endian for a
// native send; a foreign-endian stream carries the byte-swapped magic, which
// the caller detects via the Magic field. rec must be at least
// sizeofDmuReplayRecord bytes.
func parseBeginRecord(rec []byte) BeginRecord {
	bo, _ := nvHostOrder()
	return BeginRecord{
		Magic:        bo.Uint64(rec[offDrrBeginMagic : offDrrBeginMagic+8]),
		VersionInfo:  bo.Uint64(rec[offDrrBeginVerInf : offDrrBeginVerInf+8]),
		CreationTime: bo.Uint64(rec[offDrrBeginCtime : offDrrBeginCtime+8]),
		Type:         bo.Uint32(rec[offDrrBeginType : offDrrBeginType+4]),
		Flags:        bo.Uint32(rec[offDrrBeginFlags : offDrrBeginFlags+4]),
		ToGuid:       bo.Uint64(rec[offDrrBeginToGuid : offDrrBeginToGuid+8]),
		FromGuid:     bo.Uint64(rec[offDrrBeginFromG : offDrrBeginFromG+8]),
		ToName:       cstr(rec[offDrrBeginToName:]),
	}
}
