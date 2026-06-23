// SPDX-License-Identifier: BSD-3-Clause
//go:build linux

package zfs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestProbeDiffRaw dumps the raw diff record ranges and per-object stats so we
// can see exactly what the kernel streams for a known add/rm/rename/modify.
func TestProbeDiffRaw(t *testing.T) {
	h := requireKernel(t)
	defer h.Close()
	dir := "/var/tmp"
	const pool = "gofsctl_probe"
	img := filepath.Join(dir, "gofsctl_probe_d0.img")
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
	f, _ := os.OpenFile(img, os.O_RDWR|os.O_CREATE, 0600)
	f.Truncate(512 << 20)
	f.Close()
	defer os.Remove(img)
	run("/usr/sbin/zpool", "create", "-f", pool, img)
	defer exec.Command("/usr/sbin/zpool", "destroy", pool).Run()

	fs := pool + "/fsa"
	run("/usr/sbin/zfs", "create", fs)
	mnt := "/" + fs

	os.WriteFile(filepath.Join(mnt, "keep.txt"), []byte("v1\n"), 0644)
	os.WriteFile(filepath.Join(mnt, "old.txt"), []byte("orig\n"), 0644)
	os.WriteFile(filepath.Join(mnt, "gone.txt"), []byte("bye\n"), 0644)
	run("/usr/sbin/zfs", "snapshot", fs+"@d2")
	os.WriteFile(filepath.Join(mnt, "keep.txt"), []byte("v2-modified\n"), 0644)
	os.Rename(filepath.Join(mnt, "old.txt"), filepath.Join(mnt, "renamed.txt"))
	os.Remove(filepath.Join(mnt, "gone.txt"))
	run("/usr/sbin/zfs", "snapshot", fs+"@d3")

	// Reproduce the Diff pipeline but dump raw ranges.
	fromSnap := fs + "@d2"
	toSnap := fs + "@d3"
	recs, err := h.probeDiffRanges(fromSnap, toSnap)
	if err != nil {
		t.Fatalf("probeDiffRanges: %v", err)
	}
	t.Logf("=== %d raw diff ranges ===", len(recs))
	for _, dr := range recs {
		t.Logf("range type=%#x first=%d last=%d", dr.typ, dr.first, dr.last)
		if dr.typ == DDR_INUSE {
			for o := dr.first; o <= dr.last; o++ {
				fsb, ferr := h.objToStats(fromSnap, o)
				tsb, terr := h.objToStats(toSnap, o)
				t.Logf("  obj %d: FROM{present=%v path=%q gen=%d mode=%#o links=%d ct=%d.%d err=%v} TO{present=%v path=%q gen=%d mode=%#o links=%d ct=%d.%d err=%v}",
					o, fsb.present, fsb.path, fsb.gen, fsb.mode, fsb.links, fsb.ctime0, fsb.ctime1, ferr,
					tsb.present, tsb.path, tsb.gen, tsb.mode, tsb.links, tsb.ctime0, tsb.ctime1, terr)
			}
		}
	}
	entries, _ := h.Diff(fromSnap, toSnap)
	t.Logf("=== OUR Diff returned %d entries ===", len(entries))
	for _, e := range entries {
		t.Logf("  %s %q old=%q obj=%d type=%c", e.Change, e.Path, e.OldPath, e.Object, byte(e.Type))
	}
}

// probeDiffRanges runs the ZFS_IOC_DIFF ioctl and returns the raw ranges.
func (h *Handle) probeDiffRanges(fromSnap, toSnap string) ([]diffRange, error) {
	rPipe, wPipe, err := osPipe()
	if err != nil {
		return nil, err
	}
	done := make(chan struct {
		recs []diffRange
		err  error
	}, 1)
	go func() {
		recs, rerr := readDiffRanges(rPipe)
		_ = rPipe.Close()
		done <- struct {
			recs []diffRange
			err  error
		}{recs, rerr}
	}()
	cmd := &zfsCmd{}
	cmd.setName(toSnap)
	cmd.setValue(fromSnap)
	cmd.setU64(offZcCookie, uint64(wPipe.Fd()))
	ioErr := h.ioctl(ZFS_IOC_DIFF, cmd)
	_ = wPipe.Close()
	res := <-done
	if ioErr != nil {
		return nil, ioErr
	}
	return res.recs, res.err
}
