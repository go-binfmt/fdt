package fdt

import (
	"errors"
	"strings"
	"testing"
)

func TestParseMinimal(t *testing.T) {
	tr, err := Parse(rawBlob(nil, minimalStruct(), nil))
	if err != nil {
		t.Fatal(err)
	}
	if tr.Root == nil || tr.Root.Name != "" || len(tr.Root.Props) != 0 || len(tr.Reservations) != 0 {
		t.Fatalf("unexpected tree %+v", tr)
	}
}

func TestParseTrailingBytesAndNops(t *testing.T) {
	st := cat(u32(tokNop), tBegin(""), u32(tokNop), tEnd(), u32(tokNop), tFinish(), u32(0, 0))
	b := cat(rawBlob(nil, st, nil), []byte("tail beyond totalsize"))
	if _, err := Parse(b); err != nil {
		t.Fatal(err)
	}
}

func TestParseSampleRoundTrip(t *testing.T) {
	src := sampleTree()
	b, err := src.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if !treesEqual(src, got) {
		t.Fatal("tree changed through Marshal and Parse")
	}
	b2, err := got.Marshal()
	if err != nil || string(b2) != string(b) {
		t.Fatalf("second Marshal differs: %v", err)
	}
}

func TestParseOwnsItsMemory(t *testing.T) {
	b := rawBlob(nil, cat(tBegin(""), tProp(0, []byte("abcd")), tEnd(), tFinish()), []byte("p\x00"))
	tr, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	for i := range b {
		b[i] = 0xff
	}
	if string(tr.Root.Props[0].Value) != "abcd" || tr.Root.Props[0].Name != "p" {
		t.Fatalf("tree aliases the input: %+v", tr.Root.Props[0])
	}
	// A value's capacity ends with it, so append cannot scribble a neighbour.
	tr2, _ := Parse(rawBlob(nil, cat(tBegin(""), tProp(0, []byte("abcd")), tProp(2, []byte("wxyz")), tEnd(), tFinish()), []byte("p\x00q\x00")))
	v := tr2.Root.Props[0].Value
	_ = append(v, 'X')
	if string(tr2.Root.Props[1].Value) != "wxyz" {
		t.Fatal("append to a value overwrote the next one")
	}
}

func TestParseV16(t *testing.T) {
	// Version 16: 36-byte header, no size_dt_struct; the structure block ends
	// where the strings begin.
	st := cat(tBegin(""), tProp(0, []byte{1, 2, 3, 4}), tEnd(), tFinish())
	strs := []byte("p\x00")
	rsv := make([]byte, 16)
	off := 40
	total := off + 16 + len(st) + len(strs)
	// rsvmap at 40 keeps 8-byte alignment; header has 4 bytes of gap.
	b := cat(u32(Magic, uint32(total), uint32(off+16), uint32(off+16+len(st)), uint32(off), 16, 16, 9, uint32(len(strs))), make([]byte, 4), rsv, st, strs)
	tr, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if tr.BootCPUID != 9 || len(tr.Root.Props) != 1 {
		t.Fatalf("bad v16 parse: %+v", tr)
	}
	// Strings before the structure block: the structure block runs to the end.
	strs4 := []byte("p\x00\x00\x00")
	b = cat(u32(Magic, uint32(off+16+len(st)+4), uint32(off+16+4), uint32(off+16), uint32(off), 16, 16, 0, 4), make([]byte, 4), rsv, strs4, st)
	if _, err := Parse(b); err != nil {
		t.Fatal(err)
	}
}

func TestParseV16UnalignedStructEnd(t *testing.T) {
	// The structure block of a v16 blob ends at an unaligned strings offset:
	// the node name's padding would run past it.
	b := v16Blob(cat(u32(tokBeginNode), []byte("abcd\x00")), []byte("x\x00"))
	if _, err := Parse(b); err == nil || !strings.Contains(err.Error(), "runs past") {
		t.Fatalf("name: got %v", err)
	}
	b = v16Blob(cat(tBegin(""), u32(tokProp, 3, 0), []byte{1, 2, 3}), []byte("x\x00"))
	if _, err := Parse(b); err == nil || !strings.Contains(err.Error(), "runs past") {
		t.Fatalf("property: got %v", err)
	}
}

