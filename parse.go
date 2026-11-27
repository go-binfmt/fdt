package fdt

import (
	"bytes"
	"encoding/binary"
	"slices"
	"sort"
	"strings"
	"unsafe"
)

func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
func be64(b []byte) uint64 { return binary.BigEndian.Uint64(b) }

// ParseHeader decodes and validates the header of b: the magic, the version,
// totalsize against len(b), the alignment of every block offset, every block
// inside totalsize, and the structure and strings blocks not overlapping each
// other or the header. It does not look at the blocks' contents.
func ParseHeader(b []byte) (Header, error) {
	h, _, err := parseHeader(b)
	return h, err
}

// blocks are the byte ranges of a validated header, as offsets into the blob.
type blocks struct {
	structOff, structEnd   int
	stringsOff, stringsEnd int
	rsvOff                 int
	total                  int
}

func parseHeader(b []byte) (Header, blocks, error) {
	var h Header
	var bl blocks
	if len(b) < 28 {
		return h, bl, formatErr("%d bytes is shorter than a header", len(b))
	}
	h.Magic = be32(b[0:])
	h.TotalSize = be32(b[4:])
	h.OffDTStruct = be32(b[8:])
	h.OffDTStrings = be32(b[12:])
	h.OffMemRsvmap = be32(b[16:])
	h.Version = be32(b[20:])
	h.LastCompVersion = be32(b[24:])
	if h.Magic != Magic {
		return h, bl, formatErr("magic %#x, want %#x", h.Magic, uint32(Magic))
	}
	if h.LastCompVersion > Version {
		return h, bl, wrapVersion("last_comp_version %d is newer than %d", h.LastCompVersion, Version)
	}
	if h.Version < MinVersion {
		return h, bl, wrapVersion("version %d is older than %d", h.Version, MinVersion)
	}
	if h.Version < h.LastCompVersion {
		return h, bl, formatErr("version %d below last_comp_version %d", h.Version, h.LastCompVersion)
	}
	hdrSize := uint64(headerSize16)
	if h.Version >= 17 {
		hdrSize = HeaderSize
	}
	if uint64(len(b)) < hdrSize {
		return h, bl, formatErr("%d bytes is shorter than a version %d header", len(b), h.Version)
	}
	h.BootCPUIDPhys = be32(b[28:])
	h.SizeDTStrings = be32(b[32:])
	if h.Version >= 17 {
		h.SizeDTStruct = be32(b[36:])
	}
	total := uint64(h.TotalSize)
	if total < hdrSize {
		return h, bl, formatErr("totalsize %d is smaller than the header", total)
	}
	if total > uint64(len(b)) {
		return h, bl, formatErr("totalsize %d exceeds the %d bytes given", total, len(b))
	}
	if h.OffMemRsvmap%8 != 0 {
		return h, bl, formatErr("off_mem_rsvmap %d is not 8-byte aligned", h.OffMemRsvmap)
	}
	if h.OffDTStruct%4 != 0 {
		return h, bl, formatErr("off_dt_struct %d is not 4-byte aligned", h.OffDTStruct)
	}
	if uint64(h.OffMemRsvmap) < hdrSize || uint64(h.OffMemRsvmap)+16 > total {
		return h, bl, formatErr("memory reservation block at %d does not fit in %d bytes", h.OffMemRsvmap, total)
	}
	structOff := uint64(h.OffDTStruct)
	stringsOff := uint64(h.OffDTStrings)
	if structOff < hdrSize || structOff+8 > total {
		return h, bl, formatErr("structure block at %d does not fit in %d bytes", structOff, total)
	}
	structEnd := total
	if h.Version >= 17 {
		if h.SizeDTStruct%4 != 0 {
			return h, bl, formatErr("size_dt_struct %d is not a multiple of 4", h.SizeDTStruct)
		}
		structEnd = structOff + uint64(h.SizeDTStruct)
	} else if stringsOff > structOff {
		// A version 16 header does not say how long the structure block is:
		// it ends where the strings begin, or at the end of the blob.
		structEnd = stringsOff
	}
	if structEnd > total || structEnd < structOff+8 {
		return h, bl, formatErr("structure block [%d, %d) does not fit in %d bytes", structOff, structEnd, total)
	}
	stringsEnd := stringsOff + uint64(h.SizeDTStrings)
	if (h.SizeDTStrings > 0 && stringsOff < hdrSize) || stringsEnd > total {
		return h, bl, formatErr("strings block [%d, %d) does not fit in %d bytes", stringsOff, stringsEnd, total)
	}
	if h.SizeDTStrings > 0 && structOff < stringsEnd && stringsOff < structEnd {
		return h, bl, formatErr("structure block [%d, %d) overlaps strings block [%d, %d)", structOff, structEnd, stringsOff, stringsEnd)
	}
	bl = blocks{
		structOff: int(structOff), structEnd: int(structEnd),
		stringsOff: int(stringsOff), stringsEnd: int(stringsEnd),
		rsvOff: int(h.OffMemRsvmap), total: int(total),
	}
	return h, bl, nil
}

