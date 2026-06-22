// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// This file drives every branch of the zfs *_linux.go kernel paths through the
// ioctlFn / osOpenFile / unixAccess seams in seams_linux.go, fault-injecting
// both success and each errno WITHOUT a real /dev/zfs, a zfs module, or root.
// The kernel's buffer write-back (the decoded dst nvlist, zc_name for the
// dataset iterator, zc_string for PROMOTE, zc_cookie) is emulated by the fake
// directly mutating the supplied *zfsCmd. The root-only
// integration_linux_test.go exercises the genuine ioctls end to end and skips
// when /dev/zfs is absent.

var errInjected = errors.New("injected")

// lastDst is the most-recent zc_nvlist_dst buffer captured via the dstHook
// seam. A fake ioctlFn writes the emulated kernel nvlist into it (no unsafe
// pointer round-trip needed; dstHook hands us the real slice the caller will
// decode). Tests run serially so a package var is safe.
var lastDst []byte

// snapshotSeams captures the production seam values and returns a restore func.
// It also installs the dstHook capture for the duration of the test.
func snapshotSeams() func() {
	a, b, c, d, e := osOpenFile, unixAccess, ioctlFn, dstHook, encodeNative
	dstHook = func(dst []byte) { lastDst = dst }
	return func() {
		osOpenFile, unixAccess, ioctlFn, dstHook, encodeNative = a, b, c, d, e
		lastDst = nil
	}
}

// putDst encodes nv and writes it into the caller's captured dst buffer,
// marking zc_nvlist_dst_filled so the decode path runs. Fails the test if the
// buffer is too small (the test should size it adequately) or none was captured.
func putDst(t *testing.T, cmd *zfsCmd, nv Nvlist) {
	t.Helper()
	b, err := EncodeNative(nv)
	if err != nil {
		t.Fatalf("putDst encode: %v", err)
	}
	if lastDst == nil {
		t.Fatal("putDst: no dst buffer captured (dstHook not invoked)")
	}
	if len(b) > len(lastDst) {
		t.Fatalf("putDst: encoded %d bytes > dst %d", len(b), len(lastDst))
	}
	copy(lastDst, b)
	cmd.setU64(offZcNvlistDstFilled, 1)
}

// okHandle is a Handle usable with a fake ioctlFn (h.f is never touched because
// the fake replaces realIoctl).
func okHandle() *Handle { return &Handle{} }

// ---- Open / Close / Available ----

func TestOpenAndClose(t *testing.T) {
	defer snapshotSeams()()

	osOpenFile = func(string, int, os.FileMode) (*os.File, error) { return nil, errInjected }
	if _, err := Open(); err == nil {
		t.Fatal("want open error")
	}

	// Success: hand back a real temp file so Close() works.
	f, err := os.CreateTemp(t.TempDir(), "zfs")
	if err != nil {
		t.Fatal(err)
	}
	osOpenFile = func(string, int, os.FileMode) (*os.File, error) { return f, nil }
	h, err := Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestAvailable(t *testing.T) {
	defer snapshotSeams()()
	unixAccess = func(string, uint32) error { return nil }
	if !Available() {
		t.Fatal("want available")
	}
	unixAccess = func(string, uint32) error { return errInjected }
	if Available() {
		t.Fatal("want unavailable")
	}
}

// TestRealIoctlSeam covers realIoctl's success branch with a benign ioctl on a
// regular file fd (FIONREAD succeeds non-root on a normal file) and its errno
// branch with a bogus request. This is the production ioctlFn closure.
func TestRealIoctlSeam(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "fd")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := &Handle{f: f}

	// Success: FIGETBSZ (request 0x2) on a regular file returns the block size
	// with errno 0 non-root, exercising realIoctl's success branch. (The whole
	// zfsCmd buffer is the ioctl argument; only its first int is written.)
	// Skipped under -test.short because QEMU user-mode emulation returns ENOTTY
	// for FIGETBSZ; the native CI job (no -short) covers this branch.
	if !testing.Short() {
		cmd := &zfsCmd{}
		if err := realIoctl(h, 0x2 /* FIGETBSZ */, cmd); err != nil {
			t.Fatalf("realIoctl FIGETBSZ: %v", err)
		}
	}

	// Error: a bogus request number yields an errno.
	if err := realIoctl(h, 0xDEAD, &zfsCmd{}); err == nil {
		t.Fatal("want errno from bogus ioctl request")
	}
}

// ---- zfsCmd buffer helpers ----

func TestSetNameValueErrors(t *testing.T) {
	c := &zfsCmd{}
	if err := c.setName(string(make([]byte, maxPathLen))); err == nil {
		t.Fatal("want name-too-long error")
	}
	if err := c.setName("tank/ds"); err != nil {
		t.Fatalf("setName: %v", err)
	}
	if err := c.setValue(string(make([]byte, maxPathLen*2))); err == nil {
		t.Fatal("want value-too-long error")
	}
	if err := c.setValue("newname"); err != nil {
		t.Fatalf("setValue: %v", err)
	}
}

