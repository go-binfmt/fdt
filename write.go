package fdt

import (
	"encoding/binary"
	"io"
	"math"
)

// maxSize is the largest blob, structure block or property the format's
// 32-bit fields can describe. It is a variable only so the tests can reach
// the refusals without allocating 4 GiB.
var maxSize uint64 = math.MaxUint32

// Marshal writes the tree as a version 17 blob with the layout dtc uses:
// the 40-byte header, the memory reservation block at offset 40 with its
// (0, 0) terminator, the structure block, then the strings block, with no
// padding after it. Property names are shared in the strings block exactly as
// dtc shares them: a name that equals the tail of a string already present
// reuses it. The result is byte-identical to `dtc -I dts -O dtb` (with
// -b BootCPUID) for the same tree.
//
// Marshal refuses, with an error wrapping ErrFormat, a tree that would not
// parse back: no root, a root with a name or a child without one, a '/' or NUL
// in a node name, an empty property name, duplicate names, nesting deeper
// than MaxDepth, or a blob that would not fit the format's 32-bit offsets.
func (t *Tree) Marshal() ([]byte, error) {
	if t == nil || t.Root == nil {
		return nil, formatErr("tree has no root")
	}
	if t.Root.Name != "" {
		return nil, formatErr("root node named %q, want \"\"", t.Root.Name)
	}
	w := &writer{idx: make(map[string]uint32, 64)}
	if err := w.measure(t.Root, 1); err != nil {
		return nil, err
	}
	rsv := 16 * (uint64(len(t.Reservations)) + 1)
	structSize := w.structSize + 4 // FDT_END
	offStruct := uint64(HeaderSize) + rsv
	offStrings := offStruct + structSize
	total := offStrings + uint64(len(w.strtab))
	if total > maxSize {
		return nil, formatErr("blob of %d bytes exceeds the 32-bit size limit", total)
	}
	out := make([]byte, total)
	be := binary.BigEndian
	be.PutUint32(out[0:], Magic)
	be.PutUint32(out[4:], uint32(total))
	be.PutUint32(out[8:], uint32(offStruct))
	be.PutUint32(out[12:], uint32(offStrings))
	be.PutUint32(out[16:], HeaderSize)
	be.PutUint32(out[20:], Version)
	be.PutUint32(out[24:], LastCompVersion)
	be.PutUint32(out[28:], t.BootCPUID)
	be.PutUint32(out[32:], uint32(len(w.strtab)))
	be.PutUint32(out[36:], uint32(structSize))
	pos := HeaderSize
	for _, r := range t.Reservations {
		be.PutUint64(out[pos:], r.Address)
		be.PutUint64(out[pos+8:], r.Size)
		pos += 16
	}
	// The terminator and the padding of every block are already zero.
	w.out = out[:offStrings]
	w.pos = int(offStruct)
	w.emit(t.Root)
	be.PutUint32(w.out[w.pos:], tokEnd)
	copy(out[offStrings:], w.strtab)
	return out, nil
}

// WriteTo marshals the tree and writes it to dst.
func (t *Tree) WriteTo(dst io.Writer) (int64, error) {
	b, err := t.Marshal()
	if err != nil {
		return 0, err
	}
	n, err := dst.Write(b)
	return int64(n), err
}

type writer struct {
	idx        map[string]uint32 // every suffix of every string in strtab -> lowest offset
	strtab     []byte
	offs       []uint32 // nameoff per property, in blob order
	structSize uint64
	out        []byte
	pos        int
	next       int // index into offs
}

// intern is dtc's stringtable_insert: the lowest offset at which name is a
// whole NUL-terminated string, which includes the tail of a longer string.
func (w *writer) intern(name string) uint32 {
	if off, ok := w.idx[name]; ok {
		return off
	}
	off := uint32(len(w.strtab))
	w.strtab = append(w.strtab, name...)
	w.strtab = append(w.strtab, 0)
	for k := 0; k < len(name); k++ {
		if _, ok := w.idx[name[k:]]; !ok {
			w.idx[name[k:]] = off + uint32(k)
		}
	}
	return off
}

// measure validates the tree below n, sizes the structure block, and builds
// the strings block in the order dtc does (properties of a node, then its
// children, depth first).
func (w *writer) measure(n *Node, depth int) error {
	if depth > MaxDepth {
		return formatErr("nesting deeper than %d", MaxDepth)
	}
	w.structSize += 4 + uint64(align4(len(n.Name)+1)) // BEGIN_NODE
	var seen map[string]struct{}
	if len(n.Props) > linearNames {
		seen = make(map[string]struct{}, len(n.Props))
	}
	for i := range n.Props {
		p := &n.Props[i]
		if err := checkPropName(p.Name); err != nil {
			return formatErr("invalid property name %q in %q", p.Name, n.Name)
		}
		if seen != nil {
			if _, dup := seen[p.Name]; dup {
				return formatErr("duplicate property %q in %q", p.Name, n.Name)
			}
			seen[p.Name] = struct{}{}
		} else {
			for j := 0; j < i; j++ {
				if n.Props[j].Name == p.Name {
					return formatErr("duplicate property %q in %q", p.Name, n.Name)
				}
			}
		}
		if uint64(len(p.Value)) > maxSize {
			return formatErr("property %q of %q is too long", p.Name, n.Name)
		}
		w.offs = append(w.offs, w.intern(p.Name))
		w.structSize += 12 + uint64(align4(len(p.Value)))
		if w.structSize > maxSize {
			return formatErr("structure block exceeds the 32-bit size limit")
		}
	}
	if len(n.Children) > linearNames {
		seen = make(map[string]struct{}, len(n.Children))
	} else {
		seen = nil
	}
	for i, c := range n.Children {
		if c == nil {
			return formatErr("nil child in %q", n.Name)
		}
		if err := checkNodeName(c.Name); err != nil {
			return err
		}
		if seen != nil {
			if _, dup := seen[c.Name]; dup {
				return formatErr("duplicate node %q in %q", c.Name, n.Name)
			}
			seen[c.Name] = struct{}{}
		} else {
			for j := 0; j < i; j++ {
				if n.Children[j].Name == c.Name {
					return formatErr("duplicate node %q in %q", c.Name, n.Name)
				}
			}
		}
		if err := w.measure(c, depth+1); err != nil {
			return err
		}
	}
	w.structSize += 4 // END_NODE
	return nil
}

func (w *writer) emit(n *Node) {
	be := binary.BigEndian
	be.PutUint32(w.out[w.pos:], tokBeginNode)
	w.pos += 4
	w.pos += copy(w.out[w.pos:], n.Name) // the NUL and padding are zero already
	w.pos += 1 + (-(len(n.Name) + 1) & 3)
	for i := range n.Props {
		p := &n.Props[i]
		be.PutUint32(w.out[w.pos:], tokProp)
		be.PutUint32(w.out[w.pos+4:], uint32(len(p.Value)))
		be.PutUint32(w.out[w.pos+8:], w.offs[w.next])
		w.next++
		w.pos += 12
		w.pos += copy(w.out[w.pos:], p.Value)
		w.pos += -len(p.Value) & 3
	}
	for _, c := range n.Children {
		w.emit(c)
	}
	be.PutUint32(w.out[w.pos:], tokEndNode)
	w.pos += 4
}
