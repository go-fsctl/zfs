// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These integration tests drive the live ZFS kernel via /dev/zfs. They are
// skipped automatically when /dev/zfs is absent (i.e. everywhere except the
// disposable ZFS guest). They expect a pool whose name is in ZFS_TEST_POOL
// (default "testpool") to already be imported.
//
// Run inside the guest as root:
//
//	ZFS_TEST_POOL=testpool sudo -E go test -run Integration -v ./...

func testPool() string {
	if p := os.Getenv("ZFS_TEST_POOL"); p != "" {
		return p
	}
	return "testpool"
}

func requireKernel(t *testing.T) *Handle {
	t.Helper()
	if !Available() {
		t.Skip("/dev/zfs not present; skipping kernel integration test")
	}
	h, err := Open()
	if err != nil {
		t.Skipf("cannot open /dev/zfs (need root): %v", err)
	}
	return h
}

func TestIntegrationPoolConfigs(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()

	cfgs, err := h.PoolConfigs()
	if err != nil {
		t.Fatalf("PoolConfigs: %v", err)
	}
	t.Logf("imported pools: %d", len(cfgs))
	pool := testPool()
	cfg, ok := cfgs[pool]
	if !ok {
		t.Fatalf("pool %q not found; got %v", pool, keysOf(cfgs))
	}
	// Sanity-check a few well-known config keys.
	if name, ok := cfg["name"].(string); !ok || name != pool {
		t.Errorf("config name = %v, want %q", cfg["name"], pool)
	}
	if _, ok := cfg["pool_guid"].(uint64); !ok {
		t.Errorf("missing pool_guid (got %T)", cfg["pool_guid"])
	}
	if v, ok := cfg["version"]; ok {
		t.Logf("pool %q version=%v guid=%v", pool, v, cfg["pool_guid"])
	}
}

func TestIntegrationSnapshot(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()
	pool := testPool()
	snap := pool + "@gofsctl_snap1"
	if err := h.Snapshot(pool, []string{snap}); err != nil {
		t.Fatalf("Snapshot %q: %v", snap, err)
	}
	t.Logf("created snapshot %s", snap)
}

func TestIntegrationCreateFilesystem(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()
	ds := testPool() + "/gofsctl_ds1"
	if err := h.CreateFilesystem(ds); err != nil {
		t.Fatalf("CreateFilesystem %q: %v", ds, err)
	}
	t.Logf("created filesystem %s", ds)
	// Verify via the read path.
	if _, err := h.ObjsetStats(ds); err != nil {
		t.Errorf("ObjsetStats %q after create: %v", ds, err)
	}
}

func keysOf(m map[string]Nvlist) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// TestIntegrationPoolLifecycle exercises the full pure-Go pool + dataset
// lifecycle against the live kernel. It is destructive and self-contained: it
// creates its own file-backed pool (it does NOT touch ZFS_TEST_POOL) and tears
// it down at the end. Requires root and two writable backing files under
// $ZFS_TEST_DIR (default /var/tmp).
//
//	sudo -E ZFS_TEST_DIR=/var/tmp go test -run PoolLifecycle -v ./...
func TestIntegrationPoolLifecycle(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()

	dir := os.Getenv("ZFS_TEST_DIR")
	if dir == "" {
		dir = "/var/tmp"
	}
	const name = "gofsctl_itpool"
	d0 := dir + "/gofsctl_it_d0.img"

	// Backing file must pre-exist and be >= 64MiB.
	f, err := os.OpenFile(d0, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		t.Skipf("cannot create backing file %s: %v", d0, err)
	}
	if err := f.Truncate(256 << 20); err != nil {
		f.Close()
		t.Fatalf("truncate %s: %v", d0, err)
	}
	f.Close()
	defer os.Remove(d0)

	// Best-effort cleanup of any leftover from a previous failed run.
	_ = h.PoolDestroy(name)

	root := Vdev{Type: VDEV_TYPE_ROOT, Children: []Vdev{{Type: VDEV_TYPE_FILE, Path: d0}}}
	if err := h.PoolCreate(name, root, nil); err != nil {
		t.Fatalf("PoolCreate: %v", err)
	}
	t.Logf("PoolCreate %s OK", name)
	defer func() { _ = h.PoolDestroy(name) }()

	// Pool shows up in PoolConfigs.
	cfgs, err := h.PoolConfigs()
	if err != nil {
		t.Fatalf("PoolConfigs: %v", err)
	}
	cfg, ok := cfgs[name]
	if !ok {
		t.Fatalf("created pool %q not in PoolConfigs", name)
	}

	// Pool properties read back.
	pp, err := h.PoolGetProps(name)
	if err != nil {
		t.Fatalf("PoolGetProps: %v", err)
	}
	if _, ok := pp["size"]; !ok {
		t.Errorf("PoolGetProps missing size: %v", pp)
	}

	// Dataset create + property set/get.
	ds := name + "/ds1"
	if err := h.CreateFilesystem(ds); err != nil {
		t.Fatalf("CreateFilesystem: %v", err)
	}
	// atime is an INDEX prop -> uint64 enum (0 = off). quota is a NUMBER prop.
	if err := h.SetProp(ds, Nvlist{"atime": uint64(0)}); err != nil {
		t.Fatalf("SetProp atime: %v", err)
	}
	if err := h.SetProp(ds, Nvlist{"quota": uint64(64 << 20)}); err != nil {
		t.Fatalf("SetProp quota: %v", err)
	}
	props, err := h.GetProps(ds)
	if err != nil {
		t.Fatalf("GetProps: %v", err)
	}
	if v, _ := props["quota"].(uint64); v != 64<<20 {
		t.Errorf("quota = %v, want %d", props["quota"], 64<<20)
	}
	if v, _ := props["atime"].(uint64); v != 0 {
		t.Errorf("atime = %v, want 0", props["atime"])
	}

	// Rename, snapshot, destroy snapshot, destroy dataset.
	ds2 := name + "/ds2"
	if err := h.Rename(ds, ds2, false); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	snap := ds2 + "@s1"
	if err := h.Snapshot(name, []string{snap}); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := h.Destroy(snap, false); err != nil {
		t.Fatalf("Destroy snapshot: %v", err)
	}
	if err := h.Destroy(ds2, false); err != nil {
		t.Fatalf("Destroy dataset: %v", err)
	}

	// Export then re-import from the captured config.
	if err := h.PoolExport(name, false, false); err != nil {
		t.Fatalf("PoolExport: %v", err)
	}
	if _, err := h.PoolImport(name, cfg); err != nil {
		t.Fatalf("PoolImport: %v", err)
	}
	t.Logf("export/import round-trip OK")

	// PoolDestroy (the deferred cleanup also runs, harmlessly).
	if err := h.PoolDestroy(name); err != nil {
		t.Fatalf("PoolDestroy: %v", err)
	}
	t.Logf("PoolDestroy %s OK", name)
}

