package fdt

import (
	"bytes"
	"reflect"
	"testing"
)

func reuseTree() *Tree {
	t := New()
	t.BootCPUID = 3
	t.Reservations = []Reservation{{0x1000, 0x200}, {0x8000, 0x10}}
	t.Root.SetString("model", "odd") // 4 bytes: no value padding
	t.Root.Set("blob", []byte{1, 2, 3, 4, 5})
	c, _ := t.Root.AddChild("n") // name of 2 bytes: padded
	c.Set("data", []byte{9})
	c.SetEmpty("flag")
	return t
}

// A reused buffer, dirty with 0xff, must give the bytes Marshal gives: every
// padding byte and the reservation terminator are written, not assumed zero.
func TestAppendMarshalReusesADirtyBuffer(t *testing.T) {
	tr := reuseTree()
	want, err := tr.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	dirty := bytes.Repeat([]byte{0xff}, len(want)+64)
	got, err := tr.AppendMarshal(dirty[:0])
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("dirty buffer: err %v, equal %v", err, bytes.Equal(got, want))
	}
	if &got[0] != &dirty[0] {
		t.Error("the buffer was not reused")
	}
	// Appending after a prefix keeps the prefix.
	got, err = tr.AppendMarshal([]byte("prefix"))
	if err != nil || string(got[:6]) != "prefix" || !bytes.Equal(got[6:], want) {
		t.Errorf("append after a prefix: %v", err)
	}
	// An error returns nil.
	if b, err := (&Tree{}).AppendMarshal([]byte("x")); b != nil || err == nil {
		t.Errorf("error case: %v %v", b, err)
	}
}

func TestParseSharedMatchesParseAndAliases(t *testing.T) {
	blob, err := reuseTree().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	want, err := Parse(blob)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseShared(blob)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseShared differs from Parse: %v", err)
	}
	// Parse owns its values; ParseShared aliases the input.
	v, _ := got.Root.Bytes("blob")
	v[0] = 0xee
	if !bytes.Contains(blob, []byte{0xee, 2, 3, 4, 5}) {
		t.Error("ParseShared copied the value")
	}
	own, _ := want.Root.Bytes("blob")
	own[1] = 0xdd
	if bytes.Contains(blob, []byte{0xee, 0xdd}) {
		t.Error("Parse aliased the input")
	}
	// An append to a shared value must not write into the next property.
	before := bytes.Clone(blob)
	_ = append(v, 0xcc)
	if !bytes.Equal(blob, before) {
		t.Error("an append to a shared value wrote into the input")
	}
	if _, err := ParseShared(blob[:10]); err == nil {
		t.Error("a truncated blob was accepted")
	}
}
