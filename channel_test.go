// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

import "testing"

// TestChannelProgramKeys pins the channel-program innvl/outnvl key strings and
// default limits against the guest's ZCP_* macros.
func TestChannelProgramKeys(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{ZCP_ARG_PROGRAM, "program"},
		{ZCP_ARG_ARGLIST, "arg"},
		{ZCP_ARG_SYNC, "sync"},
		{ZCP_ARG_INSTRLIMIT, "instrlimit"},
		{ZCP_ARG_MEMLIMIT, "memlimit"},
		{ZCP_RET_RETURN, "return"},
		{ZCP_RET_ERROR, "error"},
	} {
		if c.got != c.want {
			t.Errorf("key = %q, want %q", c.got, c.want)
		}
	}
	if ZCP_DEFAULT_INSTRLIMIT != 10*1000*1000 {
		t.Errorf("instrlimit default = %d", ZCP_DEFAULT_INSTRLIMIT)
	}
	if ZCP_DEFAULT_MEMLIMIT != 10*1024*1024 {
		t.Errorf("memlimit default = %d", ZCP_DEFAULT_MEMLIMIT)
	}
}

// TestChannelProgramInnvlRoundTrip builds the innvl the ChannelProgram path
// sends and round-trips it through the native codec, asserting the kernel would
// see exactly the keys/types lzc_channel_program_impl emits.
func TestChannelProgramInnvlRoundTrip(t *testing.T) {
	args := Nvlist{"fs": "tank/ds", "n": uint64(3)}
	innvl := Nvlist{
		ZCP_ARG_PROGRAM:    "return 1",
		ZCP_ARG_ARGLIST:    args,
		ZCP_ARG_SYNC:       true,
		ZCP_ARG_INSTRLIMIT: uint64(ZCP_DEFAULT_INSTRLIMIT),
		ZCP_ARG_MEMLIMIT:   uint64(ZCP_DEFAULT_MEMLIMIT),
	}
	b, err := EncodeNative(innvl)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := DecodeNative(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got[ZCP_ARG_PROGRAM] != "return 1" {
		t.Errorf("program = %v", got[ZCP_ARG_PROGRAM])
	}
	if got[ZCP_ARG_SYNC] != true {
		t.Errorf("sync = %v (want bool true)", got[ZCP_ARG_SYNC])
	}
	if got[ZCP_ARG_INSTRLIMIT] != uint64(ZCP_DEFAULT_INSTRLIMIT) {
		t.Errorf("instrlimit = %v", got[ZCP_ARG_INSTRLIMIT])
	}
	sub, ok := got[ZCP_ARG_ARGLIST].(Nvlist)
	if !ok {
		t.Fatalf("arg is %T, want Nvlist", got[ZCP_ARG_ARGLIST])
	}
	if sub["fs"] != "tank/ds" || sub["n"] != uint64(3) {
		t.Errorf("arg contents = %v", sub)
	}
}

func TestCollectStringValues(t *testing.T) {
	nv := Nvlist{"1": "tank@a", "2": "tank@b", "x": uint64(9)}
	got := collectStringValues(nv)
	if len(got) != 2 {
		t.Fatalf("got %d strings, want 2 (%v)", len(got), got)
	}
	seen := map[string]bool{}
	for _, s := range got {
		seen[s] = true
	}
	if !seen["tank@a"] || !seen["tank@b"] {
		t.Errorf("missing expected snapshots: %v", got)
	}
}