// TestIntegrationSendRecv validates Send and Receive against the live kernel
// with a real CLI cross-check. It is destructive and self-contained: it builds
// its own file-backed pool via the `zpool`/`zfs` CLI (used ONLY for
// setup/cross-check — never by the library under test), writes data, then:
//
//  1. OUR Send(tp@s1) -> a stream file; cross-checks `zfs recv` (CLI) accepts
//     it and the received files match by sha256.
//  2. `zfs send` (CLI) -> a stream; OUR Receive() applies it; verifies the
//     received files match by sha256.
//
// Requires root, the zfs CLI, and a writable $ZFS_TEST_DIR (default /var/tmp).
// Run inside the guest:
//
//	sudo -E go test -run SendRecv -v ./...
func TestIntegrationSendRecv(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()

	if _, err := exec.LookPath("zfs"); err != nil {
		t.Skip("zfs CLI not found; needed for cross-check")
	}
	dir := os.Getenv("ZFS_TEST_DIR")
	if dir == "" {
		dir = "/var/tmp"
	}
	const pool = "gofsctl_srpool"
	img := filepath.Join(dir, "gofsctl_sr_d0.img")

	run := func(name string, args ...string) {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}

	// Fresh backing file and pool (CLI setup only).
	_ = exec.Command("zpool", "destroy", pool).Run()
	_ = os.Remove(img)
	f, err := os.OpenFile(img, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		t.Skipf("cannot create backing file %s: %v", img, err)
	}
	if err := f.Truncate(256 << 20); err != nil {
		f.Close()
		t.Fatalf("truncate: %v", err)
	}
	f.Close()
	defer os.Remove(img)
	run("zpool", "create", "-f", pool, img)
	defer func() { _ = exec.Command("zpool", "destroy", pool).Run() }()

	// Write deterministic content into the source dataset (the pool root fs is
	// mounted at /<pool> by default).
	mnt := "/" + pool
	files := map[string]string{
		"alpha.txt": "the quick brown fox jumps over the lazy dog\n",
		"beta.bin":  string(make([]byte, 1<<16)), // 64 KiB of zeros
		"gamma.txt": "go-fsctl pure-Go send/receive interop proof\n",
	}
	want := map[string]string{}
	for n, c := range files {
		p := filepath.Join(mnt, n)
		if err := os.WriteFile(p, []byte(c), 0644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		want[n] = sha256File(t, p)
	}
	run("zfs", "snapshot", pool+"@s1")

	// ---- Proof 1: OUR Send -> CLI recv ----
	streamPath := filepath.Join(dir, "gofsctl_sr.stream")
	defer os.Remove(streamPath)
	sf, err := os.Create(streamPath)
	if err != nil {
		t.Fatalf("create stream: %v", err)
	}
	if err := h.Send(pool+"@s1", sf, SendOptions{}); err != nil {
		sf.Close()
		t.Fatalf("Send: %v", err)
	}
	sf.Close()
	st, _ := os.Stat(streamPath)
	t.Logf("OUR Send(%s@s1) wrote %d-byte stream", pool, st.Size())

	// CLI recv into a new dataset.
	rf, err := os.Open(streamPath)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	recv := exec.Command("zfs", "recv", pool+"/r1")
	recv.Stdin = rf
	if out, err := recv.CombinedOutput(); err != nil {
		rf.Close()
		t.Fatalf("CLI `zfs recv` of OUR stream failed: %v\n%s", err, out)
	}
	rf.Close()
	// Verify files match (the received fs mounts at /<pool>/r1).
	for n, sum := range want {
		got := sha256File(t, filepath.Join(mnt, "r1", n))
		if got != sum {
			t.Errorf("Send proof: %s sha256 mismatch: got %s want %s", n, got, sum)
		}
	}
	t.Logf("Send proof OK: CLI recv accepted OUR stream; %d files match by sha256", len(want))

	// ---- Proof 2: CLI send -> OUR Receive ----
	cliStream := filepath.Join(dir, "gofsctl_cli.stream")
	defer os.Remove(cliStream)
	csf, err := os.Create(cliStream)
	if err != nil {
		t.Fatalf("create cli stream: %v", err)
	}
	send := exec.Command("zfs", "send", pool+"@s1")
	send.Stdout = csf
	var sendErr bytes.Buffer
	send.Stderr = &sendErr
	if err := send.Run(); err != nil {
		csf.Close()
		t.Fatalf("CLI `zfs send` failed: %v\n%s", err, sendErr.String())
	}
	csf.Close()
	cst, _ := os.Stat(cliStream)
	t.Logf("CLI `zfs send` produced %d-byte stream", cst.Size())

	in, err := os.Open(cliStream)
	if err != nil {
		t.Fatalf("open cli stream: %v", err)
	}
	br, err := h.Receive(pool+"/r2@s1", in, RecvOptions{})
	in.Close()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	t.Logf("OUR Receive: begin record magic=%#x type=%d toguid=%#x toname=%q",
		br.Magic, br.Type, br.ToGuid, br.ToName)
	// A freshly received filesystem is not auto-mounted by the receive ioctl
	// (the CLI mounts it in a separate step). Mount it for the sha256 check
	// (CLI used only for verification, never by the library under test).
	run("zfs", "mount", pool+"/r2")
	for n, sum := range want {
		got := sha256File(t, filepath.Join(mnt, "r2", n))
		if got != sum {
			t.Errorf("Receive proof: %s sha256 mismatch: got %s want %s", n, got, sum)
		}
	}
	t.Logf("Receive proof OK: OUR Receive consumed a real CLI stream; %d files match by sha256", len(want))
}

// TestIntegrationLifecycle validates Clone, Rollback, Hold/Release/Holds and
// Bookmark/GetBookmarks/DestroyBookmarks against the live kernel, cross-checked
// with the real `zfs`/`zpool` CLI (used ONLY for setup + verification, never by
// the library under test). It is destructive and self-contained: it builds its
// own file-backed pool and tears it down at the end.
//
// Requires root, the zfs CLI, and a writable $ZFS_TEST_DIR (default /var/tmp).
// Run inside the guest:
//
//	sudo -E go test -run Lifecycle -v ./...
func TestIntegrationLifecycle(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()

	if _, err := exec.LookPath("zfs"); err != nil {
		t.Skip("zfs CLI not found; needed for cross-check")
	}
	dir := os.Getenv("ZFS_TEST_DIR")
	if dir == "" {
		dir = "/var/tmp"
	}
	const pool = "gofsctl_lcpool"
	img := filepath.Join(dir, "gofsctl_lc_d0.img")

	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}
	// cli runs a CLI command and returns its combined output and error (for
	// cases where we EXPECT failure, e.g. destroy of a held snapshot).
	cli := func(name string, args ...string) (string, error) {
		out, err := exec.Command(name, args...).CombinedOutput()
		return string(out), err
	}

	// Fresh backing file and pool (CLI setup only).
	_ = exec.Command("zpool", "destroy", pool).Run()
	_ = os.Remove(img)
	f, err := os.OpenFile(img, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		t.Skipf("cannot create backing file %s: %v", img, err)
	}
	if err := f.Truncate(256 << 20); err != nil {
		f.Close()
		t.Fatalf("truncate: %v", err)
	}
	f.Close()
	defer os.Remove(img)
	run("zpool", "create", "-f", pool, img)
	defer func() { _ = exec.Command("zpool", "destroy", pool).Run() }()

	mnt := "/" + pool

	// ---------------------------------------------------------------
	// Clone: snapshot a source fs, OUR Clone, cross-check via CLI.
	// ---------------------------------------------------------------
	src := pool + "/src"
	run("zfs", "create", src)
	if err := os.WriteFile(filepath.Join(mnt, "src", "data.txt"), []byte("original\n"), 0644); err != nil {
		t.Fatalf("write src data: %v", err)
	}
	run("zfs", "snapshot", src+"@base")

	clone := pool + "/clone"
	if err := h.Clone(src+"@base", clone, nil); err != nil {
		t.Fatalf("OUR Clone: %v", err)
	}
	// Cross-check: clone appears as a filesystem and its origin is src@base.
	fsList := run("zfs", "list", "-H", "-t", "filesystem", "-o", "name")
	if !strings.Contains(fsList, clone) {
		t.Errorf("clone %q not in `zfs list -t filesystem`:\n%s", clone, fsList)
	}
	origin := strings.TrimSpace(run("zfs", "get", "-H", "-o", "value", "origin", clone))
	if origin != src+"@base" {
		t.Errorf("clone origin = %q, want %q", origin, src+"@base")
	}
	t.Logf("Clone proof OK: %s exists; `zfs get origin` = %s", clone, origin)

	// ---------------------------------------------------------------
	// Rollback: write+snapshot, modify, OUR Rollback, verify reverted.
	// ---------------------------------------------------------------
	rb := pool + "/rb"
	run("zfs", "create", rb)
	rbFile := filepath.Join(mnt, "rb", "v.txt")
	if err := os.WriteFile(rbFile, []byte("v1\n"), 0644); err != nil {
		t.Fatalf("write rb v1: %v", err)
	}
	run("zfs", "snapshot", rb+"@v1")
	if err := os.WriteFile(rbFile, []byte("v2-modified\n"), 0644); err != nil {
		t.Fatalf("write rb v2: %v", err)
	}
	target, err := h.Rollback(rb)
	if err != nil {
		t.Fatalf("OUR Rollback: %v", err)
	}
	t.Logf("OUR Rollback(%s) -> %s", rb, target)
	got, err := os.ReadFile(rbFile)
	if err != nil {
		t.Fatalf("read rb after rollback: %v", err)
	}
	if string(got) != "v1\n" {
		t.Errorf("after Rollback content = %q, want %q", got, "v1\n")
	}
	if target != rb+"@v1" {
		t.Errorf("Rollback target = %q, want %q", target, rb+"@v1")
	}
	t.Logf("Rollback proof OK: file content reverted to %q", got)

	// ---------------------------------------------------------------
	// Hold / Release: OUR Hold blocks destroy (EBUSY); Release unblocks.
	// ---------------------------------------------------------------
	hsnap := src + "@base"
	const tag = "gofsctl_keep"
	if err := h.Hold(hsnap, tag, false); err != nil {
		t.Fatalf("OUR Hold: %v", err)
	}
	holdsOut := run("zfs", "holds", "-H", hsnap)
	if !strings.Contains(holdsOut, tag) {
		t.Errorf("`zfs holds` missing tag %q:\n%s", tag, holdsOut)
	}
	t.Logf("Hold proof OK: `zfs holds %s` = %s", hsnap, strings.TrimSpace(holdsOut))
	// OUR Holds() must agree.
	hmap, err := h.Holds(hsnap)
	if err != nil {
		t.Fatalf("OUR Holds: %v", err)
	}
	if _, ok := hmap[tag]; !ok {
		t.Errorf("OUR Holds() = %v, want tag %q present", hmap, tag)
	}
	// Destroy must now fail with EBUSY (snapshot is held). The clone also
	// depends on src@base, so destroy a fresh held snapshot to isolate EBUSY.
	hsnap2 := rb + "@v1"
	if err := h.Hold(hsnap2, tag, false); err != nil {
		t.Fatalf("OUR Hold hsnap2: %v", err)
	}
	if out, derr := cli("zfs", "destroy", hsnap2); derr == nil {
		t.Errorf("destroy of held %s unexpectedly succeeded:\n%s", hsnap2, out)
	} else {
		t.Logf("Hold proof OK: destroy of held %s correctly failed: %s", hsnap2, strings.TrimSpace(out))
	}
	// Release, then destroy succeeds.
	if err := h.Release(hsnap2, tag); err != nil {
		t.Fatalf("OUR Release: %v", err)
	}
	run("zfs", "destroy", hsnap2)
	t.Logf("Release proof OK: after OUR Release, destroy of %s succeeded", hsnap2)
	// Release the first hold too (so teardown is clean).
	if err := h.Release(hsnap, tag); err != nil {
		t.Fatalf("OUR Release hsnap: %v", err)
	}

	// ---------------------------------------------------------------
	// Bookmark / GetBookmarks / DestroyBookmarks.
	// ---------------------------------------------------------------
	bsnap := src + "@base"
	bmark := src + "#bm1"
	if err := h.Bookmark(bsnap, bmark); err != nil {
		t.Fatalf("OUR Bookmark: %v", err)
	}
	bmList := run("zfs", "list", "-H", "-t", "bookmark", "-o", "name")
	if !strings.Contains(bmList, bmark) {
		t.Errorf("bookmark %q not in `zfs list -t bookmark`:\n%s", bmark, bmList)
	}
	t.Logf("Bookmark proof OK: `zfs list -t bookmark` shows %s", bmark)
	// OUR GetBookmarks must list it (short name = part after '#').
	bms, err := h.GetBookmarks(src)
	if err != nil {
		t.Fatalf("OUR GetBookmarks: %v", err)
	}
	if _, ok := bms["bm1"]; !ok {
		t.Errorf("OUR GetBookmarks() = %v, want \"bm1\" present", keysOfNv(bms))
	} else {
		t.Logf("GetBookmarks proof OK: OUR GetBookmarks(%s) = %v", src, keysOfNv(bms))
	}
	// OUR DestroyBookmarks removes it.
	if err := h.DestroyBookmarks(bmark); err != nil {
		t.Fatalf("OUR DestroyBookmarks: %v", err)
	}
	bmList2 := run("zfs", "list", "-H", "-t", "bookmark", "-o", "name")
	if strings.Contains(bmList2, bmark) {
		t.Errorf("bookmark %q still present after DestroyBookmarks:\n%s", bmark, bmList2)
	}
	t.Logf("DestroyBookmarks proof OK: %s gone from `zfs list -t bookmark`", bmark)
}

