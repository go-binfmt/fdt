package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/go-binfmt/fdt"
)

func writeBlob(t *testing.T) string {
	t.Helper()
	tr := fdt.New()
	r := tr.Root
	r.SetStringList("compatible", "demo,board", "demo,soc")
	r.SetUint32("#address-cells", 1)
	r.SetUint64("big", 1<<40)
	r.Set("blob", []byte{1, 2, 3})
	r.SetEmpty("flag")
	r.Set("raw", []byte{0xff, 0, 0xfe, 0})
	cpus, _ := r.AddChild("cpus")
	c0, _ := cpus.AddChild("cpu@0")
	c0.SetUint32("reg", 0)
	b, err := tr.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "t.dtb")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func run(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := RootCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestCommands(t *testing.T) {
	p := writeBlob(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"check", p}, "ok: "},
		{[]string{"dump", p}, "compatible = \"demo,board\", \"demo,soc\";"},
		{[]string{"ls", p}, "cpus\n"},
		{[]string{"ls", p, "/cpus"}, "cpu@0\n"},
		{[]string{"get", p, "/"}, "compatible\n#address-cells\n"},
		{[]string{"get", p, "/", "compatible"}, "demo,board demo,soc\n"},
		{[]string{"get", p, "/", "#address-cells"}, "0x1\n"},
		{[]string{"get", p, "/", "big"}, "0x100 0x0\n"},
		{[]string{"get", p, "/", "blob"}, "01 02 03\n"},
		{[]string{"get", p, "/", "flag"}, "\n"},
		{[]string{"get", p, "/", "raw", "-t", "x"}, "ff 00 fe 00\n"},
		{[]string{"get", p, "/", "raw"}, "0xff00fe00\n"},
		{[]string{"get", p, "/", "#address-cells", "-t", "u32"}, "1\n"},
		{[]string{"get", p, "/", "big", "-t", "u64"}, "1099511627776\n"},
		{[]string{"get", p, "/", "compatible", "-t", "s"}, "demo,board demo,soc\n"},
		{[]string{"get", p, "/cpus/cpu", "reg"}, "0x0\n"},
		{[]string{"--version"}, "fdt "},
		{[]string{}, "Usage:"},
	}
	for _, c := range cases {
		out, err := run(t, "", c.args...)
		if err != nil || !strings.Contains(out, c.want) {
			t.Errorf("%v: got %q, %v; want %q", c.args, out, err, c.want)
		}
	}
	// Stdin.
	b, _ := os.ReadFile(p)
	if out, err := run(t, string(b), "check", "-"); err != nil || !strings.HasPrefix(out, "ok: ") {
		t.Errorf("stdin: %q %v", out, err)
	}
}

func TestCommandErrors(t *testing.T) {
	p := writeBlob(t)
	bad := filepath.Join(t.TempDir(), "bad.dtb")
	os.WriteFile(bad, []byte("not a dtb at all, not even close to one"), 0o644)
	for _, args := range [][]string{
		{"check", filepath.Join(t.TempDir(), "missing")},
		{"check", bad},
		{"dump", bad},
		{"ls", bad},
		{"get", bad, "/"},
		{"ls", p, "/nothing"},
		{"get", p, "/nothing"},
		{"get", p, "/", "missing"},
		{"get", p, "/", "missing", "-t", "s"},
		{"get", p, "/", "blob", "-t", "u32"},
		{"get", p, "/", "compatible", "-t", "u64"},
		{"get", p, "/", "missing", "-t", "x"},
		{"get", p, "/", "compatible", "-t", "bogus"},
		{"check"},
	} {
		if _, err := run(t, "", args...); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
	// A malformed blob is refused by the parser, by every command that loads.
	_, want := run(t, "", "check", bad)
	for _, args := range [][]string{{"check", bad}, {"dump", bad}, {"ls", bad}, {"get", bad, "/"}} {
		if _, err := run(t, "", args...); !errors.Is(err, fdt.ErrFormat) || err.Error() != want.Error() {
			t.Errorf("%v: %v, want the parser's %v", args, err, want)
		}
	}
	// A missing file names itself.
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := run(t, "", "check", missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("missing file: %v", err)
	}
	// A read that fails midway is an error, not a short blob.
	cmd := RootCmd()
	cmd.SetIn(failingReader{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"check", "-"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("failing stdin: %v", err)
	}
	old := maxInput
	maxInput = 10
	defer func() { maxInput = old }()
	if _, err := run(t, "", "check", p); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("oversized input: %v", err)
	}
	// A directory is not a blob: reading it fails, which must be an error.
	maxInput = old
	if _, err := run(t, "", "check", t.TempDir()); err == nil {
		t.Error("directory accepted")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestVersionFlag(t *testing.T) {
	out, err := run(t, "", "--version")
	if err != nil || out != "fdt "+version()+"\n" {
		t.Errorf("--version: %q %v", out, err)
	}
}

func TestVersionAndMain(t *testing.T) {
	oldV, oldB := Version, readBuildInfo
	defer func() { Version, readBuildInfo = oldV, oldB }()
	Version = "v9.9.9"
	readBuildInfo = func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, true }
	if version() != "v9.9.9" {
		t.Error("ldflags version must win over build info")
	}
	Version = "dev"
	readBuildInfo = func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, true }
	if version() != "v1.2.3" {
		t.Error("build info version ignored")
	}
	readBuildInfo = func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true }
	if version() != "dev" {
		t.Error("(devel) must read as dev")
	}
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	if version() != "dev" {
		t.Error("no build info must read as dev")
	}

	// main exits 1 and names the tool on an error.
	oldExit, oldArgs := osExit, os.Args
	defer func() { osExit, os.Args = oldExit, oldArgs }()
	code := -1
	osExit = func(c int) { code = c }
	os.Args = []string{"fdt", "check", filepath.Join(t.TempDir(), "missing")}
	main()
	if code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	code = -1
	os.Args = []string{"fdt", "--version"}
	main()
	if code != -1 {
		t.Errorf("--version exited %d", code)
	}
}

func TestPrintable(t *testing.T) {
	for v, want := range map[string]bool{
		"a\x00":         true,
		"a\x00b\x00":    true,
		"a":             false,
		"\x00":          false,
		"a\x00\x00":     false,
		"\x01\x00":      false,
		"a\x00\xff\x00": false,
	} {
		if printable([]byte(v)) != want {
			t.Errorf("printable(%q) = %v", v, !want)
		}
	}
}