func TestParseVersionErrors(t *testing.T) {
	good := rawBlob(nil, minimalStruct(), nil)
	for name, b := range map[string][]byte{
		"future last_comp_version": setWord(good, 6, 18),
		"old version":              setWord(good, 5, 15),
	} {
		_, err := Parse(b)
		if !errors.Is(err, ErrVersion) || !errors.Is(err, ErrFormat) {
			t.Errorf("%s: got %v, want ErrVersion and ErrFormat", name, err)
		}
	}
	// Newer than we write but compatible with 17 is accepted.
	if _, err := Parse(setWord(setWord(good, 5, 18), 6, 17)); err != nil {
		t.Errorf("version 18, last_comp 17: %v", err)
	}
}

func TestParseHeaderErrors(t *testing.T) {
	good := rawBlob([]Reservation{{1, 2}}, minimalStruct(), []byte("name\x00"))
	n := len(good)
	cases := []struct {
		name string
		b    []byte
		want string
	}{
		{"empty", nil, "shorter than a header"},
		{"27 bytes", good[:27], "shorter than a header"},
		{"magic", setWord(good, 0, 0xd00dfeee), "magic"},
		{"version below last_comp", setWord(setWord(good, 5, 16), 6, 17), "below last_comp"},
		{"short v17 header", cat(good[:28]), "shorter than a version 17 header"},
		{"totalsize below header", setWord(good, 1, 39), "smaller than the header"},
		{"totalsize beyond data", setWord(good, 1, uint32(n)+1), "exceeds"},
		{"totalsize huge", setWord(good, 1, 0xffffffff), "exceeds"},
		{"rsvmap misaligned", setWord(good, 4, 44), "8-byte aligned"},
		{"struct misaligned", setWord(good, 2, 66), "4-byte aligned"},
		{"rsvmap inside header", setWord(good, 4, 32), "does not fit"},
		{"rsvmap past end", setWord(good, 4, uint32(n/8+1)*8), "does not fit"},
		{"rsvmap offset near 2^32", setWord(good, 4, 0xfffffff8), "does not fit"},
		{"struct inside header", setWord(good, 2, 36), "structure block at"},
		{"struct offset near 2^32", setWord(good, 2, 0xfffffffc), "structure block at"},
		{"struct size not multiple of 4", setWord(good, 9, 2), "multiple of 4"},
		{"struct size overflow", setWord(good, 9, 0xfffffff0), "does not fit"},
		{"struct size too small", setWord(good, 9, 4), "does not fit"},
		{"strings past end", setWord(good, 8, 0xffffffff), "strings block"},
		{"strings offset overflow", setWord(good, 3, 0xfffffffc), "strings block"},
		{"strings inside header", setWord(good, 3, 8), "strings block"},
	}
	for _, c := range cases {
		if _, err := ParseHeader(c.b); err == nil || !strings.Contains(err.Error(), c.want) || !errors.Is(err, ErrFormat) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
		if _, err := Parse(c.b); err == nil {
			t.Errorf("%s: Parse accepted it", c.name)
		}
	}
	// The strings block placed over the structure block.
	so := be32(good[8:])
	if _, err := Parse(setWord(good, 3, so)); err == nil || !strings.Contains(err.Error(), "overlaps strings") {
		t.Errorf("strings over structure: got %v", err)
	}
	h, err := ParseHeader(good)
	if err != nil || h.Version != 17 || h.SizeDTStrings != 5 || h.OffMemRsvmap != 40 {
		t.Fatalf("ParseHeader: %+v, %v", h, err)
	}
}

