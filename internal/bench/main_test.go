package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-binfmt/fdt"
)

// The test binary doubles as the C compiler the driver invokes: with FAKE_CC
// set it writes, at the -o path, a shell script standing in for the libfdt
// program, whose body is FAKE_CC_BODY.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_CC") == "1" {
		if os.Getenv("FAKE_CC_FAIL") == "1" {
			os.Stderr.WriteString("fake cc: refusing\n")
			os.Exit(1)
		}
		for i, a := range os.Args {
			if a == "-o" {
				if err := os.WriteFile(os.Args[i+1], []byte("#!/bin/sh\n"+os.Getenv("FAKE_CC_BODY")+"\n"), 0o755); err != nil {
					os.Exit(1)
				}
			}
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func tiny() []benchCase { return []benchCase{{"tiny", boardTree(3, 16)}} }

func TestRun(t *testing.T) {
	t.Setenv("FAKE_CC", "1")
	t.Setenv("FAKE_CC_BODY", "echo 1000 7")
	if err := run(filepath.Join(t.TempDir(), "d"), os.Args[0], "-Ix", "-lfdt", 1, tiny()); err != nil {
		t.Fatal(err)
	}
}

func TestRunErrors(t *testing.T) {
	t.Setenv("FAKE_CC", "1")
	t.Setenv("FAKE_CC_BODY", "echo 1000 7")
	run1 := func(dir string, cases []benchCase) error {
		return run(dir, os.Args[0], "", "", 1, cases)
	}
	// dir cannot be created.
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, nil, 0o644)
	if err := run1(filepath.Join(f, "d"), tiny()); err == nil || !strings.Contains(err.Error(), "mkdir") {
		t.Errorf("uncreatable directory: %v", err)
	}
	// the compiler fails.
	t.Setenv("FAKE_CC_FAIL", "1")
	if err := run1(t.TempDir(), tiny()); err == nil || !strings.Contains(err.Error(), "fake cc: refusing") {
		t.Errorf("compiler failure: %v", err)
	}
	t.Setenv("FAKE_CC_FAIL", "0")
	// a tree that cannot be marshalled.
	if err := run1(t.TempDir(), []benchCase{{"bad", &fdt.Tree{}}}); err == nil || !strings.Contains(err.Error(), "no root") {
		t.Errorf("unmarshallable tree: %v", err)
	}
	// the blob cannot be written: its path is a directory.
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "case0.dtb"), 0o755)
	if run1(dir, tiny()) == nil {
		t.Error("unwritable blob accepted")
	}
	// the libfdt side fails, in each mode.
	for body, want := range map[string]string{
		"exit 1":                                     "exit status 1",
		`[ "$1" = parse ] && echo 1 1 || exit 1`:     "libfdt write",
		`[ "$1" = write ] && echo 1 1 || exit 1`:     "libfdt parse",
		"echo garbage":                               "unexpected output",
		`[ "$1" = parse ] && echo 1 1 || echo 1 2 3`: "unexpected output",
	} {
		t.Setenv("FAKE_CC_BODY", body)
		if err := run1(t.TempDir(), tiny()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("libfdt side %q: %v, want %q", body, err, want)
		}
	}
}

func TestBoardTree(t *testing.T) {
	b, err := boardTree(9, 64).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	tr, err := fdt.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Find("/images/image-2"); err != nil {
		t.Error(err)
	}
	if _, err := tr.Find("/soc/device@10000000/port"); err != nil {
		t.Error(err)
	}
	if len(defaultCases()) != 4 {
		t.Error("default cases changed")
	}
}

func TestFormat(t *testing.T) {
	for n, want := range map[int]string{5: "5 B", 2048: "2.0 KiB", 3 << 20: "3.0 MiB"} {
		if human(n) != want {
			t.Errorf("human(%d) = %s", n, human(n))
		}
	}
	for ns, want := range map[float64]string{5: "5 ns", 2500: "2.5 us", 3e6: "3.00 ms"} {
		if dur(ns) != want {
			t.Errorf("dur(%v) = %s", ns, dur(ns))
		}
	}
	if median([]float64{3, 1, 2}) != 2 {
		t.Error("median")
	}
}
