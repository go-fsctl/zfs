// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import (
	"reflect"
	"testing"
)

// TestVdevNvlistFileLeaf verifies the config nvlist a single-file-vdev pool
// produces, matching the shape ZFS_IOC_POOL_CREATE expects (root vdev with a
// "file" child carrying type/path/is_log).
func TestVdevNvlistFileLeaf(t *testing.T) {
	root := Vdev{
		Type: VDEV_TYPE_ROOT,
		Children: []Vdev{
			{Type: VDEV_TYPE_FILE, Path: "/tmp/disk0.img"},
		},
	}
	nv, err := root.nvlist()
	if err != nil {
		t.Fatalf("nvlist: %v", err)
	}
	if nv[ZPOOL_CONFIG_TYPE] != VDEV_TYPE_ROOT {
		t.Errorf("root type = %v", nv[ZPOOL_CONFIG_TYPE])
	}
	kids, ok := nv[ZPOOL_CONFIG_CHILDREN].([]Nvlist)
	if !ok || len(kids) != 1 {
		t.Fatalf("children = %#v", nv[ZPOOL_CONFIG_CHILDREN])
	}
	leaf := kids[0]
	if leaf[ZPOOL_CONFIG_TYPE] != VDEV_TYPE_FILE {
		t.Errorf("leaf type = %v", leaf[ZPOOL_CONFIG_TYPE])
	}
	if leaf[ZPOOL_CONFIG_PATH] != "/tmp/disk0.img" {
		t.Errorf("leaf path = %v", leaf[ZPOOL_CONFIG_PATH])
	}
	if leaf[ZPOOL_CONFIG_IS_LOG] != uint64(0) {
		t.Errorf("leaf is_log = %v, want uint64(0)", leaf[ZPOOL_CONFIG_IS_LOG])
	}
}

// TestVdevNvlistMirror checks a mirror of two files round-trips through the
// native codec (proving the embedded-nvlist-array encoding of children).
func TestVdevNvlistMirror(t *testing.T) {
	root := Vdev{
		Type: VDEV_TYPE_ROOT,
		Children: []Vdev{{
			Type: VDEV_TYPE_MIRROR,
			Children: []Vdev{
				{Type: VDEV_TYPE_FILE, Path: "/tmp/a.img"},
				{Type: VDEV_TYPE_FILE, Path: "/tmp/b.img"},
			},
		}},
	}
	nv, err := root.nvlist()
	if err != nil {
		t.Fatalf("nvlist: %v", err)
	}
	cfg := Nvlist{
		ZPOOL_CONFIG_VERSION:   uint64(SPA_VERSION),
		ZPOOL_CONFIG_POOL_NAME: "p",
		ZPOOL_CONFIG_POOL_GUID: uint64(0x1234),
		ZPOOL_CONFIG_VDEV_TREE: nv,
	}
	b, err := EncodeNative(cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := DecodeNative(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(cfg, out) {
		t.Fatalf("round-trip mismatch:\n in=%#v\nout=%#v", cfg, out)
	}
}

func TestVdevNvlistErrors(t *testing.T) {
	if _, err := (Vdev{Type: VDEV_TYPE_FILE}).nvlist(); err == nil {
		t.Error("file vdev without path: expected error")
	}
	if _, err := (Vdev{Type: VDEV_TYPE_ROOT}).nvlist(); err == nil {
		t.Error("root vdev without children: expected error")
	}
	if _, err := (Vdev{}).nvlist(); err == nil {
		t.Error("empty type: expected error")
	}
}

// TestFlattenProps confirms the {value,source} unwrapping used by GetProps /
// PoolGetProps.
func TestFlattenProps(t *testing.T) {
	in := Nvlist{
		"compression": Nvlist{"value": "lz4", "source": "local"},
		"used":        Nvlist{"value": uint64(24576)},
		"name":        "testpool/ds", // passthrough scalar
	}
	out := flattenProps(in)
	if out["compression"] != "lz4" {
		t.Errorf("compression = %v, want lz4", out["compression"])
	}
	if out["used"] != uint64(24576) {
		t.Errorf("used = %v", out["used"])
	}
	if out["name"] != "testpool/ds" {
		t.Errorf("name = %v", out["name"])
	}
}
