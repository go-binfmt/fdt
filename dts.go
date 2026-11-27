package fdt

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// WriteDTS writes the tree as devicetree source text, the input syntax of
// `dtc -I dts`. Values are rendered by shape: empty as a flag, a list of
// printable NUL-terminated strings as strings, a multiple of 4 bytes as cells,
// anything else as a byte string. Compiling the output with dtc (with
// -b BootCPUID) gives back the same blob, which is how the tests use it.
//
// A name outside the characters dts allows is reported as ErrFormat.
func (t *Tree) WriteDTS(dst io.Writer) error {
	if t == nil || t.Root == nil {
		return formatErr("tree has no root")
	}
	w := bufio.NewWriter(dst)
	w.WriteString("/dts-v1/;\n")
	for _, r := range t.Reservations {
		fmt.Fprintf(w, "\n/memreserve/ %#x %#x;\n", r.Address, r.Size)
	}
	w.WriteString("\n")
	if err := writeDTSNode(w, t.Root, 0); err != nil {
		return err
	}
	return w.Flush()
}

func dtsName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte(",._+*#?@-", c) >= 0:
		default:
			return false
		}
	}
	return true
}

func writeDTSNode(w *bufio.Writer, n *Node, depth int) error {
	ind := strings.Repeat("\t", depth)
	name := n.Name
	if depth == 0 {
		name = "/"
	} else if !dtsName(name) {
		return formatErr("node name %q cannot be written as dts", name)
	}
	fmt.Fprintf(w, "%s%s {\n", ind, name)
	for i := range n.Props {
		p := &n.Props[i]
		if !dtsName(p.Name) {
			return formatErr("property name %q cannot be written as dts", p.Name)
		}
		fmt.Fprintf(w, "%s\t%s", ind, p.Name)
		if len(p.Value) > 0 {
			w.WriteString(" = ")
			w.WriteString(dtsValue(p.Value))
		}
		w.WriteString(";\n")
	}
	for _, c := range n.Children {
		w.WriteString("\n")
		if err := writeDTSNode(w, c, depth+1); err != nil {
			return err
		}
	}
	fmt.Fprintf(w, "%s};\n", ind)
	return nil
}

func dtsValue(v []byte) string {
	if list, ok := printableList(v); ok {
		var sb strings.Builder
		for i, s := range list {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteByte('"')
			for j := 0; j < len(s); j++ {
				if s[j] == '"' || s[j] == '\\' {
					sb.WriteByte('\\')
				}
				sb.WriteByte(s[j])
			}
			sb.WriteByte('"')
		}
		return sb.String()
	}
	var sb strings.Builder
	if len(v)%4 == 0 {
		sb.WriteByte('<')
		for i := 0; i < len(v); i += 4 {
			if i > 0 {
				sb.WriteByte(' ')
			}
			fmt.Fprintf(&sb, "0x%x", be32(v[i:]))
		}
		sb.WriteByte('>')
		return sb.String()
	}
	sb.WriteByte('[')
	for i, c := range v {
		if i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%02x", c)
	}
	sb.WriteByte(']')
	return sb.String()
}

// printableList reports whether v is one or more non-empty NUL-terminated
// printable ASCII strings.
func printableList(v []byte) ([]string, bool) {
	if len(v) < 2 || v[len(v)-1] != 0 {
		return nil, false
	}
	var out []string
	start := 0
	for i, c := range v {
		if c == 0 {
			if i == start {
				return nil, false
			}
			out = append(out, string(v[start:i]))
			start = i + 1
		} else if c < 0x20 || c > 0x7e {
			return nil, false
		}
	}
	return out, true
}
