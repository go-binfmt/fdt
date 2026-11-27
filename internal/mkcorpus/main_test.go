package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The test binary doubles as the dtc the program drives: with FAKE_DTC set it
// prints what a compiler would, so no real dtc is needed.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_DTC") == "1" {
		args := os.Args[1:]
		file := args[len(args)-1]
		ver := "17"
		for i, a := range args {
			if a == "-V" {
				ver = args[i+1]
			}
		}
		switch {
		case strings.Contains(file, "bad"):
			os.Exit(1)
		case strings.Contains(file, "empty"):
			// success with no output
		case strings.Contains(file, "v17only") && ver == "16":
			os.Exit(1)
		default:
			os.Stdout.WriteString("blob-" + ver + "-" + filepath.Base(file))
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func source(t *testing.T, names ...string) string {
	t.Helper()
	src := t.TempDir()
	if err := os.Mkdir(filepath.Join(src, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(src, "tests", n+".dts"), []byte("/dts-v1/;"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

func TestRun(t *testing.T) {
	t.Setenv("FAKE_DTC", "1")
	src, root := source(t, "good", "bad", "empty", "v17only"), t.TempDir()
	if err := run(src, os.Args[0], root); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"testdata/dtc/good.dtb":                "blob-17-good.dts",
		"testdata/dtc/good.v16.dtb":            "blob-16-good.dts",
		"testdata/dtc/v17only.dtb":             "blob-17-v17only.dts",
		"testdata/fuzz/FuzzParse/dtc-good":     "go test fuzz v1\n[]byte(\"blob-17-good.dts\")\n",
		"testdata/fuzz/FuzzParse/dtc-good.v16": "go test fuzz v1\n[]byte(\"blob-16-good.dts\")\n",
		"testdata/fuzz/FuzzParse/dtc-v17only":  "go test fuzz v1\n[]byte(\"blob-17-v17only.dts\")\n",
	}
	for p, v := range want {
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil || string(b) != v {
			t.Errorf("%s: %q, %v; want %q", p, b, err, v)
		}
	}
	for _, p := range []string{"bad", "empty", "v17only.v16"} {
		if _, err := os.Stat(filepath.Join(root, "testdata/dtc", p+".dtb")); err == nil {
			t.Errorf("%s was written", p)
		}
	}
}

func TestRunErrors(t *testing.T) {
	t.Setenv("FAKE_DTC", "1")
	// A malformed glob pattern.
	if err := run("[", os.Args[0], t.TempDir()); err == nil {
		t.Error("bad pattern accepted")
	}
	// The output directory cannot be created: root is a file.
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, nil, 0o644)
	if err := run(source(t, "good"), os.Args[0], f); err == nil || !strings.Contains(err.Error(), "mkdir") {
		t.Error("unwritable root accepted")
	}
	// The blob cannot be written: its path is a directory.
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "testdata/dtc/good.dtb"), 0o755)
	if err := run(source(t, "good"), os.Args[0], root); err == nil {
		t.Error("unwritable blob accepted")
	}
	// The seed cannot be written.
	root = t.TempDir()
	os.MkdirAll(filepath.Join(root, "testdata/fuzz/FuzzParse/dtc-good"), 0o755)
	if err := run(source(t, "good"), os.Args[0], root); err == nil {
		t.Error("unwritable seed accepted")
	}
}