func TestSetSrcConfNil(t *testing.T) {
	c := &zfsCmd{}
	if ka, err := c.setSrc(nil); ka != nil || err != nil {
		t.Fatalf("setSrc(nil) = %v, %v", ka, err)
	}
	if ka, err := c.setConf(nil); ka != nil || err != nil {
		t.Fatalf("setConf(nil) = %v, %v", ka, err)
	}
	if ka, err := c.setSrc(Nvlist{"a": uint64(1)}); ka == nil || err != nil {
		t.Fatalf("setSrc: %v, %v", ka, err)
	}
	if ka, err := c.setConf(Nvlist{"b": uint64(2)}); ka == nil || err != nil {
		t.Fatalf("setConf: %v, %v", ka, err)
	}
}

// ---- callWithDst (PoolConfigs / PoolNames / ObjsetStats / PoolGetProps) ----

func TestPoolConfigsAndNames(t *testing.T) {
	defer snapshotSeams()()

	// build error path: setName too long is exercised elsewhere; here force the
	// ioctl error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().PoolConfigs(); err == nil {
		t.Fatal("want ioctl error")
	}
	if _, err := okHandle().PoolNames(); err == nil {
		t.Fatal("want PoolNames error")
	}

	// Success: kernel returns {pool: <config nvlist>, junk: <scalar>}.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{
			"tank": Nvlist{"pool_guid": uint64(0x1234)},
			"junk": uint64(7), // non-nvlist value: skipped by the filter
		})
		return nil
	}
	cfgs, err := okHandle().PoolConfigs()
	if err != nil {
		t.Fatalf("PoolConfigs: %v", err)
	}
	if _, ok := cfgs["tank"]; !ok {
		t.Fatalf("missing tank config: %v", cfgs)
	}
	if _, ok := cfgs["junk"]; ok {
		t.Fatal("scalar should have been filtered out")
	}
	names, err := okHandle().PoolNames()
	if err != nil || len(names) != 1 || names[0] != "tank" {
		t.Fatalf("PoolNames = %v, %v", names, err)
	}
}

func TestCallWithDstEnomemGrowThenSucceed(t *testing.T) {
	defer snapshotSeams()()
	calls := 0
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		calls++
		if calls == 1 {
			// Report a needed size larger than current to trigger one retry.
			cmd.setU64(offZcNvlistDstSize, 128*1024)
			return unix.ENOMEM
		}
		putDst(t, cmd, Nvlist{"a": uint64(1)})
		return nil
	}
	if _, err := okHandle().ObjsetStats("tank/ds"); err != nil {
		t.Fatalf("ObjsetStats: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (grow then succeed)", calls)
	}
}

func TestCallWithDstEnomemNoGrowReturnsError(t *testing.T) {
	defer snapshotSeams()()
	// ENOMEM but the reported need is NOT larger than dstSize: the loop falls
	// through and returns the ENOMEM error.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		cmd.setU64(offZcNvlistDstSize, 0) // need == 0, not > dstSize
		return unix.ENOMEM
	}
	if _, err := okHandle().ObjsetStats("tank/ds"); err == nil {
		t.Fatal("want ENOMEM surfaced")
	}
}

func TestCallWithDstBuildError(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	// A name longer than MAXPATHLEN makes the build callback (setName) fail.
	long := string(make([]byte, maxPathLen))
	if _, err := okHandle().ObjsetStats(long); err == nil {
		t.Fatal("want build (setName) error")
	}
	if _, err := okHandle().PoolGetProps(long); err == nil {
		t.Fatal("want PoolGetProps build error")
	}
}

func TestGetPropsAndPoolGetProps(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{
			"compression": Nvlist{"value": "lz4", "source": "local"},
		})
		return nil
	}
	props, err := okHandle().GetProps("tank/ds")
	if err != nil || props["compression"] != "lz4" {
		t.Fatalf("GetProps = %v, %v", props, err)
	}
	pp, err := okHandle().PoolGetProps("tank")
	if err != nil || pp["compression"] != "lz4" {
		t.Fatalf("PoolGetProps = %v, %v", pp, err)
	}

	// Error propagation from the underlying ioctl.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().GetProps("tank/ds"); err == nil {
		t.Fatal("want GetProps error")
	}
	if _, err := okHandle().PoolGetProps("tank"); err == nil {
		t.Fatal("want PoolGetProps error")
	}
}

func TestObjsetStatsError(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().ObjsetStats("tank/ds"); err == nil {
		t.Fatal("want error")
	}
}

// ---- Snapshot / Create / CreateFilesystem ----

func TestSnapshot(t *testing.T) {
	defer snapshotSeams()()
	if err := okHandle().Snapshot("tank", nil); err == nil {
		t.Fatal("want no-names error")
	}
	// setName error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().Snapshot(string(make([]byte, maxPathLen)), []string{"tank@s"}); err == nil {
		t.Fatal("want setName error")
	}
	// ioctl error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().Snapshot("tank", []string{"tank@s1"}); err == nil {
		t.Fatal("want ioctl error")
	}
	// success.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().Snapshot("tank", []string{"tank@s1"}); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
}

func TestCreateFilesystemAndCreate(t *testing.T) {
	defer snapshotSeams()()
	// setName error via createWithKey.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().CreateFilesystem(string(make([]byte, maxPathLen))); err == nil {
		t.Fatal("want setName error")
	}
	// ioctl error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().CreateFilesystem("tank/ds"); err == nil {
		t.Fatal("want ioctl error")
	}
	// success with props + key (covers all createWithKey branches).
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().create("tank/ds", DMU_OST_ZFS, Nvlist{"atime": uint64(0)}); err != nil {
		t.Fatalf("create with props: %v", err)
	}
	if err := okHandle().createWithKey("tank/ds", DMU_OST_ZFS, nil, rawKey); err != nil {
		t.Fatalf("createWithKey: %v", err)
	}
}

