package fdt

import (
	"encoding/binary"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func u32(v ...uint32) []byte {
	var b []byte
	for _, x := range v {
		b = binary.BigEndian.AppendUint32(b, x)
	}
	return b
}

func cat(parts ...[]byte) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}

// pad4 zero-pads b to a multiple of 4.
func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func tBegin(name string) []byte { return cat(u32(tokBeginNode), pad4(append([]byte(name), 0))) }
func tEnd() []byte              { return u32(tokEndNode) }
func tProp(nameoff uint32, val []byte) []byte {
	return cat(u32(tokProp, uint32(len(val)), nameoff), pad4(append([]byte(nil), val...)))
}
func tFinish() []byte { return u32(tokEnd) }

// rawBlob lays out a version 17 blob from raw blocks: header, reservation
// block (with terminator), structure block, strings block. The caller may
// then poke header words.
func rawBlob(rsv []Reservation, st, strs []byte) []byte {
	var rb []byte
	for _, r := range rsv {
		rb = binary.BigEndian.AppendUint64(rb, r.Address)
		rb = binary.BigEndian.AppendUint64(rb, r.Size)
	}
	rb = append(rb, make([]byte, 16)...)
	offStruct := HeaderSize + len(rb)
	offStr := offStruct + len(st)
	total := offStr + len(strs)
	h := u32(Magic, uint32(total), uint32(offStruct), uint32(offStr), HeaderSize, 17, 16, 0, uint32(len(strs)), uint32(len(st)))
	return cat(h, rb, st, strs)
}

func setWord(b []byte, idx int, v uint32) []byte {
	c := append([]byte(nil), b...)
	binary.BigEndian.PutUint32(c[4*idx:], v)
	return c
}

// minimal is the smallest valid structure block: an empty root.
func minimalStruct() []byte { return cat(tBegin(""), tEnd(), tFinish()) }

// sampleTree has every kind of value and a reservation.
func sampleTree() *Tree {
	t := New()
	t.BootCPUID = 3
	t.Reservations = []Reservation{{0x1000, 0x2000}, {0xdead0000beef, 1}}
	r := t.Root
	r.SetUint32("#address-cells", 1)
	r.SetUint32("#size-cells", 1)
	r.SetString("model", "demo,board")
	r.SetStringList("compatible", "demo,board", "demo,soc")
	r.SetEmpty("dma-coherent")
	r.SetUint64("big", 0x0102030405060708)
	r.Set("blob", []byte{1, 2, 3, 4, 5})
	cpus, _ := r.AddChild("cpus")
	cpus.SetUint32("#size-cells", 0)
	c0, _ := cpus.AddChild("cpu@0")
	c0.SetString("device_type", "cpu")
	c0.SetUint32Cells("reg", 0)
	mem, _ := r.AddChild("memory@80000000")
	mem.SetUint32Cells("reg", 0x80000000, 0x10000000)
	mem.SetString("device_type", "memory")
	return t
}

// SetUint32Cells is a test convenience: several cells.
func (n *Node) SetUint32Cells(name string, cells ...uint32) {
	n.Set(name, u32(cells...))
}

// randomTree builds a tree whose names dtc accepts.
func randomTree(rng *rand.Rand) *Tree {
	names := []string{"compatible", "reg", "status", "#address-cells", "#size-cells", "interrupts", "clock-names", "names", "cells", "x", "phandle-x", "model"}
	t := New()
	t.BootCPUID = uint32(rng.Intn(4))
	for i := rng.Intn(3); i > 0; i-- {
		t.Reservations = append(t.Reservations, Reservation{uint64(rng.Int63()), uint64(rng.Intn(1<<20) + 1)})
	}
	var fill func(n *Node, depth int)
	fill = func(n *Node, depth int) {
		used := map[string]bool{}
		for i := rng.Intn(6); i > 0; i-- {
			name := names[rng.Intn(len(names))]
			if used[name] {
				continue
			}
			used[name] = true
			var v []byte
			switch rng.Intn(5) {
			case 0:
			case 1:
				v = append([]byte(strings.Repeat("ab", rng.Intn(5)+1)), 0)
			case 2:
				v = u32(uint32(rng.Int63()), uint32(rng.Int63()))
			case 3:
				v = make([]byte, rng.Intn(11))
				rng.Read(v)
			case 4:
				v = []byte("one\x00two\x00")
			}
			n.Set(name, v)
		}
		if depth > 3 {
			return
		}
		kids := map[string]bool{}
		for i := rng.Intn(4); i > 0; i-- {
			name := []string{"a", "b@1", "b@2", "soc", "bus@0", "node"}[rng.Intn(6)]
			if kids[name] {
				continue
			}
			kids[name] = true
			c, _ := n.AddChild(name)
			fill(c, depth+1)
		}
	}
	fill(t.Root, 0)
	return t
}

func corpus(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("testdata/dtc/*.dtb")
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus in testdata/dtc: %v", err)
	}
	return files
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// treesEqual compares two trees structurally.
func treesEqual(a, b *Tree) bool {
	if a.BootCPUID != b.BootCPUID || len(a.Reservations) != len(b.Reservations) {
		return false
	}
	for i := range a.Reservations {
		if a.Reservations[i] != b.Reservations[i] {
			return false
		}
	}
	return nodesEqual(a.Root, b.Root)
}

func nodesEqual(a, b *Node) bool {
	if a.Name != b.Name || len(a.Props) != len(b.Props) || len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Props {
		if a.Props[i].Name != b.Props[i].Name || string(a.Props[i].Value) != string(b.Props[i].Value) {
			return false
		}
	}
	for i := range a.Children {
		if !nodesEqual(a.Children[i], b.Children[i]) {
			return false
		}
	}
	return true
}

// v16Blob lays out a version 16 blob whose structure block is st, whatever
// its length: the strings block follows it directly.
func v16Blob(st, strs []byte) []byte {
	const off = 40
	total := off + 16 + len(st) + len(strs)
	return cat(u32(Magic, uint32(total), uint32(off+16), uint32(off+16+len(st)), off, 16, 16, 0, uint32(len(strs))),
		make([]byte, 4), make([]byte, 16), st, strs)
}
