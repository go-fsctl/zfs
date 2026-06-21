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

// POOL lifecycle (pure-Go pool creation — no libzfs, no zpool(8)):
root := zfs.Vdev{Type: zfs.VDEV_TYPE_ROOT, Children: []zfs.Vdev{
    {Type: zfs.VDEV_TYPE_FILE, Path: "/var/tmp/disk0.img"},
}}
err = h.PoolCreate("tank", root, nil)             // ZFS_IOC_POOL_CREATE
cfgs, err := h.PoolConfigs()                      // ZFS_IOC_POOL_CONFIGS
pp, err := h.PoolGetProps("tank")                 // ZFS_IOC_POOL_GET_PROPS
err = h.PoolExport("tank", false, false)          // ZFS_IOC_POOL_EXPORT
_, err = h.PoolImport("tank", cfgs["tank"])       // ZFS_IOC_POOL_IMPORT
err = h.PoolDestroy("tank")                       // ZFS_IOC_POOL_DESTROY

// DATASET lifecycle:
err = h.CreateFilesystem("tank/ds1")              // ZFS_IOC_CREATE
err = h.SetProp("tank/ds1", zfs.Nvlist{"quota": uint64(64 << 20)}) // ZFS_IOC_SET_PROP
props, err := h.GetProps("tank/ds1")              // ZFS_IOC_OBJSET_STATS (flattened)
err = h.Rename("tank/ds1", "tank/ds2", false)     // ZFS_IOC_RENAME
err = h.Snapshot("tank", []string{"tank/ds2@s1"}) // ZFS_IOC_SNAPSHOT
err = h.Destroy("tank/ds2@s1", false)             // ZFS_IOC_DESTROY
err = h.Destroy("tank/ds2", false)                // ZFS_IOC_DESTROY

// SEND / RECEIVE (replication). The kernel writes/reads the DMU replay stream
// to/from the file descriptor; the library only drives the ioctl + nvlist.
out, _ := os.Create("snap.stream")
err = h.Send("tank/ds2@s1", out, zfs.SendOptions{})              // ZFS_IOC_SEND_NEW
// incremental: zfs.SendOptions{FromSnap: "s0", LargeBlocks: true, Compress: true}

in, _ := os.Open("snap.stream")
br, err := h.Receive("tank/restored@s1", in, zfs.RecvOptions{})  // ZFS_IOC_RECV_NEW
// br.ToName / br.ToGuid / br.Type come from the stream's DRR_BEGIN record.
```

`SetProp` takes the kernel's native value type per property: a `uint64` enum
index for `INDEX` properties (e.g. `compression`, `atime`) and `NUMBER`
properties (e.g. `quota`), or a `string` for `STRING`-typed properties — the
same conversion the `zpool`/`zfs` CLI performs before the ioctl. Enabling a
feature-gated value (e.g. `compression=lz4`) requires that feature to be
enabled on the pool at creation time.

The native nvlist codec is exported and usable on any platform:

```go
b, err := zfs.EncodeNative(zfs.Nvlist{"name": "tank", "version": uint64(5000)})
nv, err := zfs.DecodeNative(b)
```

## Implemented ioctls

| Operation            | ioctl                   | Direction      |
| -------------------- | ----------------------- | -------------- |
| List imported pools  | `ZFS_IOC_POOL_CONFIGS`  | read (decode)  |
| Pool properties      | `ZFS_IOC_POOL_GET_PROPS`| read (decode)  |
| Dataset properties   | `ZFS_IOC_OBJSET_STATS`  | read (decode)  |
| Create pool          | `ZFS_IOC_POOL_CREATE`   | write (encode) |
| Export pool          | `ZFS_IOC_POOL_EXPORT`   | write          |
| Import pool          | `ZFS_IOC_POOL_IMPORT`   | write (encode) |
| Destroy pool         | `ZFS_IOC_POOL_DESTROY`  | write          |
| Create snapshot(s)   | `ZFS_IOC_SNAPSHOT`      | write (encode) |
| Create filesystem    | `ZFS_IOC_CREATE`        | write (encode) |
| Set properties       | `ZFS_IOC_SET_PROP`      | write (encode) |
| Rename dataset       | `ZFS_IOC_RENAME`        | write          |
| Destroy dataset/snap | `ZFS_IOC_DESTROY`       | write          |
| Send (replication)   | `ZFS_IOC_SEND_NEW`      | write (fd+nvl) |
| Receive (replication)| `ZFS_IOC_RECV_NEW`      | write (fd+nvl) |

`PoolCreate` packs the bare root vdev tree (`{type:"root", children:[…]}`) into
`zc_nvlist_conf` — exactly what the kernel hands to `spa_create()` as its
`nvroot`. `PoolImport` takes a full pool config (carrying `pool_guid` + the
vdev tree), e.g. one captured from `PoolConfigs` while the pool is still
imported. `PoolTryImport` is wired to `ZFS_IOC_POOL_TRYIMPORT` but the kernel
requires a tryconfig already assembled from on-disk vdev labels; decoding the
XDR on-disk label is not yet implemented, so it takes a caller-supplied config.

## How it works

- **`nvlist.go`** — `NV_ENCODE_NATIVE` codec. The native format is the
  in-memory `nvpair_t`/`nvlist_t` layout copied verbatim, 8-byte aligned, with
  a 4-byte outer header `[encoding, endian, 0, 0]`, an 8-byte
  `[nvl_version, nvl_nvflag]` per-list header, and a 4-byte zero terminator.
  Mirrors OpenZFS `module/nvpair/nvpair.c` (`nvs_native_*`).
- **`abi.go`** — `ZFS_IOC_*` request numbers (Linux uses the raw `zfs_ioc_t`
  enum value, base `'Z'<<8 = 0x5a00`, as the ioctl cmd via the misc device —
  not `_IOWR`-encoded), the `ZPOOL_CONFIG_*`/`VDEV_TYPE_*` config keys, and the
  `zfs_cmd_t` field offsets (`sizeof == 13744`).
- **`cmd_linux.go`** — `/dev/zfs` open + `ioctl` via
  `golang.org/x/sys/unix.Syscall(SYS_IOCTL, …)`, with the `zfs_cmd_t` held as a
  flat byte buffer accessed at exact offsets.
- **`zfs_linux.go`** — read paths + filesystem/snapshot create.
- **`pool_linux.go`** — pool lifecycle (create/destroy/export/import).
- **`dataset_linux.go`** — dataset destroy/rename/set-prop/get-props.
- **`send_recv_linux.go`** — `Send`/`Receive` over the `lzc_send`/`lzc_receive`
  "new-style" ioctl ABI (`zc_name` + packed input nvlist in `zc_nvlist_src` +
  output nvlist in `zc_nvlist_dst`). `Send` passes the output fd and feature
  flags in the input nvlist; the kernel writes the DMU replay stream to the fd.
  `Receive` reads the leading 312-byte `dmu_replay_record_t` (the `DRR_BEGIN`
  record) from the stream, passes it as a `byte_array` plus the input fd, and
  the kernel consumes the rest of the stream. Stream bytes are never produced
  or parsed in Go. `send_recv.go` holds the platform-neutral option/record
  types and `DRR_BEGIN` decoding.
- **`vdev.go`** — platform-neutral `Vdev` tree → config nvlist rendering.

## Testing

```sh
GOWORK=off go test ./...          # unit: native codec round-trip + ABI pinning
# integration tests auto-skip unless /dev/zfs is present (run in a ZFS guest):
ZFS_TEST_POOL=testpool sudo -E go test -run Integration -v ./...
```

## License

BSD-3-Clause. See [LICENSE](LICENSE).
