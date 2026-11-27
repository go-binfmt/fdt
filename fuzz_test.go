package fdt

import (
	"bytes"
	"testing"
)

// FuzzParse feeds arbitrary bytes to Parse. Anything it accepts must marshal,
// parse back to the same tree, and marshal to the same bytes again; nothing
// may panic. The corpus under testdata/fuzz/FuzzParse (dtc's test-suite blobs,
// hand-made hostile ones, past findings) replays as ordinary tests.
func FuzzParse(f *testing.F) {
	for _, p := range []*Tree{New(), sampleTree()} {
		b, _ := p.Marshal()
		f.Add(b)
	}
	f.Add(rawBlob(nil, minimalStruct(), nil))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseHeader(data)
		tr, err := Parse(data)
		if err != nil {
			return
		}
		b, err := tr.Marshal()
		if err != nil {
			t.Fatalf("Parse accepted a tree Marshal refuses: %v", err)
		}
		back, err := Parse(b)
		if err != nil {
			t.Fatalf("Marshal wrote a blob Parse refuses: %v", err)
		}
		if !treesEqual(tr, back) {
			t.Fatal("tree changed through Marshal and Parse")
		}
		b2, err := back.Marshal()
		if err != nil || !bytes.Equal(b, b2) {
			t.Fatalf("Marshal is not a fixed point: %v", err)
		}
		// The typed API must cope with whatever the blob held.
		tr.Root.Walk(func(path string, n *Node) bool {
			for _, p := range n.Props {
				_, _ = n.Uint32(p.Name)
				_, _ = n.Uint64(p.Name)
				_, _ = n.String(p.Name)
				_, _ = n.StringList(p.Name)
				_, _ = n.Uint32s(p.Name)
			}
			if got, err := tr.Find(path); err != nil || got == nil {
				// Names containing '@'-less prefixes may resolve to an
				// earlier sibling, but a path of the walk always resolves.
				t.Fatalf("Find(%q): %v", path, err)
			}
			return true
		})
	})
}
