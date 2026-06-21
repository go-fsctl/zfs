// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// hostBO is the native byte order used for the zfs_cmd_t scalar fields (the
// kernel reads them in host-endian).
var hostBO = func() binary.ByteOrder {
	bo, _ := nvHostOrder()
	return bo
}()

// zfsCmd is the in-memory zfs_cmd_t, held as a raw byte buffer sized to
// sizeof(zfs_cmd_t) for OpenZFS 2.2.2 (13744 bytes). Scalar fields are read
// and written at the exact offsets captured from the C ABI probe. Using a
// flat buffer (rather than a Go struct) sidesteps any divergence between Go's
// struct layout and the C ABI for the large alignment-sensitive trailing
// members (zc_share, zc_objset_stats, zc_begin_record, zc_inject_record,
// zc_stat).
type zfsCmd struct {
	buf [sizeofZfsCmd]byte
}

func (c *zfsCmd) setU64(off int, v uint64) {
	hostBO.PutUint64(c.buf[off:off+8], v)
}

func (c *zfsCmd) getU64(off int) uint64 {
	return hostBO.Uint64(c.buf[off : off+8])
}

// setName writes a NUL-terminated string into the zc_name[MAXPATHLEN] field.
func (c *zfsCmd) setName(s string) error {
	if len(s)+1 > maxPathLen {
		return fmt.Errorf("name too long: %d bytes", len(s))
	}
	copy(c.buf[offZcName:offZcName+maxPathLen], make([]byte, maxPathLen))
	copy(c.buf[offZcName:], s)
	return nil
}

// setValue writes into the zc_value[MAXPATHLEN*2] field.
func (c *zfsCmd) setValue(s string) error {
	if len(s)+1 > maxPathLen*2 {
		return fmt.Errorf("value too long: %d bytes", len(s))
	}
	copy(c.buf[offZcValue:], s)
	return nil
}

// Handle is an open file descriptor to /dev/zfs.
type Handle struct {
	f *os.File
}

// Open opens /dev/zfs. Requires CAP_SYS_ADMIN (effectively root) for most ops.
func Open() (*Handle, error) {
	f, err := os.OpenFile("/dev/zfs", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/zfs: %w", err)
	}
	return &Handle{f: f}, nil
}

// Close releases the /dev/zfs handle.
func (h *Handle) Close() error { return h.f.Close() }

// ioctl issues a single ZFS ioctl. cmd is mutated in place (the kernel writes
// back zc_nvlist_dst_size etc.). The caller is responsible for src/dst pinning
// via the helpers below.
func (h *Handle) ioctl(req uintptr, cmd *zfsCmd) error {
	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		h.f.Fd(),
		req,
		uintptr(unsafe.Pointer(&cmd.buf[0])),
	)
	runtime.KeepAlive(cmd)
	if errno != 0 {
		return errno
	}
	return nil
}

// callWithDst runs an ioctl that returns an nvlist in zc_nvlist_dst. It
// allocates a dst buffer of dstSize, retries once with the kernel-reported
// size if the buffer was too small (ENOMEM), and decodes the result.
func (h *Handle) callWithDst(req uintptr, build func(*zfsCmd) error, dstSize uint64) (Nvlist, error) {
	for attempt := 0; attempt < 2; attempt++ {
		cmd := &zfsCmd{}
		if err := build(cmd); err != nil {
			return nil, err
		}
		dst := make([]byte, dstSize)
		cmd.setU64(offZcNvlistDst, uint64(uintptr(unsafe.Pointer(&dst[0]))))
		cmd.setU64(offZcNvlistDstSize, dstSize)

		err := h.ioctl(req, cmd)
		runtime.KeepAlive(dst)
		if err == unix.ENOMEM {
			// Kernel wrote the required size into zc_nvlist_dst_size.
			need := cmd.getU64(offZcNvlistDstSize)
			if need > dstSize && need < 1<<30 {
				dstSize = need
				continue
			}
		}
		if err != nil {
			return nil, err
		}
		// The kernel may report the actual packed size; decode the whole
		// buffer (DecodeNative stops at the list terminator regardless).
		return DecodeNative(dst)
	}
	return nil, fmt.Errorf("ioctl dst buffer kept growing")
}

// withSrc packs nv into a NV_ENCODE_NATIVE buffer and wires it into
// zc_nvlist_src/_size, returning a function that keeps it alive.
func (c *zfsCmd) setSrc(nv Nvlist) (keepalive []byte, err error) {
	if nv == nil {
		return nil, nil
	}
	b, err := EncodeNative(nv)
	if err != nil {
		return nil, err
	}
	c.setU64(offZcNvlistSrc, uint64(uintptr(unsafe.Pointer(&b[0]))))
	c.setU64(offZcNvlistSrcSize, uint64(len(b)))
	return b, nil
}

// setConf packs nv into a NV_ENCODE_NATIVE buffer and wires it into
// zc_nvlist_conf/_size, returning a buffer the caller must keep alive across
// the ioctl. The pool create/import handlers read the pool *configuration*
// (and vdev tree) from zc_nvlist_conf, distinct from the props in
// zc_nvlist_src.
func (c *zfsCmd) setConf(nv Nvlist) (keepalive []byte, err error) {
	if nv == nil {
		return nil, nil
	}
	b, err := EncodeNative(nv)
	if err != nil {
		return nil, err
	}
	c.setU64(offZcNvlistConf, uint64(uintptr(unsafe.Pointer(&b[0]))))
	c.setU64(offZcNvlistConfSize, uint64(len(b)))
	return b, nil
}