func TestParseReservationErrors(t *testing.T) {
	// No terminator: the last 16 bytes of the blob are not (0, 0).
	good := rawBlob([]Reservation{{1, 2}}, minimalStruct(), nil)
	noTerm := cat(u32(Magic, 40+16, 40, 40, 40, 17, 16, 0, 0, 0), u32(0, 1, 0, 2))
	noTerm = setWord(noTerm, 2, 40+8)
	if _, err := Parse(noTerm); err == nil {
		t.Error("accepted a reservation block with no terminator")
	}
	// The reservation walk runs off the end of the blob, past the entry it
	// started with.
	b := cat(u32(Magic, 72, 64, 72, 40, 17, 16, 0, 0, 8), make([]byte, 0), u32(0, 1, 0, 2), u32(1, 1, 1, 1), u32(tokNop, tokNop))
	if _, err := Parse(b); err == nil || !strings.Contains(err.Error(), "terminator") {
		t.Errorf("got %v", err)
	}
	// Reservation block over the structure block (its terminator sits in a
	// zero tail the structure block is allowed to have after FDT_END).
	st := cat(minimalStruct(), make([]byte, 16))
	ov := rawBlob(nil, st, nil)
	ov = setWord(ov, 4, be32(ov[8:])) // off_mem_rsvmap = off_dt_struct
	if _, err := Parse(ov); err == nil || !strings.Contains(err.Error(), "overlaps the structure") {
		t.Errorf("rsv over struct: got %v", err)
	}
	// Reservation block over the strings block.
	strs := cat([]byte("abcdefghijklmnop"), make([]byte, 16))
	ov = rawBlob(nil, minimalStruct(), strs)
	ov = setWord(ov, 4, be32(ov[12:]))
	if _, err := Parse(ov); err == nil || !strings.Contains(err.Error(), "overlaps the strings") {
		t.Errorf("rsv over strings: got %v", err)
	}
	_ = good
}

