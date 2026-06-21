// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build !linux

package zfs

import (
	"errors"
	"os"
)

// ErrUnsupported is returned by all kernel operations on non-Linux platforms.
// The native nvlist codec (EncodeNative/DecodeNative) remains available
// everywhere for testing and tooling.
var ErrUnsupported = errors.New("zfs: /dev/zfs ioctls are only supported on Linux")

// Handle is a stub on non-Linux platforms.
type Handle struct{}

// Open always fails off Linux.
func Open() (*Handle, error) { return nil, ErrUnsupported }

// Close is a no-op stub.
func (h *Handle) Close() error { return nil }

// PoolConfigs is unsupported off Linux.
func (h *Handle) PoolConfigs() (map[string]Nvlist, error) { return nil, ErrUnsupported }

// PoolNames is unsupported off Linux.
func (h *Handle) PoolNames() ([]string, error) { return nil, ErrUnsupported }

// Snapshot is unsupported off Linux.
func (h *Handle) Snapshot(pool string, fullnames []string) error { return ErrUnsupported }

// CreateFilesystem is unsupported off Linux.
func (h *Handle) CreateFilesystem(name string) error { return ErrUnsupported }

// ObjsetStats is unsupported off Linux.
func (h *Handle) ObjsetStats(name string) (Nvlist, error) { return nil, ErrUnsupported }

// PoolCreate is unsupported off Linux.
func (h *Handle) PoolCreate(name string, root Vdev, props Nvlist) error { return ErrUnsupported }

// PoolDestroy is unsupported off Linux.
func (h *Handle) PoolDestroy(name string) error { return ErrUnsupported }

// PoolExport is unsupported off Linux.
func (h *Handle) PoolExport(name string, force, hardforce bool) error { return ErrUnsupported }

// PoolTryImport is unsupported off Linux.
func (h *Handle) PoolTryImport(tryconfig Nvlist) (Nvlist, error) { return nil, ErrUnsupported }

// PoolImport is unsupported off Linux.
func (h *Handle) PoolImport(name string, config Nvlist) (Nvlist, error) { return nil, ErrUnsupported }

// Destroy is unsupported off Linux.
func (h *Handle) Destroy(name string, defer_ bool) error { return ErrUnsupported }

// Rename is unsupported off Linux.
func (h *Handle) Rename(old, newName string, recursive bool) error { return ErrUnsupported }

// SetProp is unsupported off Linux.
func (h *Handle) SetProp(name string, props Nvlist) error { return ErrUnsupported }

// GetProps is unsupported off Linux.
func (h *Handle) GetProps(name string) (map[string]Value, error) { return nil, ErrUnsupported }

// PoolGetProps is unsupported off Linux.
func (h *Handle) PoolGetProps(name string) (map[string]Value, error) { return nil, ErrUnsupported }

// Send is unsupported off Linux.
func (h *Handle) Send(snapshot string, out *os.File, opts SendOptions) error {
	return ErrUnsupported
}

// Receive is unsupported off Linux.
func (h *Handle) Receive(destSnap string, in *os.File, opts RecvOptions) (BeginRecord, error) {
	return BeginRecord{}, ErrUnsupported
}

// Clone is unsupported off Linux.
func (h *Handle) Clone(snapshot, newFs string, props Nvlist) error { return ErrUnsupported }

// Rollback is unsupported off Linux.
func (h *Handle) Rollback(fs string) (string, error) { return "", ErrUnsupported }

// RollbackTo is unsupported off Linux.
func (h *Handle) RollbackTo(fs, target string) (string, error) { return "", ErrUnsupported }

// Hold is unsupported off Linux.
func (h *Handle) Hold(snapshot, tag string, recursive bool) error { return ErrUnsupported }

// Release is unsupported off Linux.
func (h *Handle) Release(snapshot, tag string) error { return ErrUnsupported }

// Holds is unsupported off Linux.
func (h *Handle) Holds(snapshot string) (map[string]uint64, error) { return nil, ErrUnsupported }

// Bookmark is unsupported off Linux.
func (h *Handle) Bookmark(snapshot, bookmark string) error { return ErrUnsupported }

// GetBookmarks is unsupported off Linux.
func (h *Handle) GetBookmarks(fs string) (map[string]Nvlist, error) { return nil, ErrUnsupported }

// DestroyBookmarks is unsupported off Linux.
func (h *Handle) DestroyBookmarks(bookmarks ...string) error { return ErrUnsupported }

// Available reports false off Linux.
func Available() bool { return false }
