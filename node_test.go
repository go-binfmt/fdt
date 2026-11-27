package fdt

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	tr := sampleTree()
	cases := []struct {
		path string
		want string // node name, "" for root
		err  bool
	}{
		{"/", "", false},
		{"//", "", false},
		{"/cpus", "cpus", false},
		{"/cpus/cpu@0", "cpu@0", false},
		{"/cpus/cpu", "cpu@0", false}, // unit address omitted
		{"/memory", "memory@80000000", false},
		{"/memory@80000000/", "memory@80000000", false},
		{"/memory@1", "", true},
		{"/nothing", "", true},
		{"/cpus/cpu@0/x", "", true},
		{"relative", "", true},
	}
	for _, c := range cases {
		n, err := tr.Find(c.path)
		if c.err {
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("%q: got %v, want ErrNotFound", c.path, err)
			}
			continue
		}
		if err != nil || n.Name != c.want {
			t.Errorf("%q: got %v, %v", c.path, n, err)
		}
	}
	// An exact name beats a unit-address match that comes first.
	r := New().Root
	r.AddChild("a@1")
	r.AddChild("a")
	if r.Child("a").Name != "a" {
		t.Error("exact match lost to a unit-address match")
	}
	if r.Child("a@2") != nil {
		t.Error("a name with a unit address matched a different one")
	}
	if n, _ := r.Lookup("a@1"); n.Name != "a@1" {
		t.Error("exact unit address lookup failed")
	}
}

func TestWalk(t *testing.T) {
	tr := sampleTree()
	var paths []string
	tr.Root.Walk(func(p string, n *Node) bool { paths = append(paths, p); return true })
	want := []string{"/", "/cpus", "/cpus/cpu@0", "/memory@80000000"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths %v, want %v", paths, want)
	}
	paths = nil
	tr.Root.Walk(func(p string, n *Node) bool { paths = append(paths, p); return p != "/cpus" })
	if !reflect.DeepEqual(paths, []string{"/", "/cpus"}) {
		t.Fatalf("stopped walk visited %v", paths)
	}
	paths = nil
	tr.Root.Walk(func(p string, n *Node) bool { paths = append(paths, p); return p != "/cpus/cpu@0" })
	if !reflect.DeepEqual(paths, []string{"/", "/cpus", "/cpus/cpu@0"}) {
		t.Fatalf("walk stopped in a grandchild visited %v", paths)
	}
}

func TestGetters(t *testing.T) {
	tr := sampleTree()
	r := tr.Root
	if v, err := r.Uint32("#address-cells"); err != nil || v != 1 {
		t.Errorf("Uint32: %v %v", v, err)
	}
	if v, err := r.Uint64("big"); err != nil || v != 0x0102030405060708 {
		t.Errorf("Uint64: %#x %v", v, err)
	}
	if v, err := r.Cell("#address-cells", 1); err != nil || v != 1 {
		t.Errorf("Cell 1: %v %v", v, err)
	}
	if v, err := r.Cell("big", 2); err != nil || v != 0x0102030405060708 {
		t.Errorf("Cell 2: %v %v", v, err)
	}
	if _, err := r.Cell("big", 3); !errors.Is(err, ErrValue) {
		t.Errorf("Cell 3: %v", err)
	}
	if s, err := r.String("model"); err != nil || s != "demo,board" {
		t.Errorf("String: %q %v", s, err)
	}
	if l, err := r.StringList("compatible"); err != nil || !reflect.DeepEqual(l, []string{"demo,board", "demo,soc"}) {
		t.Errorf("StringList: %v %v", l, err)
	}
	if l, err := r.StringList("dma-coherent"); err != nil || len(l) != 0 {
		t.Errorf("empty StringList: %v %v", l, err)
	}
	mem, _ := tr.Find("/memory")
	if v, err := mem.Uint32s("reg"); err != nil || !reflect.DeepEqual(v, []uint32{0x80000000, 0x10000000}) {
		t.Errorf("Uint32s: %v %v", v, err)
	}
	if v, err := r.Bytes("blob"); err != nil || !bytes.Equal(v, []byte{1, 2, 3, 4, 5}) {
		t.Errorf("Bytes: %v %v", v, err)
	}

	// Every getter on a missing property.
	for name, f := range map[string]func() error{
		"Bytes":      func() error { _, e := r.Bytes("zz"); return e },
		"Uint32":     func() error { _, e := r.Uint32("zz"); return e },
		"Uint64":     func() error { _, e := r.Uint64("zz"); return e },
		"Cell":       func() error { _, e := r.Cell("zz", 1); return e },
		"Uint32s":    func() error { _, e := r.Uint32s("zz"); return e },
		"String":     func() error { _, e := r.String("zz"); return e },
		"StringList": func() error { _, e := r.StringList("zz"); return e },
	} {
		if err := f(); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s on a missing property: %v", name, err)
		}
	}
	// And on the wrong shape.
	for name, f := range map[string]func() error{
		"Uint32":     func() error { _, e := r.Uint32("big"); return e },
		"Uint64":     func() error { _, e := r.Uint64("#address-cells"); return e },
		"Uint32s":    func() error { _, e := r.Uint32s("blob"); return e },
		"String":     func() error { _, e := r.String("compatible"); return e },
		"String 0":   func() error { _, e := r.String("dma-coherent"); return e },
		"String raw": func() error { _, e := r.String("blob"); return e },
		"StringList": func() error { _, e := r.StringList("blob"); return e },
	} {
		if err := f(); !errors.Is(err, ErrValue) {
			t.Errorf("%s on the wrong shape: %v", name, err)
		}
	}
}