// ---- dataset_linux: Destroy / Rename / SetProp ----

func TestDestroy(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().Destroy(string(make([]byte, maxPathLen)), false); err == nil {
		t.Fatal("want setName error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().Destroy("tank@s", true); err == nil {
		t.Fatal("want ioctl error")
	}
	// success, defer_ true path sets zc_defer_destroy.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		if hostBO.Uint32(cmd.buf[offZcDeferDestroy:offZcDeferDestroy+4]) != 1 {
			t.Error("defer flag not set")
		}
		return nil
	}
	if err := okHandle().Destroy("tank@s", true); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	// defer_ false branch.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().Destroy("tank/ds", false); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}

func TestRename(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().Rename(string(make([]byte, maxPathLen)), "b", false); err == nil {
		t.Fatal("want setName error")
	}
	if err := okHandle().Rename("a", string(make([]byte, maxPathLen*2)), false); err == nil {
		t.Fatal("want setValue error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().Rename("a", "b", true); err == nil {
		t.Fatal("want ioctl error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().Rename("a", "b", true); err != nil {
		t.Fatalf("Rename: %v", err)
	}
}

func TestSetProp(t *testing.T) {
	defer snapshotSeams()()
	if err := okHandle().SetProp("tank/ds", nil); err == nil {
		t.Fatal("want no-props error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().SetProp(string(make([]byte, maxPathLen)), Nvlist{"a": uint64(1)}); err == nil {
		t.Fatal("want setName error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().SetProp("tank/ds", Nvlist{"a": uint64(1)}); err == nil {
		t.Fatal("want ioctl error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().SetProp("tank/ds", Nvlist{"compression": "lz4"}); err != nil {
		t.Fatalf("SetProp: %v", err)
	}
}

// ---- pool_linux: PoolCreate / Destroy / Export / TryImport / Import ----

func TestPoolCreate(t *testing.T) {
	defer snapshotSeams()()
	// wrong root type.
	if err := okHandle().PoolCreate("tank", Vdev{Type: VDEV_TYPE_FILE, Path: "/x"}, nil); err == nil {
		t.Fatal("want wrong-root-type error")
	}
	// root.nvlist() error: root with no children.
	if err := okHandle().PoolCreate("tank", Vdev{Type: VDEV_TYPE_ROOT}, nil); err == nil {
		t.Fatal("want vdev nvlist error")
	}
	good := Vdev{Type: VDEV_TYPE_ROOT, Children: []Vdev{{Type: VDEV_TYPE_FILE, Path: "/disk0"}}}
	// setName error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().PoolCreate(string(make([]byte, maxPathLen)), good, nil); err == nil {
		t.Fatal("want setName error")
	}
	// ioctl error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().PoolCreate("tank", good, nil); err == nil {
		t.Fatal("want ioctl error")
	}
	// success with props.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().PoolCreate("tank", good, Nvlist{"ashift": uint64(12)}); err != nil {
		t.Fatalf("PoolCreate: %v", err)
	}
}

func TestPoolDestroyExport(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().PoolDestroy(string(make([]byte, maxPathLen))); err == nil {
		t.Fatal("want setName error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().PoolDestroy("tank"); err == nil {
		t.Fatal("want ioctl error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().PoolDestroy("tank"); err != nil {
		t.Fatalf("PoolDestroy: %v", err)
	}

	// Export: setName error, ioctl error, success with force+hardforce.
	if err := okHandle().PoolExport(string(make([]byte, maxPathLen)), false, false); err == nil {
		t.Fatal("want setName error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().PoolExport("tank", true, true); err == nil {
		t.Fatal("want ioctl error")
	}
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		if cmd.getU64(offZcCookie) != 1 || cmd.getU64(offZcGuid) != 1 {
			t.Error("force/hardforce flags not set")
		}
		return nil
	}
	if err := okHandle().PoolExport("tank", true, true); err != nil {
		t.Fatalf("PoolExport: %v", err)
	}
}

func TestPoolTryImport(t *testing.T) {
	defer snapshotSeams()()
	if _, err := okHandle().PoolTryImport(nil); err == nil {
		t.Fatal("want nil-tryconfig error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().PoolTryImport(Nvlist{"name": "tank"}); err == nil {
		t.Fatal("want ioctl error")
	}
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{"name": "tank"})
		return nil
	}
	nv, err := okHandle().PoolTryImport(Nvlist{"name": "tank"})
	if err != nil || nv["name"] != "tank" {
		t.Fatalf("PoolTryImport = %v, %v", nv, err)
	}
}

func TestPoolImport(t *testing.T) {
	defer snapshotSeams()()
	// missing pool_guid.
	if _, err := okHandle().PoolImport("tank", Nvlist{}); err == nil {
		t.Fatal("want missing-guid error")
	}
	cfg := Nvlist{ZPOOL_CONFIG_POOL_GUID: uint64(0x1234)}
	// build (setName) error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if _, err := okHandle().PoolImport(string(make([]byte, maxPathLen)), cfg); err == nil {
		t.Fatal("want setName error")
	}
	// ioctl error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().PoolImport("tank", cfg); err == nil {
		t.Fatal("want ioctl error")
	}
	// success.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		if cmd.getU64(offZcGuid) != 0x1234 {
			t.Error("guid not set in zc_guid")
		}
		putDst(t, cmd, Nvlist{"name": "tank"})
		return nil
	}
	nv, err := okHandle().PoolImport("tank", cfg)
	if err != nil || nv["name"] != "tank" {
		t.Fatalf("PoolImport = %v, %v", nv, err)
	}
}

