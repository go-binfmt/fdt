// Command mkcorpus regenerates testdata/dtc from the dtc test suite: every
// tests/*.dts of a dtc checkout compiled by the installed dtc, as a version 17
// blob (testdata/dtc/NAME.dtb) and as a version 16 blob (NAME.v16.dtb), plus
// each blob as a Go fuzz seed (testdata/fuzz/FuzzParse/dtc-NAME). Sources that
// dtc refuses (the suite has many deliberately bad ones) are skipped.
//
//	go run ./internal/mkcorpus -dtc-src ../dgibson-dtc-src
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	src := flag.String("dtc-src", "", "dtc checkout (its tests/ directory holds the .dts files)")
	dtc := flag.String("dtc", "dtc", "dtc executable")
	flag.Parse()
	if *src == "" {
		fmt.Fprintln(os.Stderr, "mkcorpus: -dtc-src is required")
		os.Exit(2)
	}
	if err := run(*src, *dtc); err != nil {
		fmt.Fprintln(os.Stderr, "mkcorpus:", err)
		os.Exit(1)
	}
}

func run(src, dtc string) error {
	files, err := filepath.Glob(filepath.Join(src, "tests", "*.dts"))
	if err != nil {
		return err
	}
	for _, d := range []string{"testdata/dtc", "testdata/fuzz/FuzzParse"} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	ok := 0
	for _, f := range files {
		base := strings.TrimSuffix(filepath.Base(f), ".dts")
		var made bool
		for _, v := range []struct{ ver, suffix string }{{"17", ""}, {"16", ".v16"}} {
			// dtc resolves /include/ and /incbin/ relative to the source file
			// and its -i directories.
			cmd := exec.Command(dtc, "-q", "-I", "dts", "-O", "dtb", "-V", v.ver, "-i", filepath.Join(src, "tests"), f)
			out, err := cmd.Output()
			if err != nil || len(out) == 0 {
				continue
			}
			if err := os.WriteFile("testdata/dtc/"+base+v.suffix+".dtb", out, 0o644); err != nil {
				return err
			}
			seed := fmt.Sprintf("go test fuzz v1\n[]byte(%q)\n", out)
			if err := os.WriteFile("testdata/fuzz/FuzzParse/dtc-"+base+v.suffix, []byte(seed), 0o644); err != nil {
				return err
			}
			made = true
		}
		if made {
			ok++
		}
	}
	fmt.Printf("%d of %d sources compiled\n", ok, len(files))
	return nil
}
