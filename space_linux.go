// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import (
	"fmt"
	"runtime"
	"unsafe"
)

// UserSpace reports the per-identity space accounting of filesystem `fs` for
// the given property via ZFS_IOC_USERSPACE_MANY (the same data `zfs userspace`
// and `zfs groupspace` display). prop selects user/group/project used-or-quota
// (bytes) or the object-count variants.
//
// This is a legacy zc-based ioctl: zc_name carries the filesystem,
// zc_objset_type the zfs_userquota_prop_t selector, zc_cookie a resumable ZAP
// cursor (in/out), and the kernel writes a packed array of zfs_useracct_t
// structs into zc_nvlist_dst (a raw struct buffer, NOT an nvlist), reporting
// the bytes filled in zc_nvlist_dst_size. We loop, advancing the cursor, until
// a call returns no entries. Mirrors the userland zfs_userspace() iteration.
func (h *Handle) UserSpace(fs string, prop SpaceProp) ([]SpaceEntry, error) {
	const bufSize = 64 * 1024 // holds ~240 zfs_useracct_t entries per call
	var out []SpaceEntry
	var cookie uint64
	for {
		cmd := &zfsCmd{}
		if err := cmd.setName(fs); err != nil {
			return nil, err
		}
		cmd.setU64(offZcObjsetType, uint64(prop))
		cmd.setU64(offZcCookie, cookie)

		buf := make([]byte, bufSize)
		cmd.setU64(offZcNvlistDst, uint64(uintptr(unsafe.Pointer(&buf[0]))))
		cmd.setU64(offZcNvlistDstSize, bufSize)
		noteDst(buf)

		err := h.ioctl(ZFS_IOC_USERSPACE_MANY, cmd)
		runtime.KeepAlive(buf)
		if err != nil {
			return nil, fmt.Errorf("ZFS_IOC_USERSPACE_MANY %q (%s): %w", fs, prop, err)
		}
		filled := cmd.getU64(offZcNvlistDstSize)
		n := int(filled / sizeofUseracct)
		if n == 0 {
			break
		}
		for i := 0; i < n; i++ {
			out = append(out, decodeUseracct(buf[i*sizeofUseracct:]))
		}
		// The cursor is updated in place; when it does not advance the ZAP is
		// exhausted (the next call would return 0 entries), but we rely on the
		// n==0 termination above and just carry the kernel's cursor forward.
		cookie = cmd.getU64(offZcCookie)
	}
	return out, nil
}

// decodeUseracct decodes one packed zfs_useracct_t (272 bytes) from b.
func decodeUseracct(b []byte) SpaceEntry {
	return SpaceEntry{
		Domain: cstr(b[offZuDomain : offZuDomain+useracctDomainN]),
		RID:    hostBO.Uint32(b[offZuRid : offZuRid+4]),
		Value:  hostBO.Uint64(b[offZuSpace : offZuSpace+8]),
	}
}

// SetUserQuota sets the quota property selected by prop for the identity `who`
// on filesystem `fs`, to `quota` (bytes for the byte props, an object count for
// the *OBJ* props; 0 removes the quota — "none"). `who` is a numeric uid/gid/
// project id (e.g. "1000") or a name the kernel can resolve. prop must be one
// of the settable *QUOTA variants; the *USED variants are read-only and are
// rejected.
//
// It routes through ZFS_IOC_SET_PROP with a single
// { "<prefix><who>": <uint64 quota> } property, exactly as `zfs set
// userquota@1000=10M` does (the kernel's property layer dispatches the
// userquota@-prefixed name to zfs_set_userquota).
func (h *Handle) SetUserQuota(fs string, prop SpaceProp, who string, quota uint64) error {
	switch prop {
	case UserQuota, GroupQuota, ProjectQuota,
		UserObjQuota, GroupObjQuota, ProjectObjQuota:
	default:
		return fmt.Errorf("SetUserQuota %q: %s is not a settable quota property", fs, prop)
	}
	if who == "" {
		return fmt.Errorf("SetUserQuota %q: empty identity", fs)
	}
	prefix, _ := prop.quotaPrefix()
	name := prefix + who
	if err := h.SetProp(fs, Nvlist{name: quota}); err != nil {
		return fmt.Errorf("SetUserQuota %q %s=%d: %w", fs, name, quota, err)
	}
	return nil
}

// UserSpaceByID is a convenience that returns UserSpace filtered to the single
// numeric identity id (a uid/gid/project id), or (0, false) when that id has no
// entry for the property. It avoids the caller scanning the slice for a uid.
func (h *Handle) UserSpaceByID(fs string, prop SpaceProp, id uint32) (uint64, bool, error) {
	entries, err := h.UserSpace(fs, prop)
	if err != nil {
		return 0, false, err
	}
	for _, e := range entries {
		if e.Domain == "" && e.RID == id {
			return e.Value, true, nil
		}
	}
	return 0, false, nil
}
