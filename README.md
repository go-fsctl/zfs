# go-fsctl/zfs

Pure-Go `libzfs_core`: drive ZFS kernel operations via `/dev/zfs` ioctls — no
cgo, no `libzfs`, no shelling out to `zpool`/`zfs`/`zdb`.

This library talks to the live OpenZFS kernel module the same way OpenZFS's own
`libzfs_core` does: by opening `/dev/zfs` and issuing `ZFS_IOC_*` ioctls whose
payloads are `nvlist`s packed in the kernel's native, host-endian
(`NV_ENCODE_NATIVE`) wire format. That native encoding is distinct from the
XDR encoding used in on-disk vdev labels — this package implements the native
codec from scratch.

## Status

Targets **OpenZFS 2.2.x on Linux** (validated against 2.2.2, aarch64). The
`zfs_cmd_t` ABI and `ZFS_IOC_*` numbers are pinned to that release; see
`abi.go`.

## API

```go
h, err := zfs.Open()              // open("/dev/zfs")
defer h.Close()

// READ: list imported pools, decoding the kernel's config nvlist.
cfgs, err := h.PoolConfigs()      // map[string]zfs.Nvlist
names, err := h.PoolNames()

// WRITE: drive mutating ops via packed native nvlists.
err = h.Snapshot("tank", []string{"tank@snap1"})  // ZFS_IOC_SNAPSHOT
err = h.CreateFilesystem("tank/ds1")              // ZFS_IOC_CREATE

stats, err := h.ObjsetStats("tank/ds1")           // ZFS_IOC_OBJSET_STATS
```

The native nvlist codec is exported and usable on any platform:

```go
b, err := zfs.EncodeNative(zfs.Nvlist{"name": "tank", "version": uint64(5000)})
nv, err := zfs.DecodeNative(b)
```

## Implemented ioctls

| Operation            | ioctl                  | Direction      |
| -------------------- | ---------------------- | -------------- |
| List imported pools  | `ZFS_IOC_POOL_CONFIGS` | read (decode)  |
| Dataset properties   | `ZFS_IOC_OBJSET_STATS` | read (decode)  |
| Create snapshot(s)   | `ZFS_IOC_SNAPSHOT`     | write (encode) |
| Create filesystem    | `ZFS_IOC_CREATE`       | write (encode) |

## How it works

- **`nvlist.go`** — `NV_ENCODE_NATIVE` codec. The native format is the
  in-memory `nvpair_t`/`nvlist_t` layout copied verbatim, 8-byte aligned, with
  a 4-byte outer header `[encoding, endian, 0, 0]`, an 8-byte
  `[nvl_version, nvl_nvflag]` per-list header, and a 4-byte zero terminator.
  Mirrors OpenZFS `module/nvpair/nvpair.c` (`nvs_native_*`).
- **`abi.go`** — `ZFS_IOC_*` request numbers (Linux uses the raw `zfs_ioc_t`
  enum value, base `'Z'<<8 = 0x5a00`, as the ioctl cmd via the misc device —
  not `_IOWR`-encoded) and the `zfs_cmd_t` field offsets (`sizeof == 13744`).
- **`cmd_linux.go`** — `/dev/zfs` open + `ioctl` via
  `golang.org/x/sys/unix.Syscall(SYS_IOCTL, …)`, with the `zfs_cmd_t` held as a
  flat byte buffer accessed at exact offsets.
- **`zfs_linux.go`** — the typed public API.

## Testing

```sh
GOWORK=off go test ./...          # unit: native codec round-trip + ABI pinning
# integration tests auto-skip unless /dev/zfs is present (run in a ZFS guest):
ZFS_TEST_POOL=testpool sudo -E go test -run Integration -v ./...
```

## License

BSD-3-Clause. See [LICENSE](LICENSE).
