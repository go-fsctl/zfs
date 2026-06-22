// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package main

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/go-fsctl/zfs"
)

var errBoom = errors.New("boom")

// fakeHandle satisfies the handle seam, driving each lifecycle step from a
// table. Any field set to a non-nil error makes that step fail; pcfgs controls
// what PoolConfigs returns.
type fakeHandle struct {
	pcfgs map[string]zfs.Nvlist

	poolCreateErr  error
	poolConfigErr  error
	poolPropsErr   error
	createFSErr    error
	setPropErr     error
	setPropFailN   int // fail the Nth SetProp call (1-based); 0 = use setPropErr
	setPropCalls   int
	getPropsErr    error
	renameErr      error
	snapshotErr    error
	destroyErr     error
	destroyFailN   int // fail the Nth Destroy call (1-based); 0 = use destroyErr
	destroyCalls   int
	exportErr      error
	importErr      error
	poolDestroyErr error
}

func (f *fakeHandle) Close() error                                  { return nil }
func (f *fakeHandle) PoolCreate(string, zfs.Vdev, zfs.Nvlist) error { return f.poolCreateErr }
func (f *fakeHandle) PoolConfigs() (map[string]zfs.Nvlist, error) {
	return f.pcfgs, f.poolConfigErr
}
func (f *fakeHandle) PoolGetProps(string) (map[string]zfs.Value, error) {
	return map[string]zfs.Value{"size": uint64(1), "health": "ONLINE"}, f.poolPropsErr
}
func (f *fakeHandle) CreateFilesystem(string) error { return f.createFSErr }
func (f *fakeHandle) SetProp(string, zfs.Nvlist) error {
	f.setPropCalls++
	if f.setPropFailN != 0 {
		if f.setPropCalls == f.setPropFailN {
			return errBoom
		}
		return nil
	}
	return f.setPropErr
}
func (f *fakeHandle) GetProps(string) (map[string]zfs.Value, error) {
	return map[string]zfs.Value{"atime": uint64(0), "quota": uint64(1)}, f.getPropsErr
}
func (f *fakeHandle) Rename(string, string, bool) error { return f.renameErr }
func (f *fakeHandle) Snapshot(string, []string) error   { return f.snapshotErr }
func (f *fakeHandle) Destroy(string, bool) error {
	f.destroyCalls++
	if f.destroyFailN != 0 {
		if f.destroyCalls == f.destroyFailN {
			return errBoom
		}
		return nil
	}
	return f.destroyErr
}
func (f *fakeHandle) PoolExport(string, bool, bool) error { return f.exportErr }
func (f *fakeHandle) PoolImport(string, zfs.Nvlist) (zfs.Nvlist, error) {
	return nil, f.importErr
}
func (f *fakeHandle) PoolDestroy(string) error { return f.poolDestroyErr }

// fakeBacking satisfies the backingFile seam.
type fakeBacking struct{ truncateErr error }

func (f *fakeBacking) Truncate(int64) error { return f.truncateErr }
func (f *fakeBacking) Close() error         { return nil }

// restore snapshots every seam and returns a deferred restore.
func restore() func() {
	a, b, c := openHandle, openFile, removeFile
	o, w := osExit, stdout
	return func() {
		openHandle, openFile, removeFile = a, b, c
		osExit, stdout = o, w
	}
}

// happy installs all-succeeding seams; individual tests then break one.
func happy() *fakeHandle {
	h := &fakeHandle{pcfgs: map[string]zfs.Nvlist{"gofsctl_probe": {"pool_guid": uint64(1)}}}
	openHandle = func() (handle, error) { return h, nil }
	openFile = func(string) (backingFile, error) { return &fakeBacking{}, nil }
	removeFile = func(string) error { return nil }
	return h
}

func runWith(args ...string) int {
	var buf bytes.Buffer
	stdout = &buf
	return run(args)
}

func TestRunSuccessDefaults(t *testing.T) {
	defer restore()()
	happy()
	if rc := runWith("zfsprobe"); rc != 0 {
		t.Fatalf("rc=%d, want 0", rc)
	}
}

func TestRunSuccessArgs(t *testing.T) {
	defer restore()()
	h := happy()
	h.pcfgs = map[string]zfs.Nvlist{"mypool": {"pool_guid": uint64(2)}}
	if rc := runWith("zfsprobe", "mypool", "/tmp/x.img"); rc != 0 {
		t.Fatalf("rc=%d, want 0", rc)
	}
}

