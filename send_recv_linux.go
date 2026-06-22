// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Send writes a ZFS replay (send) stream for the snapshot to out via
// ZFS_IOC_SEND_NEW (the libzfs_core lzc_send path). The KERNEL generates the
// DMU replay stream and writes it to the file descriptor; this library only
// drives the ioctl with the fd plus an input nvlist. No stream bytes are
// produced or parsed in Go.
//
// The input nvlist mirrors zfs_ioc_send_new(snapname, innvl, outnvl):
//
//	{
//	  "fd":           <int32 out.Fd()>,
//	  "fromsnap":     <string>,   // optional, incremental base
//	  "largeblockok": <bool pres>,// optional presence flags
//	  "embedok":      <bool pres>,
//	  "compressok":   <bool pres>,
//	  "rawok":        <bool pres>,
//	}
//
// snapname is carried in zc_name. out must be a regular file or pipe owned by
// this process; the kernel resolves the fd in the caller's fd table.
func (h *Handle) Send(snapshot string, out *os.File, opts SendOptions) error {
	if out == nil {
		return fmt.Errorf("Send: nil output file")
	}
	innvl := Nvlist{
		// fnvlist_lookup_int32(innvl, "fd") — must be DATA_TYPE_INT32.
		"fd": int32(out.Fd()),
	}
	if opts.FromSnap != "" {
		innvl["fromsnap"] = opts.FromSnap
	}
	// Presence-only flags: the kernel uses nvlist_exists(), so the value is
	// irrelevant — only the key's presence matters. We use Boolean{}
	// (DATA_TYPE_BOOLEAN, the bare-name form) to match libzfs_core exactly.
	if opts.LargeBlocks {
		innvl["largeblockok"] = Boolean{}
	}
	if opts.EmbedData {
		innvl["embedok"] = Boolean{}
	}
	if opts.Compress {
		innvl["compressok"] = Boolean{}
	}
	if opts.Raw {
		innvl["rawok"] = Boolean{}
	}

	// ZFS_IOC_SEND_NEW has no meaningful outnvl, but the new-style ioctl
	// path still expects a dst buffer to be available for any error nvlist;
	// we provide a modest one and ignore its contents.
	_, err := h.callNew(ZFS_IOC_SEND_NEW, snapshot, innvl)
	if err != nil {
		return fmt.Errorf("ZFS_IOC_SEND_NEW %q: %w", snapshot, err)
	}
	// Keep out alive until the ioctl returns: the kernel held the fd for the
	// duration, but make the dependency explicit for the race detector.
	runtime.KeepAlive(out)
	return nil
}

// Receive consumes a ZFS send stream from in and creates destSnap via
// ZFS_IOC_RECV_NEW (the libzfs_core lzc_receive path). It first reads the
// leading dmu_replay_record_t (the DRR_BEGIN record, 312 bytes) from the
// stream to populate the "begin_record" byte_array argument, then drives the
// ioctl with the input fd plus the input nvlist. The KERNEL reads the rest of
// the stream from the fd and applies the DMU records; this library does not
// parse the body.
//
// The input nvlist mirrors zfs_ioc_recv_new(fsname, innvl, outnvl):
//
//	{
//	  "snapname":     <string destSnap "pool/ds@snap">,
//	  "begin_record": <byte_array, the leading 312-byte dmu_replay_record_t>,
//	  "input_fd":     <int32 in.Fd()>,
//	  "origin":       <string>,    // optional
//	  "force":        <bool pres>, // optional presence flags
//	  "resumable":    <bool pres>,
//	}
//
// zc_name carries the destination filesystem (destSnap with the @snap
// stripped). On success the kernel returns read_bytes / error_flags / errors
// in outnvl; we surface read_bytes via the returned count.
func (h *Handle) Receive(destSnap string, in *os.File, opts RecvOptions) (BeginRecord, error) {
	var br BeginRecord
	if in == nil {
		return br, fmt.Errorf("Receive: nil input file")
	}

	// Read the leading dmu_replay_record_t verbatim. The kernel requires the
	// byte_array to be exactly sizeof(dmu_replay_record_t).
	rec := make([]byte, sizeofDmuReplayRecord)
	if _, err := readFull(in, rec); err != nil {
		return br, fmt.Errorf("Receive: read begin record: %w", err)
	}
	br = parseBeginRecord(rec)
	if br.Type == drrTypeBegin && br.Magic != dmuBackupMagic {
		// drr_type==DRR_BEGIN with a wrong magic means either a byte-swapped
		// (foreign-endian) stream or a non-stream input. The kernel would
		// reject the former on this arch; fail early with a clear message.
		return br, fmt.Errorf("Receive: bad stream magic %#x (want %#x); "+
			"foreign-endian or non-stream input", br.Magic, uint64(dmuBackupMagic))
	}

	innvl := Nvlist{
		"snapname":     destSnap,
		"begin_record": rec, // DATA_TYPE_BYTE_ARRAY, len == 312
		// fnvlist_lookup_int32(innvl, "input_fd").
		"input_fd": int32(in.Fd()),
	}
	if opts.Origin != "" {
		innvl["origin"] = opts.Origin
	}
	if opts.Force {
		innvl["force"] = Boolean{}
	}
	if opts.Resumable {
		innvl["resumable"] = Boolean{}
	}

	// zc_name is the destination filesystem (snapname with @snap removed).
	tofs := destSnap
	for i := 0; i < len(destSnap); i++ {
		if destSnap[i] == '@' {
			tofs = destSnap[:i]
			break
		}
	}

	out, err := h.callNewName(ZFS_IOC_RECV_NEW, tofs, innvl)
	runtime.KeepAlive(in)
	if err != nil {
		return br, fmt.Errorf("ZFS_IOC_RECV_NEW %q: %w", destSnap, err)
	}
	if out != nil {
		if ef, ok := out["error_flags"].(uint64); ok && ef != 0 {
			return br, fmt.Errorf("ZFS_IOC_RECV_NEW %q: error_flags=%#x", destSnap, ef)
		}
	}
	return br, nil
}

