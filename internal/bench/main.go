// Command bench measures fdt against libfdt (C), single core, on the same
// files: Parse against fdt_check_full plus a visit of every node and property,
// Marshal against fdt_create ... fdt_finish replaying the same operations. The
// two sides run interleaved, round by round, and the table shows medians of
// the per-round ns/op. Run it pinned: taskset -c 0 bench ...
//
//	go run ./internal/bench -dir DIR [-cc cc] [-cflags "-I/opt/homebrew/include"] [-ldflags "-L/opt/homebrew/lib -lfdt"]
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-binfmt/fdt"
)

func main() {
	dir := flag.String("dir", "", "directory for the generated blobs and the C binary (required)")
	cc := flag.String("cc", "cc", "C compiler")
	cflags := flag.String("cflags", "", "extra C flags, e.g. -I/opt/homebrew/include")
	ldflags := flag.String("ldflags", "-lfdt", "linker flags")
	rounds := flag.Int("rounds", 15, "interleaved rounds")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "bench: -dir is required")
		os.Exit(2)
	}
	if err := run(*dir, *cc, *cflags, *ldflags, *rounds); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}

// boardTree builds n device nodes under /soc with the usual mix of
// properties, plus, when dataBytes > 0, a FIT-like /images with one large data
// property.
func boardTree(n, dataBytes int) *fdt.Tree {
	t := fdt.New()
	r := t.Root
	r.SetUint32("#address-cells", 1)
	r.SetUint32("#size-cells", 1)
	r.SetString("model", "bench,board")
	r.SetStringList("compatible", "bench,board", "bench,soc")
	soc, _ := r.AddChild("soc")
	soc.SetEmpty("ranges")
	for i := 0; i < n; i++ {
		d, _ := soc.AddChild(fmt.Sprintf("device@%x", 0x10000000+i*0x1000))
		d.SetStringList("compatible", "bench,device-v2", "bench,device")
		d.Set("reg", []byte{0x10, 0, byte(i >> 8), byte(i), 0, 0, 0x10, 0})
		d.SetUint32("interrupts", uint32(i%256))
		d.SetString("status", "okay")
		d.SetStringList("clock-names", "core", "bus")
		d.SetUint32("phandle", uint32(i+1))
		if i%4 == 0 {
			d.SetEmpty("dma-coherent")
			c, _ := d.AddChild("port")
			c.SetUint32("reg", 0)
			c.SetString("label", "p0")
		}
	}
	if dataBytes > 0 {
		img, _ := r.AddChild("images")
		for i := 0; i < 3; i++ {
			k, _ := img.AddChild(fmt.Sprintf("image-%d", i))
			k.SetString("type", "kernel")
			k.SetString("compression", "none")
			k.Set("data", make([]byte, dataBytes))
			h, _ := k.AddChild("hash-1")
			h.SetString("algo", "sha256")
			h.Set("value", make([]byte, 32))
		}
	}
	return t
}

type benchCase struct {
	name string
	tree *fdt.Tree
}

func run(dir, cc, cflags, ldflags string, rounds int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	bin := filepath.Join(dir, "libfdt_bench")
	_, self, _, _ := runtime.Caller(0)
	args := append([]string{"-O2", "-o", bin, filepath.Join(filepath.Dir(self), "libfdt", "libfdt_bench.c")}, strings.Fields(cflags)...)
	args = append(args, strings.Fields(ldflags)...)
	if out, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("compiling the libfdt side: %v\n%s", err, out)
	}
	cases := []benchCase{
		{"board 40 nodes", boardTree(40, 0)},
		{"board 2000 nodes", boardTree(2000, 0)},
		{"board 100000 nodes", boardTree(100000, 0)},
		{"FIT-like, 3 x 8 MiB data", boardTree(10, 8<<20)},
	}
	type sample struct{ cParse, goParse, cWrite, goWrite []float64 }
	var res = make([]sample, len(cases))
	files := make([]string, len(cases))
	blobs := make([][]byte, len(cases))
	for i, c := range cases {
		b, err := c.tree.Marshal()
		if err != nil {
			return err
		}
		blobs[i] = b
		files[i] = filepath.Join(dir, fmt.Sprintf("case%d.dtb", i))
		if err := os.WriteFile(files[i], b, 0o644); err != nil {
			return err
		}
	}
	for r := 0; r < rounds; r++ {
		for i := range cases {
			p, err := cNs(bin, "parse", files[i])
			if err != nil {
				return err
			}
			res[i].cParse = append(res[i].cParse, p)
			res[i].goParse = append(res[i].goParse, goNs(func() {
				if _, err := fdt.Parse(blobs[i]); err != nil {
					panic(err)
				}
			}))
			w, err := cNs(bin, "write", files[i])
			if err != nil {
				return err
			}
			res[i].cWrite = append(res[i].cWrite, w)
			tr := cases[i].tree
			res[i].goWrite = append(res[i].goWrite, goNs(func() {
				if _, err := tr.Marshal(); err != nil {
					panic(err)
				}
			}))
		}
	}
	fmt.Printf("fdt vs libfdt (C, -O2), single core, %d interleaved rounds, median ns/op, %s/%s, %s\n\n", rounds, runtime.GOOS, runtime.GOARCH, runtime.Version())
	fmt.Println("| blob | operation | libfdt | fdt | libfdt / fdt |")
	fmt.Println("|---|---|---:|---:|---:|")
	for i, c := range cases {
		size := fmt.Sprintf("%s (%s)", c.name, human(len(blobs[i])))
		row(size, "parse", res[i].cParse, res[i].goParse)
		row(size, "write", res[i].cWrite, res[i].goWrite)
	}
	return nil
}

func row(blob, op string, c, g []float64) {
	cm, gm := median(c), median(g)
	fmt.Printf("| %s | %s | %s | %s | %.2fx |\n", blob, op, dur(cm), dur(gm), cm/gm)
}

func median(v []float64) float64 {
	s := slices.Clone(v)
	slices.Sort(s)
	return s[len(s)/2]
}

func human(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func dur(ns float64) string {
	switch {
	case ns >= 1e6:
		return fmt.Sprintf("%.2f ms", ns/1e6)
	case ns >= 1e3:
		return fmt.Sprintf("%.1f us", ns/1e3)
	}
	return fmt.Sprintf("%.0f ns", ns)
}

func cNs(bin, mode, file string) (float64, error) {
	out, err := exec.Command(bin, mode, file).Output()
	if err != nil {
		return 0, fmt.Errorf("libfdt %s %s: %w", mode, file, err)
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		return 0, fmt.Errorf("libfdt %s: unexpected output %q", mode, out)
	}
	return strconv.ParseFloat(f[0], 64)
}

// goNs times fn the way the C side does: nine samples of at least 20 ms, the
// median ns per call.
func goNs(fn func()) float64 {
	fn() // warm up
	var samples []float64
	for s := 0; s < 9; s++ {
		runtime.GC()
		iters := 0
		t0 := time.Now()
		var el time.Duration
		for el < 20*time.Millisecond {
			fn()
			iters++
			el = time.Since(t0)
		}
		samples = append(samples, float64(el.Nanoseconds())/float64(iters))
	}
	return median(samples)
}
