// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"fmt"
	"strings"
)

// LoadKey loads the wrapping key for the encrypted filesystem `fs` (or zvol)
// via ZFS_IOC_LOAD_KEY (the libzfs_core lzc_load_key path), making its
// keystatus "available" so the dataset can be mounted/opened. `key` is the raw
// wrapping-key material: for keyformat=raw it is exactly 32 bytes; for
// keyformat=passphrase/hex it is the bytes the kernel will validate against the
// stored key. When `nomount` is true the call is a dry-run that only verifies
// the key (the libzfs_core "noop" flag) without leaving it loaded.
//
// The input nvlist mirrors lzc_load_key(fsname, noop, wkeydata, wkeylen):
//
//	{
//	  "hidden_args": { "wkeydata": <uint8[] key> },
//	  "noop":        <boolean>   // present only when nomount/dry-run
//	}
//
// zc_name carries the filesystem. Mirrors OpenZFS 2.2.2 zfs_ioc_load_key().
func (h *Handle) LoadKey(fs string, key []byte, nomount bool) error {
	if len(key) == 0 {
		return fmt.Errorf("LoadKey %q: empty key", fs)
	}
	innvl := Nvlist{ZPOOL_HIDDEN_ARGS: hiddenArgs(key)}
	if nomount {
		// lzc adds a bare DATA_TYPE_BOOLEAN "noop" (presence-only).
		innvl["noop"] = Boolean{}
	}
	if _, err := h.callNewName(ZFS_IOC_LOAD_KEY, fs, innvl); err != nil {
		return fmt.Errorf("ZFS_IOC_LOAD_KEY %q: %w", fs, err)
	}
	return nil
}

// UnloadKey unloads the wrapping key for the encrypted filesystem `fs` via
// ZFS_IOC_UNLOAD_KEY (the libzfs_core lzc_unload_key path), making its
// keystatus "unavailable". The dataset must be unmounted first (the kernel
// returns EBUSY otherwise). No input nvlist is required. Mirrors OpenZFS 2.2.2
// zfs_ioc_unload_key().
func (h *Handle) UnloadKey(fs string) error {
	if _, err := h.callNewName(ZFS_IOC_UNLOAD_KEY, fs, nil); err != nil {
		return fmt.Errorf("ZFS_IOC_UNLOAD_KEY %q: %w", fs, err)
	}
	return nil
}

// ChangeKey changes the wrapping key (and/or the key-related properties) of the
// encrypted filesystem `fs` via ZFS_IOC_CHANGE_KEY (the libzfs_core
// lzc_change_key path). The key must already be loaded. `newKey` is the new raw
// wrapping-key material (32 bytes for keyformat=raw). When changing the key
// material, `props` MUST carry the "keyformat" (numeric, e.g.
// uint64(ZFS_KEYFORMAT_RAW)) and typically "keylocation" — the kernel
// re-validates the new key against the keyformat and returns EINVAL if it is
// absent. The dataset becomes its own encryption root
// (crypt_cmd = DCP_CMD_NEW_KEY).
//
// The input nvlist mirrors lzc_change_key(fsname, crypt_cmd, props, wkeydata,
// wkeylen):
//
//	{
//	  "crypt_cmd":   <uint64 DCP_CMD_NEW_KEY>,
//	  "hidden_args": { "wkeydata": <uint8[] newKey> },  // when newKey != nil
//	  "props":       { ... }                            // when props != nil
//	}
//
// zc_name carries the filesystem. Mirrors OpenZFS 2.2.2 zfs_ioc_change_key().
func (h *Handle) ChangeKey(fs string, newKey []byte, props Nvlist) error {
	innvl := Nvlist{"crypt_cmd": uint64(DCP_CMD_NEW_KEY)}
	if len(newKey) > 0 {
		innvl[ZPOOL_HIDDEN_ARGS] = hiddenArgs(newKey)
	}
	if len(props) > 0 {
		innvl["props"] = props
	}
	if _, err := h.callNewName(ZFS_IOC_CHANGE_KEY, fs, innvl); err != nil {
		return fmt.Errorf("ZFS_IOC_CHANGE_KEY %q: %w", fs, err)
	}
	return nil
}