// TestIntegrationEncryptPromoteInherit exercises the encryption key-management
// ops (CreateEncrypted / UnloadKey / LoadKey / ChangeKey) plus Promote and
// Inherit against the live kernel, cross-checking each against the zfs CLI.
func TestIntegrationEncryptPromoteInherit(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()

	if _, err := exec.LookPath("zfs"); err != nil {
		t.Skip("zfs CLI not found; needed for cross-check")
	}
	dir := os.Getenv("ZFS_TEST_DIR")
	if dir == "" {
		dir = "/var/tmp"
	}
	const pool = "gofsctl_encpool"
	img := filepath.Join(dir, "gofsctl_enc_d0.img")

	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}
	cli := func(name string, args ...string) (string, error) {
		out, err := exec.Command(name, args...).CombinedOutput()
		return string(out), err
	}
	get := func(prop, ds string) string {
		t.Helper()
		return strings.TrimSpace(run("zfs", "get", "-H", "-o", "value", prop, ds))
	}

	// Fresh backing file and pool.
	_ = exec.Command("zpool", "destroy", pool).Run()
	_ = os.Remove(img)
	f, err := os.OpenFile(img, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		t.Skipf("cannot create backing file %s: %v", img, err)
	}
	if err := f.Truncate(256 << 20); err != nil {
		f.Close()
		t.Fatalf("truncate: %v", err)
	}
	f.Close()
	defer os.Remove(img)
	run("zpool", "create", "-f", pool, img)
	defer func() { _ = exec.Command("zpool", "destroy", pool).Run() }()

	mnt := "/" + pool

	// Two distinct 32-byte raw keys.
	keyA := make([]byte, 32)
	keyB := make([]byte, 32)
	for i := 0; i < 32; i++ {
		keyA[i] = byte(0xA0 + i)
		keyB[i] = byte(0x10 + i)
	}

	// ---------------------------------------------------------------
	// CreateEncrypted: raw 32-byte key, keyformat=raw, keylocation=prompt.
	// ---------------------------------------------------------------
	enc := pool + "/enc"
	props := Nvlist{
		"encryption":  uint64(ZIO_CRYPT_AES_256_GCM), // numeric enum (kernel reads uint64)
		"keyformat":   uint64(ZFS_KEYFORMAT_RAW),
		"keylocation": "prompt", // keylocation is read as a string
	}
	if err := h.CreateEncrypted(enc, keyA, props); err != nil {
		t.Fatalf("OUR CreateEncrypted: %v", err)
	}
	if v := get("encryption", enc); v != "aes-256-gcm" {
		t.Errorf("encryption = %q, want aes-256-gcm", v)
	}
	if v := get("keystatus", enc); v != "available" {
		t.Errorf("keystatus after create = %q, want available", v)
	}
	t.Logf("CreateEncrypted proof OK: `zfs get encryption,keystatus` = %s,%s",
		get("encryption", enc), get("keystatus", enc))
	// The CREATE ioctl does not mount the new dataset (mounting is a userland
	// VFS step); mount it so we can write a file.
	run("zfs", "mount", enc)
	// Write a file we will read back after an unload/load cycle.
	encFile := filepath.Join(mnt, "enc", "secret.txt")
	const secret = "encrypted-payload-12345\n"
	if err := os.WriteFile(encFile, []byte(secret), 0644); err != nil {
		t.Fatalf("write encrypted file: %v", err)
	}
	wantSum := sha256File(t, encFile)

	// ---------------------------------------------------------------
	// UnloadKey: must unmount first; keystatus -> unavailable; mount fails.
	// ---------------------------------------------------------------
	run("zfs", "unmount", enc)
	if err := h.UnloadKey(enc); err != nil {
		t.Fatalf("OUR UnloadKey: %v", err)
	}
	if v := get("keystatus", enc); v != "unavailable" {
		t.Errorf("keystatus after UnloadKey = %q, want unavailable", v)
	}
	if out, merr := cli("zfs", "mount", enc); merr == nil {
		t.Errorf("mount of key-less dataset unexpectedly succeeded:\n%s", out)
	} else {
		t.Logf("UnloadKey proof OK: keystatus=unavailable, mount correctly failed: %s",
			strings.TrimSpace(out))
	}

	// ---------------------------------------------------------------
	// LoadKey(same key): keystatus -> available; mount + read identical.
	// ---------------------------------------------------------------
	if err := h.LoadKey(enc, keyA, false); err != nil {
		t.Fatalf("OUR LoadKey(keyA): %v", err)
	}
	if v := get("keystatus", enc); v != "available" {
		t.Errorf("keystatus after LoadKey = %q, want available", v)
	}
	run("zfs", "mount", enc)
	gotSum := sha256File(t, encFile)
	if gotSum != wantSum {
		t.Errorf("file checksum after load/mount = %s, want %s", gotSum, wantSum)
	}
	got, _ := os.ReadFile(encFile)
	if string(got) != secret {
		t.Errorf("file content after load = %q, want %q", got, secret)
	}
	t.Logf("LoadKey proof OK: keystatus=available, file read back identical (sha256 %s)", gotSum)

	// ---------------------------------------------------------------
	// ChangeKey(keyB): old key no longer loads; new key does.
	// ---------------------------------------------------------------
	// When changing the key MATERIAL the kernel re-validates the key against
	// the keyformat, so it must be supplied in props (same as `zfs change-key`).
	ckProps := Nvlist{
		"keyformat":   uint64(ZFS_KEYFORMAT_RAW),
		"keylocation": "prompt",
	}
	if err := h.ChangeKey(enc, keyB, ckProps); err != nil {
		t.Fatalf("OUR ChangeKey(keyB): %v", err)
	}
	run("zfs", "unmount", enc)
	if err := h.UnloadKey(enc); err != nil {
		t.Fatalf("OUR UnloadKey after ChangeKey: %v", err)
	}
	if err := h.LoadKey(enc, keyA, false); err == nil {
		t.Errorf("LoadKey(old keyA) unexpectedly succeeded after ChangeKey")
	} else {
		t.Logf("ChangeKey proof OK: LoadKey(old key) correctly failed: %v", err)
	}
	if err := h.LoadKey(enc, keyB, false); err != nil {
		t.Fatalf("OUR LoadKey(new keyB) after ChangeKey: %v", err)
	}
	if v := get("keystatus", enc); v != "available" {
		t.Errorf("keystatus after LoadKey(keyB) = %q, want available", v)
	}
	t.Logf("ChangeKey proof OK: LoadKey(new key) succeeded, keystatus=available")

	// ---------------------------------------------------------------
	// Promote: clone a snapshot, OUR Promote, origin relationship flips.
	// ---------------------------------------------------------------
	po := pool + "/po"
	run("zfs", "create", po)
	if err := os.WriteFile(filepath.Join(mnt, "po", "f.txt"), []byte("orig\n"), 0644); err != nil {
		t.Fatalf("write po file: %v", err)
	}
	run("zfs", "snapshot", po+"@s1")
	clone := pool + "/poclone"
	if err := h.Clone(po+"@s1", clone, nil); err != nil {
		t.Fatalf("Clone for promote: %v", err)
	}
	// Before promote: clone's origin is po@s1; po has no origin.
	if o := get("origin", clone); o != po+"@s1" {
		t.Fatalf("pre-promote clone origin = %q, want %q", o, po+"@s1")
	}
	if err := h.Promote(clone); err != nil {
		t.Fatalf("OUR Promote: %v", err)
	}
	// After promote: the snapshot moved onto the clone, so the ORIGINAL's
	// origin now points into the clone, and the clone's origin is "-".
	origClone := get("origin", clone)
	origPo := get("origin", po)
	if !strings.HasPrefix(origPo, clone+"@") {
		t.Errorf("after Promote, origin of original %q = %q, want it to point into clone %q",
			po, origPo, clone)
	}
	if origClone != "-" {
		t.Errorf("after Promote, clone origin = %q, want \"-\"", origClone)
	}
	t.Logf("Promote proof OK: origin flipped — `zfs get origin %s` = %s (was on the clone), clone origin = %s",
		po, origPo, origClone)

	// ---------------------------------------------------------------
	// Inherit: set prop on parent, child inherits, OUR Inherit -> "inherited".
	// ---------------------------------------------------------------
	parent := pool + "/p"
	child := parent + "/c"
	run("zfs", "create", parent)
	run("zfs", "set", "compression=gzip", parent)
	run("zfs", "create", child)
	// Give the child a LOCAL override so we can observe Inherit clearing it.
	run("zfs", "set", "compression=lz4", child)
	srcBefore := strings.TrimSpace(run("zfs", "get", "-H", "-o", "source", "compression", child))
	if srcBefore != "local" {
		t.Fatalf("pre-Inherit child compression source = %q, want local", srcBefore)
	}
	if err := h.Inherit(child, "compression", false); err != nil {
		t.Fatalf("OUR Inherit: %v", err)
	}
	srcAfter := strings.TrimSpace(run("zfs", "get", "-H", "-o", "source", "compression", child))
	valAfter := get("compression", child)
	if srcAfter != "inherited from "+parent && srcAfter != "inherited" {
		t.Errorf("after Inherit, compression source = %q, want inherited from %q", srcAfter, parent)
	}
	if valAfter != "gzip" {
		t.Errorf("after Inherit, compression value = %q, want gzip (parent's)", valAfter)
	}
	t.Logf("Inherit proof OK: `zfs get -o source compression %s` = %q, value now %q",
		child, srcAfter, valAfter)
}