func wrapVersion(format string, a ...any) error {
	return versionError{formatErr(format, a...)}
}

// versionError is both an ErrFormat and an ErrVersion.
type versionError struct{ error }

func (e versionError) Is(target error) bool { return target == ErrVersion }
func (e versionError) Unwrap() error        { return e.error }

// Parse decodes a flattened devicetree blob. b may be longer than the
// blob's totalsize (the tail is ignored) but not shorter. The returned Tree
// shares nothing with b.
//
// Everything the format lets a hostile blob lie about is checked: offsets and
// sizes against totalsize without integer overflow (also on 32-bit), block
// overlap, the (0, 0) terminator of the reservation block, NUL termination of
// node names and of property names in the strings block, property lengths
// against the structure block, nesting depth against MaxDepth, a single root
// whose name is empty, balanced BEGIN_NODE/END_NODE, properties before
// subnodes, unique property names within a node and unique node names within
// a parent, and an FDT_END token after the root. Bytes of the structure block
// after FDT_END are ignored, as libfdt ignores them.
func Parse(b []byte) (*Tree, error) { return parse(b, true) }

// ParseShared is Parse without the copy of the structure block: the property
// values of the returned Tree are subslices of b (capacity-limited, so an
// append to one cannot reach its neighbour). It is for large blobs, such as a
// FIT image with its kernel and ramdisk inside, where Parse would copy every
// embedded byte. The caller must not modify b while the Tree, or anything
// taken from it, is in use; node and property names are views into b as well.
// Everything else, including every check, is Parse.
func ParseShared(b []byte) (*Tree, error) { return parse(b, false) }

func parse(b []byte, copyBlob bool) (*Tree, error) {
	h, bl, err := parseHeader(b)
	if err != nil {
		return nil, err
	}
	t := &Tree{BootCPUID: h.BootCPUIDPhys}

	// Memory reservation block: pairs up to the (0, 0) terminator, which must
	// be inside totalsize and must not run into the structure block.
	pos := bl.rsvOff
	for {
		if pos+16 > bl.total {
			return nil, formatErr("memory reservation block has no (0, 0) terminator")
		}
		addr, size := be64(b[pos:]), be64(b[pos+8:])
		pos += 16
		if addr == 0 && size == 0 {
			break
		}
		t.Reservations = append(t.Reservations, Reservation{addr, size})
	}
	if bl.rsvOff < bl.structEnd && bl.structOff < pos {
		return nil, formatErr("memory reservation block [%d, %d) overlaps the structure block", bl.rsvOff, pos)
	}
	if bl.rsvOff < bl.stringsEnd && bl.stringsOff < pos && bl.stringsEnd > bl.stringsOff {
		return nil, formatErr("memory reservation block [%d, %d) overlaps the strings block", bl.rsvOff, pos)
	}

	p := &parser{
		sb:   b[bl.structOff:bl.structEnd],
		strs: string(b[bl.stringsOff:bl.stringsEnd]),
	}
	p.arena = p.sb
	if copyBlob {
		p.arena = slices.Clone(p.sb)
	}
	// Node names are substrings of the structure block. They view the same
	// bytes as the property values, which are the only ones a caller can write
	// to: the bytes of a name are never reachable as a []byte.
	p.names = unsafe.String(unsafe.SliceData(p.arena), len(p.arena))
	root, err := p.walk()
	if err != nil {
		return nil, err
	}
	t.Root = root
	return t, nil
}

type frame struct {
	n *Node
	// propSet and childSet are built once a node has many names, so duplicate
	// detection stays linear in the blob size.
	propSet, childSet map[string]struct{}
}

type parser struct {
	sb    []byte // structure block, as in the input
	arena []byte // private copy of sb that property values slice
	names string // sb as a string: node names are substrings of it
	strs  string // strings block

	nul []int32 // positions of NUL bytes in strs, built when a name is long
}

const linearNames = 8

// propName returns the NUL-terminated string at off in the strings block.
func (p *parser) propName(off uint32) (string, error) {
	if uint64(off) >= uint64(len(p.strs)) {
		return "", formatErr("property name offset %d outside the %d-byte strings block", off, len(p.strs))
	}
	s := p.strs[off:]
	// Names are short: look at a small window first.
	w := s
	if len(w) > 64 {
		w = w[:64]
	}
	if i := strings.IndexByte(w, 0); i >= 0 {
		if i == 0 {
			return "", formatErr("empty property name at strings offset %d", off)
		}
		return s[:i], nil
	}
	// A long name, or an unterminated one: binary search the NUL positions so
	// a hostile block cannot make every property rescan a long run.
	if p.nul == nil {
		p.nul = make([]int32, 0, 16)
		for i := 0; i < len(p.strs); i++ {
			if p.strs[i] == 0 {
				p.nul = append(p.nul, int32(i))
			}
		}
	}
	k := sort.Search(len(p.nul), func(i int) bool { return int(p.nul[i]) >= int(off) })
	if k == len(p.nul) {
		return "", formatErr("property name at strings offset %d is not NUL-terminated", off)
	}
	return s[:int(p.nul[k])-int(off)], nil
}

