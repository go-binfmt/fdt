package fdt

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// Find returns the node at an absolute path such as "/images/kernel-1".
// "/" is the root. See Node.Lookup for the matching rules.
func (t *Tree) Find(path string) (*Node, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("%w: path %q is not absolute", ErrNotFound, path)
	}
	return t.Root.Lookup(path)
}

// Lookup resolves a slash-separated path relative to n (a leading slash is
// ignored, repeated slashes count once). A component matches a child by its
// full name, or, like libfdt's fdt_subnode_offset, a component without '@'
// matches the first child named "component@unit-address". An exact match
// anywhere among the children wins over a unit-address match.
func (n *Node) Lookup(path string) (*Node, error) {
	cur := n
	rest := path
	for {
		rest = strings.TrimLeft(rest, "/")
		if rest == "" {
			return cur, nil
		}
		comp := rest
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			comp, rest = rest[:i], rest[i:]
		} else {
			rest = ""
		}
		next := cur.Child(comp)
		if next == nil {
			return nil, fmt.Errorf("%w: node %q in path %q", ErrNotFound, comp, path)
		}
		cur = next
	}
}

// Child returns the child named name, or nil. A name without '@' also matches
// "name@unit-address" when no child is named exactly name.
func (n *Node) Child(name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	if strings.IndexByte(name, '@') < 0 {
		for _, c := range n.Children {
			if len(c.Name) > len(name) && c.Name[len(name)] == '@' && c.Name[:len(name)] == name {
				return c
			}
		}
	}
	return nil
}

// Walk calls fn for n and every descendant in blob order (a node before its
// children), with the node's absolute path ("/" for the root). Returning false
// stops the walk.
func (n *Node) Walk(fn func(path string, n *Node) bool) {
	n.walk("", fn)
}

func (n *Node) walk(parent string, fn func(string, *Node) bool) bool {
	path := parent + "/" + n.Name
	if parent == "" && n.Name == "" {
		path = "/"
	}
	if !fn(path, n) {
		return false
	}
	base := path
	if base == "/" {
		base = ""
	}
	for _, c := range n.Children {
		if !c.walk(base, fn) {
			return false
		}
	}
	return true
}

// Prop returns a pointer to the property called name, or nil. The pointer is
// invalidated by anything that adds or removes properties of n.
func (n *Node) Prop(name string) *Prop {
	for i := range n.Props {
		if n.Props[i].Name == name {
			return &n.Props[i]
		}
	}
	return nil
}

// Bytes returns the raw value of the property name (not a copy).
func (n *Node) Bytes(name string) ([]byte, error) {
	p := n.Prop(name)
	if p == nil {
		return nil, fmt.Errorf("%w: property %q of %q", ErrNotFound, name, n.Name)
	}
	return p.Value, nil
}

func (n *Node) shape(name string, format string, a ...any) error {
	return fmt.Errorf("%w: property %q of %q %s", ErrValue, name, n.Name, fmt.Sprintf(format, a...))
}

// Uint32 returns the property as one big-endian 32-bit cell.
func (n *Node) Uint32(name string) (uint32, error) {
	v, err := n.Bytes(name)
	if err != nil {
		return 0, err
	}
	if len(v) != 4 {
		return 0, n.shape(name, "is %d bytes, want 4", len(v))
	}
	return binary.BigEndian.Uint32(v), nil
}

// Uint64 returns the property as one big-endian 64-bit value (two cells).
func (n *Node) Uint64(name string) (uint64, error) {
	v, err := n.Bytes(name)
	if err != nil {
		return 0, err
	}
	if len(v) != 8 {
		return 0, n.shape(name, "is %d bytes, want 8", len(v))
	}
	return binary.BigEndian.Uint64(v), nil
}

// Cell returns an address-like property of 1 or 2 cells, as the
// #address-cells of its parent says (FIT's load and entry).
func (n *Node) Cell(name string, cells int) (uint64, error) {
	switch cells {
	case 1:
		v, err := n.Uint32(name)
		return uint64(v), err
	case 2:
		return n.Uint64(name)
	}
	return 0, fmt.Errorf("%w: %d cells, want 1 or 2", ErrValue, cells)
}

// Uint32s returns the property as a list of 32-bit cells.
func (n *Node) Uint32s(name string) ([]uint32, error) {
	v, err := n.Bytes(name)
	if err != nil {
		return nil, err
	}
	if len(v)%4 != 0 {
		return nil, n.shape(name, "is %d bytes, not a multiple of 4", len(v))
	}
	out := make([]uint32, len(v)/4)
	for i := range out {
		out[i] = binary.BigEndian.Uint32(v[4*i:])
	}
	return out, nil
}

// String returns the property as one NUL-terminated string.
func (n *Node) String(name string) (string, error) {
	v, err := n.Bytes(name)
	if err != nil {
		return "", err
	}
	if len(v) == 0 || v[len(v)-1] != 0 || strings.IndexByte(string(v[:len(v)-1]), 0) >= 0 {
		return "", n.shape(name, "is not a single NUL-terminated string")
	}
	return string(v[:len(v)-1]), nil
}