func TestParseStructureErrors(t *testing.T) {
	longName := strings.Repeat("n", 100)
	manyChildren := func(dup bool) []byte {
		st := tBegin("")
		for i := 0; i < 12; i++ {
			st = cat(st, tBegin(string(rune('a'+i))), tEnd())
		}
		if dup {
			st = cat(st, tBegin("c"), tEnd())
		}
		return cat(st, tEnd(), tFinish())
	}
	manyProps := func(dup bool) []byte {
		st := tBegin("")
		// 12 distinct names at offsets 0, 2, 4, ...
		for i := 0; i < 12; i++ {
			st = cat(st, tProp(uint32(2*i), nil))
		}
		if dup {
			st = cat(st, tProp(0, nil))
		}
		return cat(st, tEnd(), tFinish())
	}
	strs12 := []byte("a\x00b\x00c\x00d\x00e\x00f\x00g\x00h\x00i\x00j\x00k\x00l\x00")
	deep := tBegin("")
	for i := 0; i < MaxDepth; i++ {
		deep = cat(deep, tBegin("n"))
	}
	for i := 0; i < MaxDepth+1; i++ {
		deep = cat(deep, tEnd())
	}
	deep = cat(deep, tFinish())
	okDepth := tBegin("")
	for i := 0; i < MaxDepth-1; i++ {
		okDepth = cat(okDepth, tBegin("n"))
	}
	for i := 0; i < MaxDepth; i++ {
		okDepth = cat(okDepth, tEnd())
	}
	okDepth = cat(okDepth, tFinish())

	cases := []struct {
		name string
		st   []byte
		strs []byte
		want string
	}{
		{"no end token", cat(tBegin(""), tEnd(), u32(tokNop)), nil, "without FDT_END"},
		{"unterminated node name", cat(u32(tokBeginNode), []byte("abcdefgh")), nil, "not NUL-terminated"},
		{"too deep", deep, nil, "nesting deeper"},
		{"second root", cat(tBegin(""), tEnd(), tBegin(""), tEnd(), tFinish()), nil, "second root"},
		{"named root", cat(tBegin("x"), tEnd(), tFinish()), nil, "root node named"},
		{"empty child name", cat(tBegin(""), tBegin(""), tEnd(), tEnd(), tFinish()), nil, "invalid node name"},
		{"slash in child name", cat(tBegin(""), tBegin("a/b"), tEnd(), tEnd(), tFinish()), nil, "invalid node name"},
		{"duplicate child", cat(tBegin(""), tBegin("a"), tEnd(), tBegin("a"), tEnd(), tEnd(), tFinish()), nil, "duplicate node"},
		{"duplicate child, many", manyChildren(true), nil, "duplicate node"},
		{"end node with no node", cat(tBegin(""), tEnd(), tEnd(), tFinish()), nil, "END_NODE without"},
		{"property with no node", cat(tBegin(""), tEnd(), tProp(0, nil), tFinish()), []byte("p\x00"), "outside any node"},
		{"truncated property", cat(tBegin(""), u32(tokProp, 0)), nil, "truncated property"},
		{"property value past the block", cat(tBegin(""), u32(tokProp, 100, 0), tEnd(), tFinish()), []byte("p\x00"), "runs past"},
		{"property length near 2^32", cat(tBegin(""), u32(tokProp, 0xffffffff, 0), tEnd(), tFinish()), []byte("p\x00"), "runs past"},
		{"name offset outside", cat(tBegin(""), tProp(2, nil), tEnd(), tFinish()), []byte("p\x00"), "outside the"},
		{"name offset near 2^32", cat(tBegin(""), tProp(0xffffffff, nil), tEnd(), tFinish()), []byte("p\x00"), "outside the"},
		{"empty property name", cat(tBegin(""), tProp(1, nil), tEnd(), tFinish()), []byte("p\x00"), "empty property name"},
		{"unterminated short name", cat(tBegin(""), tProp(0, nil), tEnd(), tFinish()), []byte("abc"), "not NUL-terminated"},
		{"unterminated long name", cat(tBegin(""), tProp(0, nil), tEnd(), tFinish()), []byte(longName), "not NUL-terminated"},
		{"property after subnode", cat(tBegin(""), tBegin("a"), tEnd(), tProp(0, nil), tEnd(), tFinish()), []byte("p\x00"), "follows a subnode"},
		{"duplicate property by offset", cat(tBegin(""), tProp(0, nil), tProp(0, nil), tEnd(), tFinish()), []byte("p\x00"), "duplicate property"},
		{"duplicate property by content", cat(tBegin(""), tProp(0, nil), tProp(2, nil), tEnd(), tFinish()), []byte("p\x00p\x00"), "duplicate property"},
		{"duplicate property, many", manyProps(true), strs12, "duplicate property"},
		{"unknown token", cat(tBegin(""), u32(7), tEnd(), tFinish()), nil, "unknown token"},
		{"end inside node", cat(tBegin(""), tFinish()), nil, "inside an open node"},
		{"no root", cat(u32(tokNop), tFinish()), nil, "no root"},
	}
	for _, c := range cases {
		_, err := Parse(rawBlob(nil, c.st, c.strs))
		if err == nil || !errors.Is(err, ErrFormat) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
	for name, st := range map[string][]byte{
		"max depth":     okDepth,
		"many children": manyChildren(false),
	} {
		if _, err := Parse(rawBlob(nil, st, nil)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Parse(rawBlob(nil, manyProps(false), strs12)); err != nil {
		t.Errorf("many props: %v", err)
	}
	// A long property name, found by the NUL index.
	tr, err := Parse(rawBlob(nil, cat(tBegin(""), tProp(0, nil), tProp(70, nil), tEnd(), tFinish()), append([]byte(longName+"\x00"), "zz\x00"...)))
	if err != nil || tr.Root.Props[0].Name != longName {
		t.Errorf("long name: %v", err)
	}
	// A second long name hits the same NUL table.
	if _, err := Parse(rawBlob(nil, cat(tBegin(""), tProp(0, nil), tProp(50, nil), tEnd(), tFinish()), append([]byte(longName+"\x00"), "zz\x00"...))); err != nil {
		t.Errorf("long name tail: %v", err)
	}
}

// A blob that makes every property point at the same long NUL-free run must
// not take quadratic time: the NUL table answers each lookup by search.
func TestParseNameScanIsBounded(t *testing.T) {
	strs := append([]byte(strings.Repeat("x", 1<<16)), 0)
	st := tBegin("")
	for i := 0; i < 2000; i++ {
		st = cat(st, tProp(uint32(i), nil))
	}
	st = cat(st, tEnd(), tFinish())
	if _, err := Parse(rawBlob(nil, st, strs)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		// Distinct offsets into one run are distinct names of different
		// lengths, so this parses; the point is only that it returns quickly.
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeepTreeDoesNotRecurseUnbounded(t *testing.T) {
	st := tBegin("")
	for i := 0; i < 100000; i++ {
		st = append(st, tBegin("n")...)
	}
	if _, err := Parse(rawBlob(nil, st, nil)); err == nil {
		t.Fatal("accepted 100000 nested nodes")
	}
}
