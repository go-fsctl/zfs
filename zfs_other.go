// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build !linux

package zfs

import "errors"

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

// Available reports false off Linux.
func Available() bool { return false }
