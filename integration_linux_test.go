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
	"strings"
	"testing"
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
