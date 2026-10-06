// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"bufio"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// TestIntegrationProvision walks what a storage provisioner does, against the
// live kernel and the already-imported ZFS_TEST_POOL (the CI kernel job's
// file-backed pool): create a dataset with a refquota, an owner tag and
// mountpoint=legacy in ONE ZFS_IOC_CREATE; read each back; mount it with
// mount(2); write incompressible data until the kernel answers EDQUOT near the
// refquota; unmount; destroy. It also checks the two kernel behaviours the
// API documents: a rejected property leaves NO dataset behind, and a user
// property is inherited by a child (reported with the parent as source).
//
//	ZFS_TEST_POOL=testpool sudo -E go test -run IntegrationProvision -v ./...
func TestIntegrationProvision(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()

	const (
		owner    = "fileshare:owner"
		refquota = 64 << 20
	)
	ds := testPool() + "/gofsctl_prov"
	child := ds + "/child"

	// Leftovers from an interrupted run.
	_ = h.Destroy(child, false)
	_ = h.Destroy(ds, false)

	// 1. A property the kernel rejects (refquota is a NUMBER, not a string):
	// the error names it and zfs_ioc_create has destroyed the dataset.
	err := h.CreateFilesystemWithProps(ds, Nvlist{owner: "x", "refquota": "64M"})
	if err == nil {
		_ = h.Destroy(ds, false)
		t.Fatal("CreateFilesystemWithProps accepted refquota as a string")
	}
	if !strings.Contains(err.Error(), "refquota") {
		t.Errorf("error does not name the rejected property: %v", err)
	}
	if _, serr := h.ObjsetStats(ds); !errors.Is(serr, unix.ENOENT) {
		t.Fatalf("after a rejected create, ObjsetStats = %v; want ENOENT (dataset left behind)", serr)
	}
	t.Logf("rejected create: %v (and no dataset left)", err)

	// 2. Create with props in one ioctl.
	props := Nvlist{
		"refquota":   uint64(refquota),
		owner:        "gofsctl-test",
		"mountpoint": ZFS_MOUNTPOINT_LEGACY,
		"canmount":   uint64(ZFS_CANMOUNT_NOAUTO),
	}
	if err := h.CreateFilesystemWithProps(ds, props); err != nil {
		t.Fatalf("CreateFilesystemWithProps: %v", err)
	}
	defer func() { _ = h.Destroy(ds, false) }()

	if q, err := h.Refquota(ds); err != nil || q != refquota {
		t.Errorf("Refquota = %d, %v; want %d", q, err, refquota)
	}
	if q, err := h.Quota(ds); err != nil || q != 0 {
		t.Errorf("Quota = %d, %v; want 0 (none)", q, err)
	}
	if v, src, err := h.UserProp(ds, owner); err != nil || v != "gofsctl-test" || src != ds {
		t.Errorf("UserProp = %q, %q, %v; want gofsctl-test set locally on %s", v, src, err, ds)
	}
	gp, err := h.GetProps(ds)
	if err != nil {
		t.Fatalf("GetProps: %v", err)
	}
	if gp["mountpoint"] != ZFS_MOUNTPOINT_LEGACY {
		t.Errorf("mountpoint = %v, want legacy", gp["mountpoint"])
	}
	if gp["canmount"] != uint64(ZFS_CANMOUNT_NOAUTO) {
		t.Errorf("canmount = %v, want %d", gp["canmount"], ZFS_CANMOUNT_NOAUTO)
	}

	// The typed setters round-trip.
	if err := h.SetQuota(ds, 2*refquota); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	if q, err := h.Quota(ds); err != nil || q != 2*refquota {
		t.Errorf("Quota after SetQuota = %d, %v", q, err)
	}
	if err := h.SetQuota(ds, 0); err != nil {
		t.Fatalf("SetQuota(0): %v", err)
	}
	if err := h.SetUserProp(ds, "fileshare:share", "s1"); err != nil {
		t.Fatalf("SetUserProp: %v", err)
	}
	if v, _, err := h.UserProp(ds, "fileshare:share"); err != nil || v != "s1" {
		t.Errorf("UserProp after SetUserProp = %q, %v", v, err)
	}
	if _, _, err := h.UserProp(ds, "fileshare:absent"); !errors.Is(err, ErrPropNotSet) {
		t.Errorf("absent user prop: %v; want ErrPropNotSet", err)
	}

	// 3. User properties are inherited: a child created WITHOUT the tag reports
	// the parent's value, with the parent as its source.
	if err := h.CreateFilesystem(child); err != nil {
		t.Fatalf("CreateFilesystem child: %v", err)
	}
	if v, src, err := h.UserProp(child, owner); err != nil || v != "gofsctl-test" || src != ds {
		t.Errorf("child UserProp = %q, %q, %v; want inherited from %s", v, src, err, ds)
	}
	if err := h.Destroy(child, false); err != nil {
		t.Fatalf("Destroy child: %v", err)
	}

	// 4. Mount where WE choose, write until EDQUOT, unmount.
	mnt := t.TempDir()
	if err := Mount(ds, mnt, unix.MS_NOSUID|unix.MS_NODEV, ""); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	mounted := true
	defer func() {
		if mounted {
			_ = Unmount(mnt, unix.MNT_DETACH)
		}
	}()
	if !mountedAt(t, ds, mnt) {
		t.Fatalf("%s not listed in /proc/self/mountinfo at %s", ds, mnt)
	}

	written, werr := fillUntilError(filepath.Join(mnt, "fill"), 4*refquota)
	t.Logf("wrote %d bytes (refquota %d) before: %v", written, refquota, werr)
	if !errors.Is(werr, unix.EDQUOT) {
		t.Errorf("write stopped with %v, want EDQUOT", werr)
	}
	// ZFS enforces refquota against space reserved per transaction group, so
	// the stop is near, not at, the limit. Generous bounds still catch both a
	// limit that was ignored and one applied at the wrong scale.
	if written < refquota/2 || written > 2*refquota {
		t.Errorf("EDQUOT after %d bytes, not near refquota %d", written, refquota)
	}

	if err := Unmount(mnt, 0); err != nil {
		t.Fatalf("Unmount: %v", err)
	}
	mounted = false
	if mountedAt(t, ds, mnt) {
		t.Errorf("%s still mounted at %s after Unmount", ds, mnt)
	}

	// 5. Destroy (the deferred Destroy then fails harmlessly).
	if err := h.Destroy(ds, false); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, err := h.ObjsetStats(ds); !errors.Is(err, unix.ENOENT) {
		t.Errorf("after Destroy, ObjsetStats = %v; want ENOENT", err)
	}
}

// fillUntilError appends 1 MiB blocks of random (incompressible: the pool's
// default compression would otherwise store zeros as nothing) data to path,
// fsyncing each, until a write or fsync fails or limit bytes are written.
func fillUntilError(path string, limit int) (int, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	written := 0
	for written < limit {
		if _, err := rand.Read(buf); err != nil {
			return written, err
		}
		n, err := f.Write(buf)
		written += n
		if err != nil {
			return written, err
		}
		if err := f.Sync(); err != nil {
			return written, err
		}
	}
	return written, nil
}

// mountedAt reports whether /proc/self/mountinfo lists a zfs mount of ds at
// mnt. Fields: ... (5) mount point ... " - " fstype source superopts.
func mountedAt(t *testing.T, ds, mnt string) bool {
	t.Helper()
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		t.Fatalf("mountinfo: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		pre, post, ok := strings.Cut(sc.Text(), " - ")
		if !ok {
			continue
		}
		a, b := strings.Fields(pre), strings.Fields(post)
		if len(a) >= 5 && len(b) >= 2 && a[4] == mnt && b[0] == "zfs" && b[1] == ds {
			return true
		}
	}
	return false
}