func TestCallWithDstConfGrowAndBudget(t *testing.T) {
	defer snapshotSeams()()
	cfg := Nvlist{ZPOOL_CONFIG_POOL_GUID: uint64(1)}
	// ENOMEM grow-then-succeed.
	calls := 0
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		calls++
		if calls == 1 {
			cmd.setU64(offZcNvlistDstSize, 512*1024)
			return unix.ENOMEM
		}
		putDst(t, cmd, Nvlist{"ok": uint64(1)})
		return nil
	}
	if _, err := okHandle().PoolImport("tank", cfg); err != nil {
		t.Fatalf("grow path: %v", err)
	}
	// ENOMEM with no growth -> surfaced.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		cmd.setU64(offZcNvlistDstSize, 0)
		return unix.ENOMEM
	}
	if _, err := okHandle().PoolImport("tank", cfg); err == nil {
		t.Fatal("want ENOMEM surfaced")
	}
}

// ---- encryption_linux: LoadKey / UnloadKey / ChangeKey / CreateEncrypted /
//      Promote / Inherit ----

func TestLoadKey(t *testing.T) {
	defer snapshotSeams()()
	if err := okHandle().LoadKey("fs", nil, false); err == nil {
		t.Fatal("want empty-key error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().LoadKey("fs", rawKey, true); err == nil {
		t.Fatal("want ioctl error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().LoadKey("fs", rawKey, true); err != nil {
		t.Fatalf("LoadKey nomount: %v", err)
	}
	if err := okHandle().LoadKey("fs", rawKey, false); err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
}

func TestUnloadKey(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().UnloadKey("fs"); err == nil {
		t.Fatal("want ioctl error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().UnloadKey("fs"); err != nil {
		t.Fatalf("UnloadKey: %v", err)
	}
}

func TestChangeKey(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().ChangeKey("fs", rawKey, Nvlist{"keyformat": uint64(1)}); err == nil {
		t.Fatal("want ioctl error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().ChangeKey("fs", rawKey, Nvlist{"keyformat": uint64(1)}); err != nil {
		t.Fatalf("ChangeKey: %v", err)
	}
	// no newKey, no props branch.
	if err := okHandle().ChangeKey("fs", nil, nil); err != nil {
		t.Fatalf("ChangeKey no-key: %v", err)
	}
}

func TestCreateEncrypted(t *testing.T) {
	defer snapshotSeams()()
	if err := okHandle().CreateEncrypted("fs", nil, Nvlist{"a": uint64(1)}); err == nil {
		t.Fatal("want empty-key error")
	}
	if err := okHandle().CreateEncrypted("fs", rawKey, nil); err == nil {
		t.Fatal("want empty-props error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	props := Nvlist{"encryption": uint64(ZIO_CRYPT_AES_256_GCM), "keyformat": uint64(ZFS_KEYFORMAT_RAW)}
	if err := okHandle().CreateEncrypted("tank/enc", rawKey, props); err != nil {
		t.Fatalf("CreateEncrypted: %v", err)
	}
}

func TestPromote(t *testing.T) {
	defer snapshotSeams()()
	if err := okHandle().Promote("tank/ds@snap"); err == nil {
		t.Fatal("want not-a-filesystem error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().Promote(string(make([]byte, maxPathLen))); err == nil {
		t.Fatal("want setName error")
	}
	// ioctl error with NO conflict string.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().Promote("tank/clone"); err == nil {
		t.Fatal("want ioctl error")
	}
	// ioctl error WITH a conflicting snapshot name written into zc_string.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		copy(cmd.buf[offZcString:offZcString+maxNameLen], "s1\x00")
		return errInjected
	}
	if err := okHandle().Promote("tank/clone"); err == nil {
		t.Fatal("want conflict error")
	}
	// success.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().Promote("tank/clone"); err != nil {
		t.Fatalf("Promote: %v", err)
	}
}

func TestInherit(t *testing.T) {
	defer snapshotSeams()()
	if err := okHandle().Inherit("fs", "", false); err == nil {
		t.Fatal("want empty-prop error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().Inherit(string(make([]byte, maxPathLen)), "atime", false); err == nil {
		t.Fatal("want setName error")
	}
	if err := okHandle().Inherit("fs", string(make([]byte, maxPathLen*2)), false); err == nil {
		t.Fatal("want setValue error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().Inherit("fs", "atime", true); err == nil {
		t.Fatal("want ioctl error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().Inherit("fs", "atime", true); err != nil {
		t.Fatalf("Inherit: %v", err)
	}
}

// ---- lifecycle_linux: Clone / Rollback / Hold / Release / Holds / Bookmark /
//      GetBookmarks / DestroyBookmarks / descendantFilesystems / listChildren ----

func TestClone(t *testing.T) {
	defer snapshotSeams()()
	if err := okHandle().Clone("tank/ds", "tank/clone", nil); err == nil {
		t.Fatal("want not-a-snapshot error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().Clone("tank/ds@s", "tank/clone", Nvlist{"a": uint64(1)}); err == nil {
		t.Fatal("want ioctl error")
	}
	// errlist error in outnvl.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{"tank/clone": int32(int32(unix.EEXIST))})
		return nil
	}
	if err := okHandle().Clone("tank/ds@s", "tank/clone", nil); err == nil {
		t.Fatal("want errlist error")
	}
	// success (no outnvl filled).
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().Clone("tank/ds@s", "tank/clone", nil); err != nil {
		t.Fatalf("Clone: %v", err)
	}
}

func TestRollback(t *testing.T) {
	defer snapshotSeams()()
	if _, err := okHandle().RollbackTo("fs", ""); err == nil {
		t.Fatal("want empty-target error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().Rollback("fs"); err == nil {
		t.Fatal("want ioctl error")
	}
	// success with target in outnvl.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{"target": "fs@s2"})
		return nil
	}
	got, err := okHandle().RollbackTo("fs", "fs@s2")
	if err != nil || got != "fs@s2" {
		t.Fatalf("RollbackTo = %q, %v", got, err)
	}
	// success but outnvl present without a string "target" (wrong type) -> "".
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{"target": uint64(7)})
		return nil
	}
	got, err = okHandle().Rollback("fs")
	if err != nil || got != "" {
		t.Fatalf("Rollback wrong-type target = %q, %v", got, err)
	}
	// success with NO outnvl filled -> "".
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	got, err = okHandle().Rollback("fs")
	if err != nil || got != "" {
		t.Fatalf("Rollback empty = %q, %v", got, err)
	}
}

func TestHold(t *testing.T) {
	defer snapshotSeams()()
	if err := okHandle().Hold("tank/ds@s", "", false); err == nil {
		t.Fatal("want empty-tag error")
	}
	if err := okHandle().Hold("tank/ds", "keep", false); err == nil {
		t.Fatal("want not-a-snapshot error")
	}
	// non-recursive ioctl error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().Hold("tank/ds@s", "keep", false); err == nil {
		t.Fatal("want ioctl error")
	}
	// non-recursive errlist error.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{"tank/ds@s": int32(int32(unix.EBUSY))})
		return nil
	}
	if err := okHandle().Hold("tank/ds@s", "keep", false); err == nil {
		t.Fatal("want errlist error")
	}
	// non-recursive success.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().Hold("tank/ds@s", "keep", false); err != nil {
		t.Fatalf("Hold: %v", err)
	}
}

func TestHoldRecursive(t *testing.T) {
	defer snapshotSeams()()

	// Recursive: descendantFilesystems enumeration fails (list_next ioctl err).
	ioctlFn = func(_ *Handle, req uintptr, _ *zfsCmd) error {
		if req == ZFS_IOC_DATASET_LIST_NEXT {
			return errInjected
		}
		return nil
	}
	if err := okHandle().Hold("tank/ds@s", "keep", true); err == nil {
		t.Fatal("want descendant enumerate error")
	}

	// Recursive success: list one child "tank/ds/kid", then ESRCH; the child
	// HAS the snapshot (ObjsetStats ok) so it gets a hold; the HOLD itself
	// succeeds.
	step := 0
	ioctlFn = func(_ *Handle, req uintptr, cmd *zfsCmd) error {
		switch req {
		case ZFS_IOC_DATASET_LIST_NEXT:
			step++
			switch step {
			case 1:
				// child of tank/ds
				copy(cmd.buf[offZcName:offZcName+maxNameLen], "tank/ds/kid\x00")
				cmd.setU64(offZcCookie, 1)
				return nil
			case 2:
				return unix.ESRCH // no more children of tank/ds
			default:
				return unix.ESRCH // no children of tank/ds/kid
			}
		case ZFS_IOC_OBJSET_STATS:
			// kid@s exists.
			putDst(t, cmd, Nvlist{"x": uint64(1)})
			return nil
		default: // HOLD
			return nil
		}
	}
	if err := okHandle().Hold("tank/ds@s", "keep", true); err != nil {
		t.Fatalf("Hold recursive: %v", err)
	}
}

func TestHoldRecursiveChildMissingSnap(t *testing.T) {
	defer snapshotSeams()()
	step := 0
	ioctlFn = func(_ *Handle, req uintptr, cmd *zfsCmd) error {
		switch req {
		case ZFS_IOC_DATASET_LIST_NEXT:
			step++
			switch step {
			case 1:
				copy(cmd.buf[offZcName:offZcName+maxNameLen], "tank/ds/kid\x00")
				cmd.setU64(offZcCookie, 1)
				return nil
			default:
				return unix.ESRCH
			}
		case ZFS_IOC_OBJSET_STATS:
			return errInjected // kid@s does NOT exist -> not held
		default:
			return nil
		}
	}
	if err := okHandle().Hold("tank/ds@s", "keep", true); err != nil {
		t.Fatalf("Hold recursive (missing child snap): %v", err)
	}
}

func TestRelease(t *testing.T) {
	defer snapshotSeams()()
	if err := okHandle().Release("tank/ds@s", ""); err == nil {
		t.Fatal("want empty-tag error")
	}
	if err := okHandle().Release("tank/ds", "keep"); err == nil {
		t.Fatal("want not-a-snapshot error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().Release("tank/ds@s", "keep"); err == nil {
		t.Fatal("want ioctl error")
	}
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{"tank/ds@s": int32(int32(unix.EBUSY))})
		return nil
	}
	if err := okHandle().Release("tank/ds@s", "keep"); err == nil {
		t.Fatal("want errlist error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().Release("tank/ds@s", "keep"); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestHolds(t *testing.T) {
	defer snapshotSeams()()
	if _, err := okHandle().Holds("tank/ds"); err == nil {
		t.Fatal("want not-a-snapshot error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().Holds("tank/ds@s"); err == nil {
		t.Fatal("want ioctl error")
	}
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{
			"keep":  uint64(1700000000),
			"wrong": "notuint64", // non-uint64: skipped
		})
		return nil
	}
	res, err := okHandle().Holds("tank/ds@s")
	if err != nil || res["keep"] != 1700000000 {
		t.Fatalf("Holds = %v, %v", res, err)
	}
	if _, ok := res["wrong"]; ok {
		t.Fatal("non-uint64 hold should be skipped")
	}
}

func TestBookmark(t *testing.T) {
	defer snapshotSeams()()
	if err := okHandle().Bookmark("tank/ds", "tank/ds#b"); err == nil {
		t.Fatal("want source-not-snapshot error")
	}
	if err := okHandle().Bookmark("tank/ds@s", "tank/ds"); err == nil {
		t.Fatal("want target-not-bookmark error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().Bookmark("tank/ds@s", "tank/ds#b"); err == nil {
		t.Fatal("want ioctl error")
	}
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{"tank/ds#b": int32(int32(unix.EEXIST))})
		return nil
	}
	if err := okHandle().Bookmark("tank/ds@s", "tank/ds#b"); err == nil {
		t.Fatal("want errlist error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().Bookmark("tank/ds@s", "tank/ds#b"); err != nil {
		t.Fatalf("Bookmark: %v", err)
	}
}

func TestGetBookmarks(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().GetBookmarks("tank/ds"); err == nil {
		t.Fatal("want ioctl error")
	}
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{
			"bm1":  Nvlist{"guid": uint64(1)},
			"junk": uint64(2), // non-nvlist: skipped
		})
		return nil
	}
	res, err := okHandle().GetBookmarks("tank/ds")
	if err != nil || len(res) != 1 {
		t.Fatalf("GetBookmarks = %v, %v", res, err)
	}
}

