package fdt

import (
	"bytes"
	"errors"
	"math/rand"
	"strings"
	"testing"
)

func TestMarshalLayout(t *testing.T) {
	tr := New()
	tr.BootCPUID = 7
	tr.Reservations = []Reservation{{0x10, 0x20}}
	tr.Root.SetUint32("a", 1)
	b, err := tr.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	h, err := ParseHeader(b)
	if err != nil {
		t.Fatal(err)
	}
	// header 40, one reservation + terminator 32, struct: BEGIN+pad(4) 8,
	// PROP 12+4, END_NODE 4, END 4 = 32, strings "a\0" = 2.
	want := Header{Magic: Magic, TotalSize: 40 + 32 + 32 + 2, OffDTStruct: 72, OffDTStrings: 104, OffMemRsvmap: 40,
		Version: 17, LastCompVersion: 16, BootCPUIDPhys: 7, SizeDTStrings: 2, SizeDTStruct: 32}
	if h != want {
		t.Fatalf("header %+v, want %+v", h, want)
	}
	if len(b) != int(want.TotalSize) {
		t.Fatalf("len %d, want %d", len(b), want.TotalSize)
	}
}

func TestMarshalSharesStringTails(t *testing.T) {
	// dtc's stringtable_insert reuses the tail of an earlier string: "cells"
	// is found inside "#size-cells", and "x" inside "#x"... in the order the
	// properties are met.
	tr := New()
	tr.Root.SetEmpty("#size-cells")
	tr.Root.SetEmpty("cells")
	tr.Root.SetEmpty("size-cells")
	tr.Root.SetEmpty("#size-cells2")
	b, err := tr.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	h, _ := ParseHeader(b)
	got := string(b[h.OffDTStrings : h.OffDTStrings+h.SizeDTStrings])
	if want := "#size-cells\x00#size-cells2\x00"; got != want {
		t.Fatalf("strings %q, want %q", got, want)
	}
	back, err := Parse(b)
	if err != nil || !treesEqual(tr, back) {
		t.Fatalf("round trip: %v", err)
	}
}

func TestMarshalErrors(t *testing.T) {
	deep := New()
	cur := deep.Root
	for i := 0; i < MaxDepth; i++ {
		cur, _ = cur.AddChild("n")
	}
	many := New()
	for i := 0; i < 12; i++ {
		many.Root.AddChild(string(rune('a' + i)))
	}
	manyDup := New()
	manyDup.Root.Children = many.Root.Children[:]
	manyDup.Root.Children = append(manyDup.Root.Children[:12:12], &Node{Name: "a"})
	manyP := New()
	for i := 0; i < 12; i++ {
		manyP.Root.SetEmpty(string(rune('a' + i)))
	}
	manyP.Root.Props = append(manyP.Root.Props, Prop{Name: "a"})

	cases := []struct {
		name string
		tree *Tree
		want string
	}{
		{"nil tree", nil, "no root"},
		{"nil root", &Tree{}, "no root"},
		{"named root", &Tree{Root: &Node{Name: "x"}}, "root node named"},
		{"empty child name", &Tree{Root: &Node{Children: []*Node{{}}}}, "invalid node name"},
		{"slash in name", &Tree{Root: &Node{Children: []*Node{{Name: "a/b"}}}}, "invalid node name"},
		{"nil child", &Tree{Root: &Node{Children: []*Node{nil}}}, "nil child"},
		{"empty property name", &Tree{Root: &Node{Props: []Prop{{Name: ""}}}}, "invalid property name"},
		{"NUL in property name", &Tree{Root: &Node{Props: []Prop{{Name: "a\x00b"}}}}, "invalid property name"},
		{"duplicate property", &Tree{Root: &Node{Props: []Prop{{Name: "a"}, {Name: "a"}}}}, "duplicate property"},
		{"duplicate property, many", manyP, "duplicate property"},
		{"duplicate child", &Tree{Root: &Node{Children: []*Node{{Name: "a"}, {Name: "a"}}}}, "duplicate node"},
		{"duplicate child, many", manyDup, "duplicate node"},
		{"too deep", deep, "nesting deeper"},
	}
	for _, c := range cases {
		_, err := c.tree.Marshal()
		if err == nil || !errors.Is(err, ErrFormat) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
	// Exactly MaxDepth levels is fine.
	ok := New()
	cur = ok.Root
	for i := 0; i < MaxDepth-1; i++ {
		cur, _ = cur.AddChild("n")
	}
	if _, err := ok.Marshal(); err != nil {
		t.Errorf("depth %d: %v", MaxDepth, err)
	}
}

func TestMarshalSizeLimits(t *testing.T) {
	defer func(old uint64) { maxSize = old }(maxSize)
	tr := sampleTree()
	full, err := tr.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		limit uint64
		want  string
	}{
		{uint64(len(full)) - 1, "exceeds the 32-bit size limit"},
		{20, "structure block exceeds"},
	} {
		maxSize = c.limit
		if _, err := tr.Marshal(); !errors.Is(err, ErrFormat) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("limit %d: got %v, want %q", c.limit, err, c.want)
		}
	}
	one := New()
	one.Root.Set("v", make([]byte, 10))
	maxSize = 9
	if _, err := one.Marshal(); !errors.Is(err, ErrFormat) || !strings.Contains(err.Error(), "too long") {
		t.Errorf("long value: got %v", err)
	}
}

func TestMarshalWriteTo(t *testing.T) {
	tr := sampleTree()
	var buf bytes.Buffer
	n, err := tr.WriteTo(&buf)
	want, _ := tr.Marshal()
	if err != nil || n != int64(len(want)) || !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("WriteTo: n=%d err=%v", n, err)
	}
	if _, err := (&Tree{}).WriteTo(&buf); err == nil {
		t.Fatal("WriteTo accepted a rootless tree")
	}
	if _, err := tr.WriteTo(failWriter{}); err == nil {
		t.Fatal("WriteTo hid a write error")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// Marshal's output must always parse back to the same tree, and be a fixed
// point of Parse then Marshal.
func TestMarshalParseRandomTrees(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		tr := randomTree(rng)
		b, err := tr.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		back, err := Parse(b)
		if err != nil {
			t.Fatalf("tree %d: %v", i, err)
		}
		if !treesEqual(tr, back) {
			t.Fatalf("tree %d changed", i)
		}
		b2, _ := back.Marshal()
		if !bytes.Equal(b, b2) {
			t.Fatalf("tree %d: Marshal is not a fixed point", i)
		}
	}
}