func keysOfNv(m map[string]Nvlist) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("sha256sum", path).Output()
	if err != nil {
		t.Fatalf("sha256sum %s: %v", path, err)
	}
	// "<hex>  <path>\n"
	for i := 0; i < len(out); i++ {
		if out[i] == ' ' {
			return string(out[:i])
		}
	}
	return string(out)
}

// TestIntegrationPoolAdmin exercises the pool-level admin operations added in
// feat/pool-scrub-trim-vdev against the live kernel, cross-checking each with
// the zpool CLI. It builds a fresh file-backed pool with three backing files:
// one in the pool initially, the others used for vdev attach/replace.
//
// Run inside the guest as root:
//
//	sudo -E go test -run TestIntegrationPoolAdmin -v ./...
func TestIntegrationPoolAdmin(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()

	if _, err := exec.LookPath("zpool"); err != nil {
		t.Skip("zpool CLI not found; needed for cross-check")
	}
	dir := os.Getenv("ZFS_TEST_DIR")
	if dir == "" {
		dir = "/var/tmp"
	}
	const pool = "gofsctl_admpool"
	d0 := filepath.Join(dir, "gofsctl_adm_d0.img")
	d1 := filepath.Join(dir, "gofsctl_adm_d1.img")
	d2 := filepath.Join(dir, "gofsctl_adm_d2.img")

	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}
	status := func(args ...string) string {
		t.Helper()
		return run("zpool", append([]string{"status"}, args...)...)
	}

	// Fresh backing files and pool (CLI setup only).
	_ = exec.Command("zpool", "destroy", pool).Run()
	for _, p := range []string{d0, d1, d2} {
		_ = os.Remove(p)
		f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0600)
		if err != nil {
			t.Skipf("cannot create backing file %s: %v", p, err)
		}
		if err := f.Truncate(512 << 20); err != nil { // 512 MiB each
			f.Close()
			t.Fatalf("truncate %s: %v", p, err)
		}
		f.Close()
		defer os.Remove(p)
	}
	// Single-disk pool on d0 to start.
	run("zpool", "create", "-f", pool, d0)
	defer func() { _ = exec.Command("zpool", "destroy", pool).Run() }()

	// Put some data in so a scrub has blocks to examine.
	mnt := "/" + pool
	for i := 0; i < 8; i++ {
		fn := filepath.Join(mnt, "data", "f")
		_ = os.MkdirAll(filepath.Join(mnt, "data"), 0755)
		buf := bytes.Repeat([]byte{byte(i), 0xab, 0xcd}, 1<<20) // ~3 MiB each
		if err := os.WriteFile(fn+string(rune('0'+i)), buf, 0644); err != nil {
			t.Fatalf("write data: %v", err)
		}
	}
	run("zpool", "sync", pool)

	// ---------------------------------------------------------------
	// Scrub: OUR ScrubStart -> `zpool status` shows scrub; OUR ScanStatus
	// agrees; OUR ScrubStop cancels.
	// ---------------------------------------------------------------
	if err := h.ScrubStart(pool); err != nil {
		t.Fatalf("OUR ScrubStart: %v", err)
	}
	st, err := h.ScanStatus(pool)
	if err != nil {
		t.Fatalf("OUR ScanStatus: %v", err)
	}
	if st.Func != ScanScrub {
		t.Errorf("ScanStatus.Func = %v, want scrub", st.Func)
	}
	// State should be scanning or finished (a tiny pool can complete instantly).
	if st.State != ScanStateScanning && st.State != ScanStateFinished {
		t.Errorf("ScanStatus.State = %v, want scanning/finished", st.State)
	}
	scrubStatus := status()
	if !strings.Contains(scrubStatus, "scrub") {
		t.Errorf("`zpool status` does not mention scrub:\n%s", scrubStatus)
	}
	t.Logf("Scrub proof OK: OUR ScanStatus={func=%v state=%v examined=%d/%d %.1f%% errors=%d}\n`zpool status` scan line: %s",
		st.Func, st.State, st.Examined, st.ToExamine, st.Percent(), st.Errors, scanLine(scrubStatus))

	// Cancel (no-op if already finished; ScrubStop tolerates that).
	if err := h.ScrubStop(pool); err != nil {
		t.Fatalf("OUR ScrubStop: %v", err)
	}
	t.Logf("ScrubStop proof OK (scrub canceled/no-op)")

	// ---------------------------------------------------------------
	// Trim: OUR TrimPool(start) -> `zpool status -t` shows trim state.
	// ---------------------------------------------------------------
	if err := h.TrimPool(pool, nil, 0, false, POOL_TRIM_START); err != nil {
		// File vdevs support manual trim on 2.2.x; a hard failure is a real bug.
		t.Fatalf("OUR TrimPool(start): %v", err)
	}
	trimStatus := status("-t")
	low := strings.ToLower(trimStatus)
	if !strings.Contains(low, "trim") && !strings.Contains(low, "untrimmed") {
		t.Errorf("`zpool status -t` does not mention trim:\n%s", trimStatus)
	}
	t.Logf("Trim proof OK: `zpool status -t` trim line: %s", trimLine(trimStatus))
	_ = h.TrimPool(pool, nil, 0, false, POOL_TRIM_CANCEL) // best-effort cleanup

	// ---------------------------------------------------------------
	// Initialize: OUR InitializePool(start) -> `zpool status -i` shows it;
	// cancel works.
	// ---------------------------------------------------------------
	if err := h.InitializePool(pool, nil, POOL_INITIALIZE_START); err != nil {
		t.Fatalf("OUR InitializePool(start): %v", err)
	}
	initStatus := status("-i")
	if !strings.Contains(strings.ToLower(initStatus), "initial") {
		t.Errorf("`zpool status -i` does not mention initializing:\n%s", initStatus)
	}
	t.Logf("Initialize proof OK: `zpool status -i` init line: %s", initLine(initStatus))
	if err := h.InitializePool(pool, nil, POOL_INITIALIZE_CANCEL); err != nil {
		t.Fatalf("OUR InitializePool(cancel): %v", err)
	}
	t.Logf("Initialize cancel proof OK")

	// ---------------------------------------------------------------
	// VdevAttach: turn the single-disk pool into a mirror by attaching d1
	// to d0 -> `zpool status` shows mirror (+ resilver). OUR VdevDetach
	// removes it -> back to single.
	// ---------------------------------------------------------------
	if err := h.VdevAttach(pool, d0, d1, false); err != nil {
		t.Fatalf("OUR VdevAttach(%s -> %s): %v", d0, d1, err)
	}
	// Wait briefly for the config to reflect the mirror + any resilver.
	mirrorStatus := waitFor(t, 10, func() (string, bool) {
		s := status()
		return s, strings.Contains(s, "mirror")
	})
	if !strings.Contains(mirrorStatus, "mirror") {
		t.Errorf("after VdevAttach `zpool status` has no mirror:\n%s", mirrorStatus)
	}
	if !strings.Contains(mirrorStatus, filepath.Base(d1)) && !strings.Contains(mirrorStatus, d1) {
		t.Errorf("after VdevAttach `zpool status` does not list %s:\n%s", d1, mirrorStatus)
	}
	t.Logf("VdevAttach->mirror proof OK:\n%s", mirrorStatus)

	// Detach d1 -> back to single-disk.
	// Allow any in-flight resilver to settle first (detach of a resilvering
	// device can return EBUSY).
	waitResilverDone(t, h, pool)
	if err := h.VdevDetach(pool, d1); err != nil {
		t.Fatalf("OUR VdevDetach(%s): %v", d1, err)
	}
	singleStatus := waitFor(t, 10, func() (string, bool) {
		s := status()
		return s, !strings.Contains(s, "mirror")
	})
	if strings.Contains(singleStatus, "mirror") {
		t.Errorf("after VdevDetach pool still a mirror:\n%s", singleStatus)
	}
	t.Logf("VdevDetach->single proof OK:\n%s", singleStatus)

	// ---------------------------------------------------------------
	// VdevAttach(replace=true): replace d0 with d2. After resilver the pool
	// runs on d2 and d0 is gone.
	// ---------------------------------------------------------------
	if err := h.VdevAttach(pool, d0, d2, true); err != nil {
		t.Fatalf("OUR VdevAttach(replace %s -> %s): %v", d0, d2, err)
	}
	waitResilverDone(t, h, pool)
	replStatus := waitFor(t, 15, func() (string, bool) {
		s := status()
		return s, strings.Contains(s, filepath.Base(d2)) && !strings.Contains(s, filepath.Base(d0))
	})
	if !strings.Contains(replStatus, filepath.Base(d2)) {
		t.Errorf("after replace `zpool status` does not list %s:\n%s", d2, replStatus)
	}
	if strings.Contains(replStatus, filepath.Base(d0)) {
		t.Errorf("after replace `zpool status` still lists old device %s:\n%s", d0, replStatus)
	}
	t.Logf("VdevAttach(replace) proof OK:\n%s", replStatus)
}