func TestDestroyBookmarks(t *testing.T) {
	defer snapshotSeams()()
	if err := okHandle().DestroyBookmarks(); err == nil {
		t.Fatal("want no-bookmarks error")
	}
	if err := okHandle().DestroyBookmarks("tank/ds@s"); err == nil {
		t.Fatal("want not-a-bookmark error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().DestroyBookmarks("tank/ds#b"); err == nil {
		t.Fatal("want ioctl error")
	}
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{"tank/ds#b": int32(int32(unix.ENOENT))})
		return nil
	}
	if err := okHandle().DestroyBookmarks("tank/ds#b"); err == nil {
		t.Fatal("want errlist error")
	}
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if err := okHandle().DestroyBookmarks("tank/ds#b1", "tank/ds#b2"); err != nil {
		t.Fatalf("DestroyBookmarks: %v", err)
	}
}

func TestListChildrenSkipAndError(t *testing.T) {
	defer snapshotSeams()()
	// setName error inside listChildren.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if _, err := okHandle().listChildren(string(make([]byte, maxPathLen))); err == nil {
		t.Fatal("want setName error")
	}
	// Non-ESRCH ioctl error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if _, err := okHandle().listChildren("tank/ds"); err == nil {
		t.Fatal("want ioctl error")
	}
	// One empty-name entry (skipped) and one snapshot-name entry (skipped),
	// then ESRCH.
	step := 0
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		step++
		switch step {
		case 1:
			// empty name -> skipped
			copy(cmd.buf[offZcName:offZcName+maxNameLen], "\x00")
			cmd.setU64(offZcCookie, 1)
			return nil
		case 2:
			// snapshot name -> skipped
			copy(cmd.buf[offZcName:offZcName+maxNameLen], "tank/ds@s\x00")
			cmd.setU64(offZcCookie, 2)
			return nil
		case 3:
			copy(cmd.buf[offZcName:offZcName+maxNameLen], "tank/ds/kid\x00")
			cmd.setU64(offZcCookie, 3)
			return nil
		default:
			return unix.ESRCH
		}
	}
	kids, err := okHandle().listChildren("tank/ds")
	if err != nil {
		t.Fatalf("listChildren: %v", err)
	}
	if len(kids) != 1 || kids[0] != "tank/ds/kid" {
		t.Fatalf("kids = %v, want [tank/ds/kid]", kids)
	}
}

