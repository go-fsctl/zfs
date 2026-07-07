// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

//go:build linux

package zfs

import "fmt"

// ChannelProgram runs the Lua channel program `script` against `pool` via
// ZFS_IOC_CHANNEL_PROGRAM (the libzfs_core lzc_channel_program / _nosync path).
// It returns the program's return value as the decoded outnvl["return"] nvlist.
//
// The input nvlist mirrors lzc_channel_program_impl():
//
//	{
//	  "program":    <string script>,
//	  "arg":        <nvlist args>,   // the Lua global `arg`; empty if none
//	  "sync":       <bool>,
//	  "instrlimit": <uint64>,        // Lua instruction cap
//	  "memlimit":   <uint64>,        // byte cap
//	}
//
// On success the kernel returns outnvl = { "return": <value> }. A Lua runtime
// error (the program called error(), failed an assert, etc.) comes back as the
// ioctl error ECHRNG with outnvl = { "error": <message/details> }, which is
// surfaced in the returned error. Mirrors OpenZFS 2.2.2 zfs_ioc_channel_program().
func (h *Handle) ChannelProgram(pool, script string, opts ChannelProgramOptions) (Nvlist, error) {
	if script == "" {
		return nil, fmt.Errorf("ChannelProgram: empty script")
	}
	instr := opts.InstrLimit
	if instr == 0 {
		instr = ZCP_DEFAULT_INSTRLIMIT
	}
	mem := opts.MemLimit
	if mem == 0 {
		mem = ZCP_DEFAULT_MEMLIMIT
	}
	// "arg" is a required key (DATA_TYPE_ANY): always supply a (possibly
	// empty) nvlist, matching libzfs which always adds argnvl.
	arg := opts.Args
	if arg == nil {
		arg = Nvlist{}
	}
	innvl := Nvlist{
		ZCP_ARG_PROGRAM:    script,
		ZCP_ARG_ARGLIST:    arg,
		ZCP_ARG_SYNC:       opts.Sync,
		ZCP_ARG_INSTRLIMIT: instr,
		ZCP_ARG_MEMLIMIT:   mem,
	}
	// A Lua runtime error comes back as the ioctl errno ECHRNG with the kernel
	// having filled the outnvl with an "error" entry; a syntax error comes back
	// as EINVAL similarly. callNewName decodes and returns that outnvl on the
	// dst-filled error paths, so the detail is available below.
	out, err := h.callNewName(ZFS_IOC_CHANNEL_PROGRAM, pool, innvl)
	if err != nil {
		if e, ok := out[ZCP_RET_ERROR]; ok {
			return out, fmt.Errorf("ZFS_IOC_CHANNEL_PROGRAM %q: %w: %v", pool, err, e)
		}
		return out, fmt.Errorf("ZFS_IOC_CHANNEL_PROGRAM %q: %w", pool, err)
	}
	if ret, ok := out[ZCP_RET_RETURN].(Nvlist); ok {
		return ret, nil
	}
	// A program with no (or a non-nvlist) return value yields an empty result.
	return Nvlist{}, nil
}

// ListSnapshotsZCP is a convenience wrapper that runs a small channel program
// returning the snapshots of dataset `fs` as a set, using the zcp built-in
// zfs.list.snapshots. It returns the snapshot names (full "pool/ds@snap").
// This demonstrates the read-only (nosync) channel-program path.
func (h *Handle) ListSnapshotsZCP(fs string) ([]string, error) {
	// The program builds an array-like table { [1]=name1, [2]=name2, ... }.
	// zcp return tables are encoded by the kernel as an nvlist whose keys are
	// the decimal indices; we collect the string values regardless of order.
	const script = `
args = ...
local r = {}
local i = 1
for s in zfs.list.snapshots(args["fs"]) do
    r[i] = s
    i = i + 1
end
return r
`
	out, err := h.ChannelProgram(poolOf(fs), script, ChannelProgramOptions{
		Sync: false,
		Args: Nvlist{"fs": fs},
	})
	if err != nil {
		return nil, fmt.Errorf("ListSnapshotsZCP %q: %w", fs, err)
	}
	return collectStringValues(out), nil
}
