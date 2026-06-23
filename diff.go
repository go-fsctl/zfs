// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, go-fsctl

package zfs

// DiffChange is the single-character change type of a DiffEntry, matching the
// markers `zfs diff` prints.
type DiffChange byte

const (
	// Added marks a path that exists only in the "to" snapshot ('+').
	Added DiffChange = '+'
	// Removed marks a path that exists only in the "from" snapshot ('-').
	Removed DiffChange = '-'
	// Modified marks a path present in both whose contents/metadata changed ('M').
	Modified DiffChange = 'M'
	// Renamed marks a path (object) that moved between the snapshots ('R').
	Renamed DiffChange = 'R'
)

// String renders the change marker as a one-character string.
func (c DiffChange) String() string { return string(byte(c)) }

// FileType classifies the object behind a DiffEntry, derived from the stat mode
// bits. It mirrors the type characters `zfs diff -F` prints (F file, / dir, etc.).
type FileType byte

const (
	// TypeUnknown is an unrecognized / unavailable type.
	TypeUnknown FileType = 0
	// TypeFile is a regular file ('F').
	TypeFile FileType = 'F'
	// TypeDir is a directory ('/').
	TypeDir FileType = '/'
	// TypeSymlink is a symbolic link ('@').
	TypeSymlink FileType = '@'
	// TypeFIFO is a named pipe ('|').
	TypeFIFO FileType = '|'
	// TypeSocket is a socket ('=').
	TypeSocket FileType = '='
	// TypeBlockDev is a block device ('B').
	TypeBlockDev FileType = 'B'
	// TypeCharDev is a character device ('>').
	TypeCharDev FileType = '>'
)

// POSIX S_IF* mode bits (octal), as encoded in zfs_stat_t.zs_mode. Defined here
// rather than pulling in syscall so the classification is identical on every
// build target.
const (
	sIFMT   = 0o170000
	sIFIFO  = 0o010000
	sIFCHR  = 0o020000
	sIFDIR  = 0o040000
	sIFBLK  = 0o060000
	sIFREG  = 0o100000
	sIFLNK  = 0o120000
	sIFSOCK = 0o140000
)

// fileTypeFromMode maps a zs_mode value's S_IFMT bits onto a FileType.
func fileTypeFromMode(mode uint64) FileType {
	switch mode & sIFMT {
	case sIFREG:
		return TypeFile
	case sIFDIR:
		return TypeDir
	case sIFLNK:
		return TypeSymlink
	case sIFIFO:
		return TypeFIFO
	case sIFSOCK:
		return TypeSocket
	case sIFBLK:
		return TypeBlockDev
	case sIFCHR:
		return TypeCharDev
	default:
		return TypeUnknown
	}
}

// DiffEntry is one change between two snapshots, as produced by Diff. It mirrors
// a line of `zfs diff`: a change marker, the object's type, the path(s), and the
// underlying object number. For a rename, Path is the new name and OldPath the
// previous one; for every other change OldPath is empty.
type DiffEntry struct {
	Change  DiffChange // +, -, M or R
	Type    FileType   // F, /, @, etc. (TypeUnknown if the stat was unavailable)
	Path    string     // path within the dataset (the new path for a rename)
	OldPath string     // previous path (rename only; "" otherwise)
	Object  uint64     // the DMU object (inode) number the change concerns
}