// ---- send_recv_linux: Send / Receive / readFull / callNew(Name) ----

func TestSend(t *testing.T) {
	defer snapshotSeams()()
	if err := okHandle().Send("tank@s", nil, SendOptions{}); err == nil {
		t.Fatal("want nil-file error")
	}
	out, err := os.CreateTemp(t.TempDir(), "send")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	// ioctl error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	if err := okHandle().Send("tank@s", out, SendOptions{}); err == nil {
		t.Fatal("want ioctl error")
	}
	// success with all option flags set.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	opts := SendOptions{FromSnap: "tank@s0", LargeBlocks: true, EmbedData: true, Compress: true, Raw: true}
	if err := okHandle().Send("tank@s", out, opts); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

// streamFile returns a real temp file pre-filled with a valid DRR_BEGIN record
// (correct magic) plus padding, positioned at offset 0.
func beginStream(t *testing.T, magic uint64, withType bool) *os.File {
	t.Helper()
	bo, _ := nvHostOrder()
	rec := make([]byte, sizeofDmuReplayRecord)
	bo.PutUint64(rec[offDrrBeginMagic:], magic)
	if withType {
		// leave drr_type = DRR_BEGIN (0)
	}
	f, err := os.CreateTemp(t.TempDir(), "stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(rec); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestReceive(t *testing.T) {
	defer snapshotSeams()()

	// nil input.
	if _, err := okHandle().Receive("tank/ds@s", nil, RecvOptions{}); err == nil {
		t.Fatal("want nil-file error")
	}

	// short read (empty file) -> read begin record error.
	empty, err := os.CreateTemp(t.TempDir(), "empty")
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	if _, err := okHandle().Receive("tank/ds@s", empty, RecvOptions{}); err == nil {
		t.Fatal("want short-read error")
	}

	// bad magic with drr_type==DRR_BEGIN -> bad-stream-magic error.
	bad := beginStream(t, 0xBAD, true)
	defer bad.Close()
	if _, err := okHandle().Receive("tank/ds@s", bad, RecvOptions{}); err == nil {
		t.Fatal("want bad-magic error")
	}

	// good magic, ioctl error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return errInjected }
	g1 := beginStream(t, dmuBackupMagic, true)
	defer g1.Close()
	if _, err := okHandle().Receive("tank/ds@s", g1, RecvOptions{Origin: "tank/o@s", Force: true, Resumable: true}); err == nil {
		t.Fatal("want ioctl error")
	}

	// good magic, success with error_flags set in outnvl -> error.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{"error_flags": uint64(0x1)})
		return nil
	}
	g2 := beginStream(t, dmuBackupMagic, true)
	defer g2.Close()
	if _, err := okHandle().Receive("tank/ds@s", g2, RecvOptions{}); err == nil {
		t.Fatal("want error_flags error")
	}

	// good magic, success with error_flags == 0 -> ok.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		putDst(t, cmd, Nvlist{"error_flags": uint64(0)})
		return nil
	}
	g3 := beginStream(t, dmuBackupMagic, true)
	defer g3.Close()
	if _, err := okHandle().Receive("tank/ds@s", g3, RecvOptions{}); err != nil {
		t.Fatalf("Receive: %v", err)
	}

	// good magic, success with NO outnvl filled -> ok.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	g4 := beginStream(t, dmuBackupMagic, true)
	defer g4.Close()
	if _, err := okHandle().Receive("tank/ds@s", g4, RecvOptions{}); err != nil {
		t.Fatalf("Receive (no outnvl): %v", err)
	}
}