// StringList returns the property as a list of NUL-terminated strings, like
// "compatible". An empty property is an empty list.
func (n *Node) StringList(name string) ([]string, error) {
	v, err := n.Bytes(name)
	if err != nil {
		return nil, err
	}
	if len(v) == 0 {
		return nil, nil
	}
	if v[len(v)-1] != 0 {
		return nil, n.shape(name, "is not a NUL-terminated string list")
	}
	return strings.Split(string(v[:len(v)-1]), "\x00"), nil
}

// Set creates or replaces a property. The value is stored, not copied. It
// reports ErrValue for a name Marshal would refuse.
func (n *Node) Set(name string, value []byte) error {
	if err := checkPropName(name); err != nil {
		return err
	}
	if p := n.Prop(name); p != nil {
		p.Value = value
		return nil
	}
	n.Props = append(n.Props, Prop{Name: name, Value: value})
	return nil
}

// SetUint32 sets a one-cell property.
func (n *Node) SetUint32(name string, v uint32) error {
	return n.Set(name, binary.BigEndian.AppendUint32(nil, v))
}

// SetUint64 sets a two-cell property.
func (n *Node) SetUint64(name string, v uint64) error {
	return n.Set(name, binary.BigEndian.AppendUint64(nil, v))
}

// SetCell sets a property of 1 or 2 cells; a value that does not fit one cell
// is refused.
func (n *Node) SetCell(name string, cells int, v uint64) error {
	switch cells {
	case 1:
		if v > 0xffffffff {
			return fmt.Errorf("%w: %#x does not fit one cell", ErrValue, v)
		}
		return n.SetUint32(name, uint32(v))
	case 2:
		return n.SetUint64(name, v)
	}
	return fmt.Errorf("%w: %d cells, want 1 or 2", ErrValue, cells)
}

// SetString sets a property to one NUL-terminated string.
func (n *Node) SetString(name, s string) error {
	if strings.IndexByte(s, 0) >= 0 {
		return fmt.Errorf("%w: string contains NUL", ErrValue)
	}
	return n.Set(name, append([]byte(s), 0))
}

// SetStringList sets a property to a list of NUL-terminated strings.
func (n *Node) SetStringList(name string, list ...string) error {
	var v []byte
	for _, s := range list {
		if strings.IndexByte(s, 0) >= 0 {
			return fmt.Errorf("%w: string contains NUL", ErrValue)
		}
		v = append(append(v, s...), 0)
	}
	return n.Set(name, v)
}

// SetEmpty sets a property with no value, a boolean flag in devicetree usage.
func (n *Node) SetEmpty(name string) error { return n.Set(name, nil) }

// DeleteProp removes a property and reports whether it was there.
func (n *Node) DeleteProp(name string) bool {
	for i := range n.Props {
		if n.Props[i].Name == name {
			n.Props = append(n.Props[:i], n.Props[i+1:]...)
			return true
		}
	}
	return false
}

// AddChild appends a new empty child. It reports ErrExists if a child has that
// name and ErrFormat for a name Marshal would refuse. Children are written in
// the order they were added, after all properties.
func (n *Node) AddChild(name string) (*Node, error) {
	if err := checkNodeName(name); err != nil {
		return nil, err
	}
	for _, c := range n.Children {
		if c.Name == name {
			return nil, fmt.Errorf("%w: node %q in %q", ErrExists, name, n.Name)
		}
	}
	c := &Node{Name: name}
	n.Children = append(n.Children, c)
	return c, nil
}

// DeleteChild removes the child named exactly name and reports whether it was
// there.
func (n *Node) DeleteChild(name string) bool {
	for i, c := range n.Children {
		if c.Name == name {
			n.Children = append(n.Children[:i], n.Children[i+1:]...)
			return true
		}
	}
	return false
}

// Ensure returns the node at path below n, creating missing nodes (mkdir -p).
func (n *Node) Ensure(path string) (*Node, error) {
	cur := n
	for _, comp := range strings.Split(path, "/") {
		if comp == "" {
			continue
		}
		var next *Node
		for _, c := range cur.Children {
			if c.Name == comp {
				next = c
				break
			}
		}
		if next == nil {
			var err error
			if next, err = cur.AddChild(comp); err != nil {
				return nil, err
			}
		}
		cur = next
	}
	return cur, nil
}

func checkNodeName(name string) error {
	if name == "" || strings.ContainsAny(name, "/\x00") {
		return formatErr("invalid node name %q", name)
	}
	return nil
}

func checkPropName(name string) error {
	if name == "" || strings.IndexByte(name, 0) >= 0 {
		return fmt.Errorf("%w: invalid property name %q", ErrValue, name)
	}
	return nil
}
