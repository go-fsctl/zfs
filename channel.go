// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

// ChannelProgramOptions controls a ZFS channel-program (zcp) run. Zero-valued
// fields fall back to the kernel defaults (sync=true, the ZCP_DEFAULT_* limits).
type ChannelProgramOptions struct {
	// Sync, when true, runs the program in a sync task so its changes are
	// committed atomically before the ioctl returns (the lzc_channel_program
	// path). When false the program runs read-only / non-syncing (the
	// lzc_channel_program_nosync path) and may not make persistent changes.
	Sync bool

	// InstrLimit caps the number of Lua instructions the program may execute
	// (0 = ZCP_DEFAULT_INSTRLIMIT). The kernel rejects 0-on-the-wire and any
	// value above ZCP_MAX_INSTRLIMIT with EINVAL, so 0 here means "default".
	InstrLimit uint64

	// MemLimit caps the program's memory use in bytes (0 = ZCP_DEFAULT_MEMLIMIT).
	MemLimit uint64

	// Args is the Lua argument table, passed to the program as the global
	// `arg`. It may be nil/empty (an empty table is supplied). Values are
	// ordinary nvlist values (uint64, int64, string, bool, nested Nvlist,
	// arrays); the kernel hands the whole nvlist to the interpreter verbatim.
	Args Nvlist
}

// collectStringValues returns every string value in nv (ignoring keys/order).
// zcp array-style return tables come back as an nvlist keyed by decimal index;
// callers that only want the values (e.g. a list of snapshot names) use this.
func collectStringValues(nv Nvlist) []string {
	out := make([]string, 0, len(nv))
	for _, v := range nv {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