func TestReadFull(t *testing.T) {
	// short read path.
	f, err := os.CreateTemp(t.TempDir(), "rf")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	f.Seek(0, 0)
	if _, err := readFull(f, make([]byte, 10)); err == nil {
		t.Fatal("want short-read error")
	}
	// exact read path.
	f.Seek(0, 0)
	if n, err := readFull(f, make([]byte, 3)); err != nil || n != 3 {
		t.Fatalf("readFull = %d, %v", n, err)
	}
	// m==0, err==nil path: a reader that returns no bytes and no error.
	if _, err := readFull(zeroReader{}, make([]byte, 4)); err == nil {
		t.Fatal("want short-read error from a 0,nil reader")
	}
}

// zeroReader always returns (0, nil), exercising readFull's m==0 guard.
type zeroReader struct{}

func (zeroReader) Read([]byte) (int, error) { return 0, nil }

func TestCallNewNameEncodeAndGrowBudget(t *testing.T) {
	defer snapshotSeams()()

	// setName error inside the loop.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }
	if _, err := okHandle().callNewName(ZFS_IOC_HOLD, string(make([]byte, maxPathLen)), Nvlist{"a": uint64(1)}); err == nil {
		t.Fatal("want setName error")
	}

	// ENOMEM repeatedly until the 1 GiB budget is exceeded.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return unix.ENOMEM }
	if _, err := okHandle().callNewName(ZFS_IOC_HOLD, "tank", nil); err == nil {
		t.Fatal("want budget-exceeded error")
	}

	// ENOMEM grow once, then succeed.
	calls := 0
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		calls++
		if calls == 1 {
			return unix.ENOMEM
		}
		putDst(t, cmd, Nvlist{"ok": uint64(1)})
		return nil
	}
	if _, err := okHandle().callNewName(ZFS_IOC_HOLD, "tank", nil); err != nil {
		t.Fatalf("grow-then-succeed: %v", err)
	}
}

// TestCallNewNameLargeSrcAndBudget covers the srcBuf*2 dstSize-sizing branch
// (a >64 KiB innvl makes the initial dstSize follow len(srcBuf)*2) and the
// 1 GiB budget guard (with a multi-MiB initial dstSize, repeated ENOMEM
// doublings cross 1 GiB before the attempt cap). A single large Uint8Array
// keeps the encode cheap.
func TestCallNewNameLargeSrcAndBudget(t *testing.T) {
	defer snapshotSeams()()
	innvl := Nvlist{"blob": Uint8Array(make([]byte, 6<<20))} // ~6 MiB src
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return unix.ENOMEM }
	if _, err := okHandle().callNewName(ZFS_IOC_HOLD, "tank", innvl); err == nil {
		t.Fatal("want budget-exceeded error")
	}
}