func (p *parser) walk() (*Node, error) {
	var root *Node
	stack := make([]frame, 0, 16)
	sb := p.sb
	pos := 0
	for {
		if pos+4 > len(sb) {
			return nil, formatErr("structure block ends without FDT_END")
		}
		tok := be32(sb[pos:])
		pos += 4
		switch tok {
		case tokNop:
		case tokBeginNode:
			rest := sb[pos:]
			i := bytes.IndexByte(rest, 0)
			if i < 0 {
				return nil, formatErr("node name at structure offset %d is not NUL-terminated", pos)
			}
			name := p.names[pos : pos+i]
			next := pos + align4(i+1)
			if next > len(sb) {
				return nil, formatErr("node name at structure offset %d runs past the block", pos)
			}
			pos = next
			if len(stack) >= MaxDepth {
				return nil, formatErr("nesting deeper than %d", MaxDepth)
			}
			n := &Node{Name: name}
			if len(stack) == 0 {
				if root != nil {
					return nil, formatErr("a second root node follows the first")
				}
				if name != "" {
					return nil, formatErr("root node named %q, want \"\"", name)
				}
				root = n
			} else {
				if name == "" || strings.IndexByte(name, '/') >= 0 {
					return nil, formatErr("invalid node name %q", name)
				}
				f := &stack[len(stack)-1]
				if err := f.addChild(n); err != nil {
					return nil, err
				}
			}
			stack = append(stack, frame{n: n})
		case tokEndNode:
			if len(stack) == 0 {
				return nil, formatErr("FDT_END_NODE without a node at structure offset %d", pos-4)
			}
			stack = stack[:len(stack)-1]
		case tokProp:
			if len(stack) == 0 {
				return nil, formatErr("property outside any node at structure offset %d", pos-4)
			}
			if pos+8 > len(sb) {
				return nil, formatErr("truncated property at structure offset %d", pos-4)
			}
			ln := int(be32(sb[pos:]))
			nameoff := be32(sb[pos+4:])
			pos += 8
			if ln < 0 || ln > len(sb)-pos || align4(ln) > len(sb)-pos {
				return nil, formatErr("property value of %d bytes runs past the structure block", uint32(ln))
			}
			name, err := p.propName(nameoff)
			if err != nil {
				return nil, err
			}
			f := &stack[len(stack)-1]
			if len(f.n.Children) > 0 {
				return nil, formatErr("property %q follows a subnode", name)
			}
			if err := f.addProp(name); err != nil {
				return nil, err
			}
			f.n.Props = append(f.n.Props, Prop{Name: name, Value: p.arena[pos : pos+ln : pos+ln]})
			pos += align4(ln)
		case tokEnd:
			if len(stack) != 0 {
				return nil, formatErr("FDT_END inside an open node")
			}
			if root == nil {
				return nil, formatErr("no root node")
			}
			return root, nil
		default:
			return nil, formatErr("unknown token %#x at structure offset %d", tok, pos-4)
		}
	}
}

func (f *frame) addChild(c *Node) error {
	n := f.n
	if f.childSet != nil {
		if _, dup := f.childSet[c.Name]; dup {
			return formatErr("duplicate node %q in %q", c.Name, n.Name)
		}
		f.childSet[c.Name] = struct{}{}
	} else {
		for _, o := range n.Children {
			if o.Name == c.Name {
				return formatErr("duplicate node %q in %q", c.Name, n.Name)
			}
		}
		if len(n.Children) >= linearNames {
			f.childSet = make(map[string]struct{}, 2*linearNames)
			for _, o := range n.Children {
				f.childSet[o.Name] = struct{}{}
			}
			f.childSet[c.Name] = struct{}{}
		}
	}
	n.Children = append(n.Children, c)
	return nil
}

// addProp only checks the name; the caller appends the property.
func (f *frame) addProp(name string) error {
	n := f.n
	if f.propSet == nil {
		// Small nodes: a linear scan; past linearNames, switch to a set.
		for i := range n.Props {
			if n.Props[i].Name == name {
				return formatErr("duplicate property %q in %q", name, n.Name)
			}
		}
		if len(n.Props) >= linearNames {
			f.propSet = make(map[string]struct{}, 2*linearNames)
			for i := range n.Props {
				f.propSet[n.Props[i].Name] = struct{}{}
			}
			f.propSet[name] = struct{}{}
		}
		return nil
	}
	if _, dup := f.propSet[name]; dup {
		return formatErr("duplicate property %q in %q", name, n.Name)
	}
	f.propSet[name] = struct{}{}
	return nil
}