// CreateEncrypted creates a new encrypted ZFS filesystem `name` via
// ZFS_IOC_CREATE, supplying the wrapping key in the hidden-args channel. It is
// the encryption-aware counterpart of CreateFilesystem and mirrors
// lzc_create(fsname, LZC_DATSET_TYPE_ZFS, props, wkeydata, wkeylen).
//
// `key` is the raw wrapping-key material — exactly WRAPPING_KEY_LEN (32) bytes
// for keyformat=raw. `props` MUST declare "encryption" and "keyformat" as
// NUMERIC enum values (e.g. uint64(ZIO_CRYPT_AES_256_GCM) and
// uint64(ZFS_KEYFORMAT_RAW)); unlike ordinary string properties, the kernel's
// dsl_crypto_params_create_nvlist reads these with nvlist_lookup_uint64 and
// returns EINVAL for a string. "keylocation" is read as a string (e.g.
// "prompt", the default). The kernel reads the key from the nested hidden_args
// nvlist as a DATA_TYPE_UINT8_ARRAY.
//
// The input nvlist mirrors lzc_create:
//
//	{
//	  "type":        <int32 DMU_OST_ZFS>,
//	  "props":       { "encryption": <uint64>, "keyformat": <uint64>, ... },
//	  "hidden_args": { "wkeydata": <uint8[] key> },
//	}
//
// zc_name carries the new dataset name. Mirrors OpenZFS 2.2.2 zfs_ioc_create().
func (h *Handle) CreateEncrypted(name string, key []byte, props Nvlist) error {
	if len(key) == 0 {
		return fmt.Errorf("CreateEncrypted %q: empty key", name)
	}
	if len(props) == 0 {
		return fmt.Errorf("CreateEncrypted %q: props must declare encryption/keyformat", name)
	}
	return h.createWithKey(name, DMU_OST_ZFS, props, key)
}

// Promote promotes the clone `cloneFs` so that it becomes the origin of the
// filesystem it was cloned from, via ZFS_IOC_PROMOTE (legacy ioctl). After the
// promotion the origin snapshots move to `cloneFs` and the former parent's
// "origin" property points into `cloneFs`; the clone no longer depends on the
// original and the original can be destroyed independently. The promoted clone
// must not have a snapshot whose name collides with a promoted one (the kernel
// reports the conflicting snapshot name in zc_string and returns EEXIST).
//
// Only zc_name (= cloneFs) is consulted on input. Mirrors OpenZFS 2.2.2
// zfs_ioc_promote().
func (h *Handle) Promote(cloneFs string) error {
	if strings.ContainsAny(cloneFs, "@#") {
		return fmt.Errorf("Promote: %q is not a filesystem", cloneFs)
	}
	cmd := &zfsCmd{}
	if err := cmd.setName(cloneFs); err != nil {
		return err
	}
	if err := h.ioctl(ZFS_IOC_PROMOTE, cmd); err != nil {
		// On EEXIST the kernel leaves the conflicting snapshot short-name in
		// zc_string; surface it to help the caller.
		if conflict := cstr(cmd.buf[offZcString : offZcString+maxNameLen]); conflict != "" {
			return fmt.Errorf("ZFS_IOC_PROMOTE %q: conflicting snapshot %q: %w", cloneFs, conflict, err)
		}
		return fmt.Errorf("ZFS_IOC_PROMOTE %q: %w", cloneFs, err)
	}
	return nil
}

// Inherit clears the local setting of property `prop` on dataset `fs` so that
// it reverts to being inherited from the parent (or, for received properties
// with received=true, reverts to the value that was received), via
// ZFS_IOC_INHERIT_PROP (legacy ioctl). It is the ioctl behind `zfs inherit`.
//
// zc_name carries the dataset, zc_value the property name, and zc_cookie the
// "received" flag (1 == revert to the received value, matching `zfs inherit
// -S`). Mirrors OpenZFS 2.2.2 zfs_ioc_inherit_prop().
func (h *Handle) Inherit(fs, prop string, received bool) error {
	if prop == "" {
		return fmt.Errorf("Inherit %q: empty property name", fs)
	}
	cmd := &zfsCmd{}
	if err := cmd.setName(fs); err != nil {
		return err
	}
	if err := cmd.setValue(prop); err != nil {
		return err
	}
	if received {
		cmd.setU64(offZcCookie, 1)
	}
	if err := h.ioctl(ZFS_IOC_INHERIT_PROP, cmd); err != nil {
		return fmt.Errorf("ZFS_IOC_INHERIT_PROP %q prop %q: %w", fs, prop, err)
	}
	return nil
}