func TestCallNewNameExhaustAttempts(t *testing.T) {
	defer snapshotSeams()()
	// Small innvl -> initial dstSize 128 KiB; 8 ENOMEM doublings reach 32 MiB,
	// under the 1 GiB budget, so the loop exhausts its 8 attempts and returns
	// the kept-growing error.
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return unix.ENOMEM }
	if _, err := okHandle().callNewName(ZFS_IOC_HOLD, "tank", nil); err == nil {
		t.Fatal("want kept-growing error after 8 attempts")
	}
}

// badNv carries an unsupported value type so EncodeNative (and thus setSrc /
// setConf) fails — exercising the encode-error branches of every builder.
var badNv = Nvlist{"bad": float64(1.5)}

func TestEncodeErrorBranches(t *testing.T) {
	defer snapshotSeams()()
	ioctlFn = func(*Handle, uintptr, *zfsCmd) error { return nil }

	// zfs_linux: Snapshot setSrc encode error is unreachable (snaps are bools),
	// but createWithKey props encode error is reachable.
	if err := okHandle().create("tank/ds", DMU_OST_ZFS, badNv); err == nil {
		t.Fatal("want createWithKey props encode error")
	}
	// dataset_linux: SetProp setSrc encode error.
	if err := okHandle().SetProp("tank/ds", badNv); err == nil {
		t.Fatal("want SetProp encode error")
	}

	// zfs_linux Snapshot: its src ({"snaps":{name:true}}) always encodes, so its
	// setSrc-error branch is only reachable by fault-injecting the encoder.
	encodeNative = func(Nvlist) ([]byte, error) { return nil, errInjected }
	if err := okHandle().Snapshot("tank", []string{"tank@s1"}); err == nil {
		t.Fatal("want Snapshot setSrc encode error")
	}
	encodeNative = EncodeNative
	// pool_linux: PoolCreate setConf encode error (bad vdev Extra) and setSrc
	// (bad props) encode error.
	rootBadConf := Vdev{Type: VDEV_TYPE_ROOT, Children: []Vdev{
		{Type: VDEV_TYPE_FILE, Path: "/d", Extra: Nvlist{"bad": float64(1)}},
	}}
	if err := okHandle().PoolCreate("tank", rootBadConf, nil); err == nil {
		t.Fatal("want PoolCreate setConf encode error")
	}
	goodRoot := Vdev{Type: VDEV_TYPE_ROOT, Children: []Vdev{{Type: VDEV_TYPE_FILE, Path: "/d"}}}
	if err := okHandle().PoolCreate("tank", goodRoot, badNv); err == nil {
		t.Fatal("want PoolCreate setSrc encode error")
	}
	// pool_linux: callWithDstConf setConf encode error (PoolTryImport with a bad
	// tryconfig).
	if _, err := okHandle().PoolTryImport(badNv); err == nil {
		t.Fatal("want PoolTryImport setConf encode error")
	}
	// send_recv: callNewName EncodeNative(innvl) error — Send with a bad option
	// isn't possible, but Receive's innvl can't carry bad types either; drive
	// callNewName directly with a bad innvl.
	if _, err := okHandle().callNewName(ZFS_IOC_HOLD, "tank", badNv); err == nil {
		t.Fatal("want callNewName encode error")
	}
}

// TestCallWithDstKeptGrowing covers the post-loop "kept growing" returns of
// callWithDst and callWithDstConf: every iteration reports ENOMEM with a larger
// need, so the bounded loop exits without success.
func TestCallWithDstKeptGrowing(t *testing.T) {
	defer snapshotSeams()()
	// callWithDst loops at most twice; report a strictly-growing need both
	// times so neither iteration succeeds and the function returns the
	// kept-growing error.
	need := uint64(128 * 1024)
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		need *= 2
		cmd.setU64(offZcNvlistDstSize, need)
		return unix.ENOMEM
	}
	if _, err := okHandle().ObjsetStats("tank/ds"); err == nil {
		t.Fatal("want callWithDst kept-growing error")
	}

	// callWithDstConf: same shape via PoolImport.
	need2 := uint64(256 * 1024)
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		need2 *= 2
		cmd.setU64(offZcNvlistDstSize, need2)
		return unix.ENOMEM
	}
	if _, err := okHandle().PoolImport("tank", Nvlist{ZPOOL_CONFIG_POOL_GUID: uint64(1)}); err == nil {
		t.Fatal("want callWithDstConf kept-growing error")
	}
}

func TestCallNewNameDecodeError(t *testing.T) {
	defer snapshotSeams()()
	// dst_filled set but the dst bytes are not a valid native stream.
	ioctlFn = func(_ *Handle, _ uintptr, cmd *zfsCmd) error {
		// Write a clearly-non-native header (encoding byte != nvEncodeNative)
		// into the captured dst slice.
		lastDst[0] = nvEncodeXDR + 9
		cmd.setU64(offZcNvlistDstFilled, 1)
		return nil
	}
	if _, err := okHandle().callNewName(ZFS_IOC_GET_HOLDS, "tank/ds@s", nil); err == nil {
		t.Fatal("want decode error")
	}
}
