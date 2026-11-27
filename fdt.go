// Package fdt reads and writes flattened devicetree blobs (DTB, version 17),
// the format of Devicetree Specification chapter 5, which is also the container
// of U-Boot FIT images.
//
// Parse turns untrusted bytes into a Tree and refuses, never panics, on
// anything malformed. Tree.Marshal writes a canonical blob, byte for byte what
// `dtc -I dts -O dtb` writes for the same tree (same block order, same
// alignment, same string-table sharing), so Marshal(Parse(dtc output)) is the
// identity.
package fdt

import (
	"errors"
	"fmt"
)

// Constants of the format (Devicetree Specification v0.4, section 5).
const (
	// Magic is the first word of every blob.
	Magic = 0xd00dfeed
	// Version is the version Marshal writes and the newest this package knows.
	Version = 17
	// LastCompVersion is the last_comp_version Marshal writes.
	LastCompVersion = 16
	// MinVersion is the oldest version Parse accepts. Versions 16 and 17 differ
	// only by the header's size_dt_struct word.
	MinVersion = 16

	// HeaderSize is the size of a version 17 header in bytes.
	HeaderSize = 40
	// headerSize16 is the size of a version 16 header.
	headerSize16 = 36

	// MaxDepth bounds node nesting, in Parse and in Marshal. The root is
	// depth 1. Real trees are a few levels deep; a bound keeps the work and
	// the stack of every consumer finite.
	MaxDepth = 128
)

// Structure block tokens.
const (
	tokBeginNode = 1
	tokEndNode   = 2
	tokProp      = 3
	tokNop       = 4
	tokEnd       = 9
)

var (
	// ErrFormat is wrapped by every error Parse returns about the bytes it was
	// given, and by Marshal errors about a Tree that cannot be written.
	ErrFormat = errors.New("fdt: malformed blob")
	// ErrVersion is returned for a version this package does not read.
	ErrVersion = errors.New("fdt: unsupported version")
	// ErrNotFound is returned by lookups that match nothing.
	ErrNotFound = errors.New("fdt: not found")
	// ErrValue is returned by a typed getter whose property has the wrong
	// length or shape.
	ErrValue = errors.New("fdt: property value has the wrong shape")
	// ErrExists is returned when adding a node or property whose name is taken.
	ErrExists = errors.New("fdt: name already exists")
)

func formatErr(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrFormat}, a...)...)
}

// Header is the decoded blob header. Parse and ParseHeader fill it from the
// bytes; Marshal computes its own.
type Header struct {
	Magic           uint32
	TotalSize       uint32
	OffDTStruct     uint32
	OffDTStrings    uint32
	OffMemRsvmap    uint32
	Version         uint32
	LastCompVersion uint32
	BootCPUIDPhys   uint32
	SizeDTStrings   uint32
	// SizeDTStruct is zero in a version 16 blob, whose header has no such word.
	SizeDTStruct uint32
}

// Reservation is one entry of the memory reservation block.
type Reservation struct {
	Address uint64
	Size    uint64
}

// Tree is a parsed devicetree blob: the boot CPU id, the memory reservations
// and the node tree. Its fields are plain data; edit them directly or through
// the Node methods.
type Tree struct {
	// BootCPUID is boot_cpuid_phys of the header.
	BootCPUID uint32
	// Reservations is the memory reservation block without its (0, 0)
	// terminator.
	Reservations []Reservation
	// Root is the root node, whose Name is empty.
	Root *Node
}

// Node is a devicetree node. Props keep their blob order, and so do Children;
// the writer emits them in that order.
type Node struct {
	Name     string
	Props    []Prop
	Children []*Node
}

// Prop is a property: a name and an uninterpreted value, possibly empty.
type Prop struct {
	Name  string
	Value []byte
}

// New returns a Tree holding only an empty root node.
func New() *Tree { return &Tree{Root: &Node{}} }

func align4(n int) int { return (n + 3) &^ 3 }
