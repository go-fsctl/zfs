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

// CreateEncrypted is unsupported off Linux.
func (h *Handle) CreateEncrypted(name string, key []byte, props Nvlist) error {
	return ErrUnsupported
}

// LoadKey is unsupported off Linux.
func (h *Handle) LoadKey(fs string, key []byte, nomount bool) error { return ErrUnsupported }

// UnloadKey is unsupported off Linux.
func (h *Handle) UnloadKey(fs string) error { return ErrUnsupported }

// ChangeKey is unsupported off Linux.
func (h *Handle) ChangeKey(fs string, newKey []byte, props Nvlist) error { return ErrUnsupported }

// Promote is unsupported off Linux.
func (h *Handle) Promote(cloneFs string) error { return ErrUnsupported }

// Inherit is unsupported off Linux.
func (h *Handle) Inherit(fs, prop string, received bool) error { return ErrUnsupported }

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

// ScanPool is unsupported off Linux.
func (h *Handle) ScanPool(pool string, fn ScanFunc, cmd ScanCmd) error { return ErrUnsupported }

// ScrubStart is unsupported off Linux.
func (h *Handle) ScrubStart(pool string) error { return ErrUnsupported }

// ScrubStop is unsupported off Linux.
func (h *Handle) ScrubStop(pool string) error { return ErrUnsupported }

// ScrubPause is unsupported off Linux.
func (h *Handle) ScrubPause(pool string) error { return ErrUnsupported }

// ResilverStart is unsupported off Linux.
func (h *Handle) ResilverStart(pool string) error { return ErrUnsupported }

// ScanStatus is unsupported off Linux.
func (h *Handle) ScanStatus(pool string) (ScanStatus, error) { return ScanStatus{}, ErrUnsupported }

// TrimPool is unsupported off Linux.
func (h *Handle) TrimPool(pool string, vdevs []string, rate uint64, secure bool, cmd uint64) error {
	return ErrUnsupported
}

// InitializePool is unsupported off Linux.
func (h *Handle) InitializePool(pool string, vdevs []string, cmd uint64) error {
	return ErrUnsupported
}

// VdevAttach is unsupported off Linux.
func (h *Handle) VdevAttach(pool, existingVdev, newVdev string, replace bool) error {
	return ErrUnsupported
}

// VdevDetach is unsupported off Linux.
func (h *Handle) VdevDetach(pool, vdev string) error { return ErrUnsupported }

// VdevSetState is unsupported off Linux.
func (h *Handle) VdevSetState(pool, vdev string, newState uint64, flags uint64) (uint64, error) {
	return 0, ErrUnsupported
}

// VdevOnline is unsupported off Linux.
func (h *Handle) VdevOnline(pool, vdev string) (uint64, error) { return 0, ErrUnsupported }

// VdevOffline is unsupported off Linux.
func (h *Handle) VdevOffline(pool, vdev string) (uint64, error) { return 0, ErrUnsupported }

// VdevReopen is unsupported off Linux.
func (h *Handle) VdevReopen(pool string, scrubRestart bool) error { return ErrUnsupported }

// ChannelProgram is unsupported off Linux.
func (h *Handle) ChannelProgram(pool, script string, opts ChannelProgramOptions) (Nvlist, error) {
	return nil, ErrUnsupported
}

// ListSnapshotsZCP is unsupported off Linux.
func (h *Handle) ListSnapshotsZCP(fs string) ([]string, error) { return nil, ErrUnsupported }

// Diff is unsupported off Linux.
func (h *Handle) Diff(fromSnap, toSnapOrFs string) ([]DiffEntry, error) { return nil, ErrUnsupported }

// UserSpace is unsupported off Linux.
func (h *Handle) UserSpace(fs string, prop SpaceProp) ([]SpaceEntry, error) {
	return nil, ErrUnsupported
}

// UserSpaceByID is unsupported off Linux.
func (h *Handle) UserSpaceByID(fs string, prop SpaceProp, id uint32) (uint64, bool, error) {
	return 0, false, ErrUnsupported
}

// SetUserQuota is unsupported off Linux.
func (h *Handle) SetUserQuota(fs string, prop SpaceProp, who string, quota uint64) error {
	return ErrUnsupported
}

// Available reports false off Linux.
func Available() bool { return false }