func TestRunOpenFileError(t *testing.T) {
	defer restore()()
	happy()
	openFile = func(string) (backingFile, error) { return nil, errBoom }
	if rc := runWith("zfsprobe"); rc != 1 {
		t.Fatalf("rc=%d", rc)
	}
}

func TestRunTruncateError(t *testing.T) {
	defer restore()()
	happy()
	openFile = func(string) (backingFile, error) { return &fakeBacking{truncateErr: errBoom}, nil }
	if rc := runWith("zfsprobe"); rc != 1 {
		t.Fatalf("rc=%d", rc)
	}
}

func TestRunOpenHandleError(t *testing.T) {
	defer restore()()
	happy()
	openHandle = func() (handle, error) { return nil, errBoom }
	if rc := runWith("zfsprobe"); rc != 1 {
		t.Fatalf("rc=%d", rc)
	}
}

// stepErr tests each handle-method failure path in turn.
func TestRunStepErrors(t *testing.T) {
	steps := []struct {
		name string
		set  func(*fakeHandle)
	}{
		{"PoolCreate", func(h *fakeHandle) { h.poolCreateErr = errBoom }},
		{"PoolConfigs", func(h *fakeHandle) { h.poolConfigErr = errBoom }},
		{"PoolGetProps", func(h *fakeHandle) { h.poolPropsErr = errBoom }},
		{"CreateFilesystem", func(h *fakeHandle) { h.createFSErr = errBoom }},
		{"SetProp-atime", func(h *fakeHandle) { h.setPropFailN = 1 }},
		{"SetProp-quota", func(h *fakeHandle) { h.setPropFailN = 2 }},
		{"GetProps", func(h *fakeHandle) { h.getPropsErr = errBoom }},
		{"Rename", func(h *fakeHandle) { h.renameErr = errBoom }},
		{"Snapshot", func(h *fakeHandle) { h.snapshotErr = errBoom }},
		{"Destroy-snap", func(h *fakeHandle) { h.destroyFailN = 1 }},
		{"Destroy-ds", func(h *fakeHandle) { h.destroyFailN = 2 }},
		{"PoolExport", func(h *fakeHandle) { h.exportErr = errBoom }},
		{"PoolImport", func(h *fakeHandle) { h.importErr = errBoom }},
		{"PoolDestroy", func(h *fakeHandle) { h.poolDestroyErr = errBoom }},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			defer restore()()
			h := happy()
			s.set(h)
			if rc := runWith("zfsprobe"); rc != 1 {
				t.Fatalf("%s: rc=%d, want 1", s.name, rc)
			}
		})
	}
}

// TestRunPoolNotListed covers the "pool not in PoolConfigs" early return.
func TestRunPoolNotListed(t *testing.T) {
	defer restore()()
	h := happy()
	h.pcfgs = map[string]zfs.Nvlist{"other": {"pool_guid": uint64(9)}}
	if rc := runWith("zfsprobe"); rc != 1 {
		t.Fatalf("rc=%d, want 1", rc)
	}
}

// TestDefaultSeams exercises the real openFile/removeFile seam closures with an
// ordinary temp file (no /dev/zfs needed). openHandle's default closure is left
// to the integration environment; here we only confirm it is wired.
func TestDefaultSeams(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/probe.img"
	f, err := openFile(path)
	if err != nil {
		t.Fatalf("openFile: %v", err)
	}
	if err := f.Truncate(0); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	f.Close()
	if err := removeFile(path); err != nil {
		t.Fatalf("removeFile: %v", err)
	}
	// openHandle default closure: with no /dev/zfs it returns an error, which is
	// fine — we only need the closure body to execute.
	if _, err := openHandle(); err == nil {
		t.Log("openHandle succeeded (running inside a ZFS guest)")
	}
}

// TestMainInvokesRun drives the thin main() wrapper through the osExit seam.
func TestMainInvokesRun(t *testing.T) {
	defer restore()()
	happy()
	// main() reads os.Args directly; pin it to the no-arg default so the pool
	// name matches the fake PoolConfigs map.
	savedArgs := os.Args
	os.Args = []string{"zfsprobe"}
	defer func() { os.Args = savedArgs }()
	var buf bytes.Buffer
	stdout = &buf
	code := -1
	osExit = func(c int) { code = c }
	main()
	if code != 0 {
		t.Fatalf("main exit=%d, want 0", code)
	}
}