// readFull reads exactly len(p) bytes from r, looping over short reads (a pipe
// or socket may return fewer bytes than requested). It takes an io.Reader (the
// stream fd in production) so the short-read paths are unit-testable.
func readFull(r io.Reader, p []byte) (int, error) {
	n := 0
	for n < len(p) {
		m, err := r.Read(p[n:])
		n += m
		if err != nil {
			return n, err
		}
		if m == 0 {
			return n, fmt.Errorf("short read: got %d of %d bytes", n, len(p))
		}
	}
	return n, nil
}

// callNew issues a new-style ioctl (name + packed innvl in zc_nvlist_src,
// outnvl in zc_nvlist_dst) and returns the decoded outnvl. It is used for
// SEND_NEW, where the outnvl is empty on success.
func (h *Handle) callNew(req uintptr, name string, innvl Nvlist) (Nvlist, error) {
	return h.callNewName(req, name, innvl)
}

// callNewName is the workhorse for the new-style (lzc_*) ioctl ABI: zc_name =
// name, zc_nvlist_src = packed innvl, zc_nvlist_dst = a malloc'd buffer the
// kernel fills with outnvl (and sets zc_nvlist_dst_filled). Mirrors the
// userland lzc_ioctl() in lib/libzfs_core/libzfs_core.c, including the ENOMEM
// grow-and-retry loop.
func (h *Handle) callNewName(req uintptr, name string, innvl Nvlist) (Nvlist, error) {
	var srcBuf []byte
	if innvl != nil {
		var err error
		srcBuf, err = EncodeNative(innvl)
		if err != nil {
			return nil, fmt.Errorf("encode innvl: %w", err)
		}
	}
	dstSize := uint64(128 * 1024)
	if l := uint64(len(srcBuf)) * 2; l > dstSize {
		dstSize = l
	}
	for attempt := 0; attempt < 8; attempt++ {
		cmd := &zfsCmd{}
		if err := cmd.setName(name); err != nil {
			return nil, err
		}
		if srcBuf != nil {
			cmd.setU64(offZcNvlistSrc, uint64(uintptr(unsafe.Pointer(&srcBuf[0]))))
			cmd.setU64(offZcNvlistSrcSize, uint64(len(srcBuf)))
		}
		dst := make([]byte, dstSize)
		cmd.setU64(offZcNvlistDst, uint64(uintptr(unsafe.Pointer(&dst[0]))))
		cmd.setU64(offZcNvlistDstSize, dstSize)
		noteDst(dst)

		err := h.ioctl(req, cmd)
		runtime.KeepAlive(srcBuf)
		runtime.KeepAlive(dst)
		if err == unix.ENOMEM {
			dstSize *= 2
			if dstSize > 1<<30 {
				return nil, fmt.Errorf("outnvl buffer exceeded 1 GiB")
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if cmd.getU64(offZcNvlistDstFilled) != 0 {
			out, derr := DecodeNative(dst)
			if derr != nil {
				return nil, fmt.Errorf("decode outnvl: %w", derr)
			}
			return out, nil
		}
		return nil, nil
	}
	return nil, fmt.Errorf("outnvl buffer kept growing")
}