func TestSetters(t *testing.T) {
	n := New().Root
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(n.SetUint32("a", 1))
	must(n.SetUint32("a", 2)) // replace in place
	if len(n.Props) != 1 {
		t.Fatalf("replace appended: %v", n.Props)
	}
	must(n.SetCell("c1", 1, 0xffffffff))
	must(n.SetCell("c2", 2, 1<<40))
	if err := n.SetCell("c1", 1, 1<<32); !errors.Is(err, ErrValue) {
		t.Errorf("SetCell overflow: %v", err)
	}
	if err := n.SetCell("c1", 4, 1); !errors.Is(err, ErrValue) {
		t.Errorf("SetCell cells: %v", err)
	}
	if v, _ := n.Cell("c2", 2); v != 1<<40 {
		t.Errorf("c2 = %#x", v)
	}
	must(n.SetString("s", "hi"))
	must(n.SetStringList("l", "a", "", "b"))
	if l, _ := n.StringList("l"); !reflect.DeepEqual(l, []string{"a", "", "b"}) {
		t.Errorf("list with an empty string: %q", l)
	}
	for _, f := range []func() error{
		func() error { return n.SetString("s", "a\x00b") },
		func() error { return n.SetStringList("l", "ok", "a\x00b") },
		func() error { return n.Set("", nil) },
		func() error { return n.Set("a\x00", nil) },
		func() error { return n.SetUint64("", 1) },
	} {
		if err := f(); !errors.Is(err, ErrValue) {
			t.Errorf("bad setter: %v", err)
		}
	}
	if !n.DeleteProp("s") || n.DeleteProp("s") {
		t.Error("DeleteProp result wrong")
	}
	if n.Prop("s") != nil || n.Prop("a") == nil {
		t.Error("DeleteProp removed the wrong property")
	}

	c, err := n.AddChild("c")
	must(err)
	if _, err := n.AddChild("c"); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate AddChild: %v", err)
	}
	for _, bad := range []string{"", "a/b", "a\x00"} {
		if _, err := n.AddChild(bad); !errors.Is(err, ErrFormat) {
			t.Errorf("AddChild(%q): %v", bad, err)
		}
	}
	deep, err := n.Ensure("/x/y//z")
	must(err)
	again, err := n.Ensure("x/y/z")
	must(err)
	if deep != again || deep.Name != "z" {
		t.Error("Ensure did not reuse existing nodes")
	}
	if got, _ := n.Ensure("/"); got != n {
		t.Error("Ensure of the root path")
	}
	if _, err := n.Ensure("x/a\x00"); err == nil {
		t.Error("Ensure created a bad name")
	}
	if !n.DeleteChild("c") || n.DeleteChild("c") {
		t.Error("DeleteChild result wrong")
	}
	_ = c
	// What was built must marshal and parse back.
	tr := &Tree{Root: n}
	b, err := tr.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if back, err := Parse(b); err != nil || !treesEqual(tr, back) {
		t.Fatalf("built tree does not round trip: %v", err)
	}
}

func TestWriteDTS(t *testing.T) {
	var sb strings.Builder
	if err := sampleTree().WriteDTS(&sb); err != nil {
		t.Fatal(err)
	}
	got := sb.String()
	for _, want := range []string{
		"/dts-v1/;", "/memreserve/ 0x1000 0x2000;", "/ {", "\t#address-cells = <0x1>;",
		"model = \"demo,board\";", "compatible = \"demo,board\", \"demo,soc\";", "dma-coherent;",
		"blob = [01 02 03 04 05];", "\t\tcpu@0 {", "reg = <0x80000000 0x10000000>;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dts lacks %q:\n%s", want, got)
		}
	}
	q := New()
	q.Root.SetString("q", `a"b\c`)
	sb.Reset()
	if err := q.WriteDTS(&sb); err != nil || !strings.Contains(sb.String(), `"a\"b\\c"`) {
		t.Errorf("escapes: %v\n%s", err, sb.String())
	}
	bad := New()
	bad.Root.AddChild("white space")
	if err := bad.WriteDTS(&sb); !errors.Is(err, ErrFormat) {
		t.Errorf("bad node name: %v", err)
	}
	bad = New()
	bad.Root.SetEmpty("bad name")
	if err := bad.WriteDTS(&sb); !errors.Is(err, ErrFormat) {
		t.Errorf("bad property name: %v", err)
	}
	if err := (&Tree{}).WriteDTS(&sb); !errors.Is(err, ErrFormat) {
		t.Errorf("no root: %v", err)
	}
	if dtsName("") || dtsName("a b") || !dtsName("#x,y.z_+?*@0-9") {
		t.Error("dtsName wrong")
	}
	for _, v := range [][]byte{{0}, {0, 'a', 0}, {'a'}, {'a', 0, 0}, {1, 0}, {0xff, 0}} {
		if s := dtsValue(v); strings.HasPrefix(s, "\"") {
			t.Errorf("%v printed as a string: %s", v, s)
		}
	}
}