// scanLine returns the line of `zpool status` output mentioning scan/scrub.
func scanLine(s string) string { return grepLine(s, "scan") }

// trimLine returns the `zpool status -t` line mentioning trim.
func trimLine(s string) string { return grepLine(s, "trim") }

// initLine returns the `zpool status -i` line mentioning initial.
func initLine(s string) string { return grepLine(s, "initial") }

func grepLine(s, sub string) string {
	low := strings.ToLower(sub)
	for _, ln := range strings.Split(s, "\n") {
		if strings.Contains(strings.ToLower(ln), low) {
			return strings.TrimSpace(ln)
		}
	}
	return "(none)"
}

// waitFor polls fn up to attempts times (~200ms apart) until it returns true,
// returning the last observed value.
func waitFor(t *testing.T, attempts int, fn func() (string, bool)) string {
	t.Helper()
	var last string
	for i := 0; i < attempts; i++ {
		s, ok := fn()
		last = s
		if ok {
			return s
		}
		time.Sleep(200 * time.Millisecond)
	}
	return last
}

// waitResilverDone polls OUR ScanStatus until no resilver/scrub is scanning.
func waitResilverDone(t *testing.T, h *Handle, pool string) {
	t.Helper()
	for i := 0; i < 50; i++ {
		st, err := h.ScanStatus(pool)
		if err != nil || !st.Scanning() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestIntegrationChannelDiffUserspace drives the channel-program, diff and
// userspace ioctls against a freshly-created loopback pool and cross-checks the
// results against the zfs/zpool CLIs. Skipped without /dev/zfs (and without the
// CLIs for the cross-check).
func TestIntegrationChannelDiffUserspace(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()

	if _, err := exec.LookPath("zfs"); err != nil {
		t.Skip("zfs CLI not found; needed for cross-check")
	}
	dir := os.Getenv("ZFS_TEST_DIR")
	if dir == "" {
		dir = "/var/tmp"
	}
	const pool = "gofsctl_cdupool"
	img := filepath.Join(dir, "gofsctl_cdu_d0.img")

	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}

	_ = exec.Command("zpool", "destroy", pool).Run()
	_ = os.Remove(img)
	f, err := os.OpenFile(img, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		t.Skipf("cannot create backing file %s: %v", img, err)
	}
	if err := f.Truncate(512 << 20); err != nil {
		f.Close()
		t.Fatalf("truncate: %v", err)
	}
	f.Close()
	defer os.Remove(img)
	run("zpool", "create", "-f", pool, img)
	defer func() { _ = exec.Command("zpool", "destroy", pool).Run() }()

	fs := pool + "/fsa"
	run("zfs", "create", fs)
	mnt := "/" + fs

	// ---- Proof 1: ChannelProgram (list snapshots via zcp) ----
	run("zfs", "snapshot", fs+"@s1")
	run("zfs", "snapshot", fs+"@s2")

	// OUR ChannelProgram running a property-get Lua snippet. zfs.get_prop
	// returns (value, source); we keep just the value so the program returns a
	// single value (zcp cannot return multiple).
	res, err := h.ChannelProgram(pool,
		`args = ...; local v = zfs.get_prop(args["ds"], "compression"); return v`,
		ChannelProgramOptions{Sync: false, Args: Nvlist{"ds": fs}})
	if err != nil {
		t.Fatalf("ChannelProgram get_prop: %v", err)
	}
	t.Logf("OUR ChannelProgram zfs.get_prop(%s, compression) -> %v", fs, res)

	// OUR ListSnapshotsZCP convenience wrapper.
	got, err := h.ListSnapshotsZCP(fs)
	if err != nil {
		t.Fatalf("ListSnapshotsZCP: %v", err)
	}
	sort.Strings(got)
	want := []string{fs + "@s1", fs + "@s2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ListSnapshotsZCP = %v, want %v", got, want)
	}
	// Cross-check against `zfs list -t snap`.
	cli := run("zfs", "list", "-H", "-t", "snapshot", "-o", "name", "-r", fs)
	t.Logf("OUR ListSnapshotsZCP(%s) = %v\nCLI `zfs list -t snap`:\n%s", fs, got, cli)
	for _, snap := range want {
		if !strings.Contains(cli, snap) {
			t.Errorf("CLI snapshot list missing %s", snap)
		}
	}
	// Cross-check OUR zcp against the real `zfs program` (best-effort: the CLI's
	// channel-program path is not always present/permitted, so a failure here is
	// logged but not fatal — OUR result is already cross-checked against
	// `zfs list -t snap` above).
	tmpLua := filepath.Join(dir, "gofsctl_list.lua")
	_ = os.WriteFile(tmpLua, []byte(`args = ...
local r = {}
local i = 1
for s in zfs.list.snapshots(args["fs"]) do r[i] = s; i = i + 1 end
return r
`), 0644)
	defer os.Remove(tmpLua)
	if prog, perr := exec.Command("zfs", "program", pool, tmpLua, fs).CombinedOutput(); perr != nil {
		t.Logf("CLI `zfs program` cross-check skipped: %v\n%s", perr, prog)
	} else {
		t.Logf("CLI `zfs program` snapshot list:\n%s", prog)
		for _, snap := range want {
			if !strings.Contains(string(prog), snap) {
				t.Errorf("CLI zfs program output missing %s", snap)
			}
		}
	}

	// ---- Proof 2: Diff ----
	// Modify the filesystem between two snapshots: add, remove, modify, rename.
	run("zfs", "snapshot", fs+"@d1")
	if err := os.WriteFile(filepath.Join(mnt, "added.txt"), []byte("new\n"), 0644); err != nil {
		t.Fatalf("write added: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mnt, "keep.txt"), []byte("v1\n"), 0644); err != nil {
		t.Fatalf("write keep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mnt, "old.txt"), []byte("orig\n"), 0644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mnt, "gone.txt"), []byte("bye\n"), 0644); err != nil {
		t.Fatalf("write gone: %v", err)
	}
	run("zfs", "snapshot", fs+"@d2")
	// Now mutate for the d2->d3 diff: modify keep, rename old->renamed, rm gone.
	if err := os.WriteFile(filepath.Join(mnt, "keep.txt"), []byte("v2-modified\n"), 0644); err != nil {
		t.Fatalf("modify keep: %v", err)
	}
	if err := os.Rename(filepath.Join(mnt, "old.txt"), filepath.Join(mnt, "renamed.txt")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := os.Remove(filepath.Join(mnt, "gone.txt")); err != nil {
		t.Fatalf("remove gone: %v", err)
	}
	run("zfs", "snapshot", fs+"@d3")

	// OUR Diff(d2, d3).
	entries, err := h.Diff(fs+"@d2", fs+"@d3")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	ourDiff := map[string]DiffChange{}
	for _, e := range entries {
		base := filepath.Base(e.Path)
		ourDiff[base] = e.Change
		t.Logf("OUR Diff: %s %s %s (old=%s)", e.Change, base, e.Path, e.OldPath)
	}
	// Cross-check against `zfs diff`.
	cliDiff := run("zfs", "diff", fs+"@d2", fs+"@d3")
	t.Logf("CLI `zfs diff %s@d2 %s@d3`:\n%s", fs, fs, cliDiff)

	if c, ok := ourDiff["keep.txt"]; !ok || c != Modified {
		t.Errorf("Diff: keep.txt = %v, want M", c)
	}
	if c, ok := ourDiff["gone.txt"]; !ok || c != Removed {
		t.Errorf("Diff: gone.txt = %v, want -", c)
	}
	if c, ok := ourDiff["renamed.txt"]; !ok || c != Renamed {
		t.Errorf("Diff: renamed.txt = %v, want R", c)
	}
	// CLI markers should agree (R for rename, M for keep, - for gone).
	if !strings.Contains(cliDiff, "gone.txt") || !strings.Contains(cliDiff, "renamed.txt") ||
		!strings.Contains(cliDiff, "keep.txt") {
		t.Errorf("CLI diff missing expected paths")
	}

	// ---- Proof 3: UserSpace ----
	// chown files to two uids so userused@ has multiple entries.
	run("chown", "1000:1000", filepath.Join(mnt, "added.txt"))
	run("chown", "2000:2000", filepath.Join(mnt, "keep.txt"))
	run("zpool", "sync", pool)

	entriesU, err := h.UserSpace(fs, UserUsed)
	if err != nil {
		t.Fatalf("UserSpace: %v", err)
	}
	ourUsed := map[uint32]uint64{}
	for _, e := range entriesU {
		ourUsed[e.RID] = e.Value
		t.Logf("OUR UserSpace userused@%d = %d bytes (domain=%q)", e.RID, e.Value, e.Domain)
	}
	// Cross-check against `zfs userspace`.
	cliUS := run("zfs", "userspace", "-H", "-p", "-o", "name,used", fs)
	t.Logf("CLI `zfs userspace %s`:\n%s", fs, cliUS)
	for _, line := range strings.Split(strings.TrimSpace(cliUS), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// name is like "1000" / "POSIX User 1000" depending on resolution; parse trailing digits.
		name := fields[0]
		used, perr := strconv.ParseUint(fields[len(fields)-1], 10, 64)
		if perr != nil {
			continue
		}
		id, ierr := strconv.ParseUint(name, 10, 32)
		if ierr != nil {
			continue
		}
		if got := ourUsed[uint32(id)]; got != used {
			t.Errorf("UserSpace uid %d: OUR %d != CLI %d", id, got, used)
		}
	}
	if _, ok := ourUsed[1000]; !ok {
		t.Errorf("UserSpace: uid 1000 not reported")
	}

	// OUR UserSpaceByID convenience.
	if v, ok, err := h.UserSpaceByID(fs, UserUsed, 1000); err != nil || !ok {
		t.Errorf("UserSpaceByID(1000) = %d ok=%v err=%v", v, ok, err)
	}

	// ---- Proof 4: SetUserQuota ----
	if err := h.SetUserQuota(fs, UserQuota, "1000", 50<<20); err != nil {
		t.Fatalf("SetUserQuota: %v", err)
	}
	q := run("zfs", "get", "-H", "-p", "-o", "value", "userquota@1000", fs)
	t.Logf("OUR SetUserQuota(userquota@1000=50M); CLI `zfs get userquota@1000` = %s", strings.TrimSpace(q))
	if got := strings.TrimSpace(q); got != strconv.Itoa(50<<20) {
		t.Errorf("userquota@1000 = %s, want %d", got, 50<<20)
	}
}
