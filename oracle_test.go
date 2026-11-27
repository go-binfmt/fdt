package fdt

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The reference tools are dtc and fdtget. In CI FDT_REQUIRE_DTC=1 turns a
// missing tool into a failure, so a runner image that loses them cannot turn
// the lane into a green skip.
func needTool(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		if os.Getenv("FDT_REQUIRE_DTC") != "" {
			t.Fatalf("%s is required (FDT_REQUIRE_DTC) but not found: %v", name, err)
		}
		t.Skipf("%s not installed", name)
	}
	return p
}

func dtcFromDTS(t *testing.T, dtc, dts string, boot uint32) []byte {
	t.Helper()
	cmd := exec.Command(dtc, "-q", "-f", "-I", "dts", "-O", "dtb", "-b", fmt.Sprint(boot))
	cmd.Stdin = strings.NewReader(dts)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("dtc rejected our source: %v\n%s\n%s", err, errb.String(), dts)
	}
	return out.Bytes()
}

// Marshal against dtc: the same tree, written as dts and compiled by dtc,
// must give the same bytes.
func TestMarshalMatchesDtcRandom(t *testing.T) {
	dtc := needTool(t, "dtc")
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 150; i++ {
		tr := randomTree(rng)
		var sb strings.Builder
		if err := tr.WriteDTS(&sb); err != nil {
			t.Fatal(err)
		}
		want := dtcFromDTS(t, dtc, sb.String(), tr.BootCPUID)
		got, err := tr.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("tree %d differs from dtc (%d vs %d bytes)\n%s", i, len(got), len(want), sb.String())
		}
	}
}

// Every blob of the dtc test suite: Parse then Marshal is the identity, and
// so is Parse then WriteDTS then dtc when the names allow dts.
func TestCorpusRoundTripAndDtc(t *testing.T) {
	var dtc string
	if p, err := exec.LookPath("dtc"); err == nil {
		dtc = p
	} else if os.Getenv("FDT_REQUIRE_DTC") != "" {
		t.Fatalf("dtc is required: %v", err)
	}
	viaDTS := 0
	for _, f := range corpus(t) {
		if strings.HasSuffix(f, ".v16.dtb") {
			continue
		}
		b := mustRead(t, f)
		tr, err := Parse(b)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		got, err := tr.Marshal()
		if err != nil {
			t.Errorf("%s: Marshal: %v", f, err)
			continue
		}
		if !bytes.Equal(got, b) {
			t.Errorf("%s: Marshal(Parse(blob)) differs from the blob dtc wrote", f)
		}
		if dtc == "" {
			continue
		}
		var sb strings.Builder
		if tr.WriteDTS(&sb) != nil {
			continue // a name dts cannot spell
		}
		viaDTS++
		if want := dtcFromDTS(t, dtc, sb.String(), tr.BootCPUID); !bytes.Equal(want, b) {
			t.Errorf("%s: dtc(WriteDTS(Parse(blob))) differs from the blob", f)
		}
	}
	if dtc != "" && viaDTS < 50 {
		t.Errorf("only %d corpus blobs went through dts; the lane is not measuring much", viaDTS)
	}
}

// A version 16 blob (dtc -V 16) holds the same tree as the version 17 one.
func TestCorpusV16SameTree(t *testing.T) {
	n := 0
	for _, f := range corpus(t) {
		if strings.HasSuffix(f, ".v16.dtb") {
			continue
		}
		v16 := strings.TrimSuffix(f, ".dtb") + ".v16.dtb"
		b16, err := os.ReadFile(v16)
		if err != nil {
			continue
		}
		a, err1 := Parse(mustRead(t, f))
		b, err2 := Parse(b16)
		if err1 != nil || err2 != nil {
			t.Errorf("%s: %v / %v", f, err1, err2)
			continue
		}
		if !treesEqual(a, b) {
			t.Errorf("%s: v16 and v17 blobs parse differently", f)
		}
		n++
	}
	if n < 50 {
		t.Errorf("only %d v16 blobs compared", n)
	}
}

// Parse against fdtget: for every node of every corpus blob, the same
// subnodes and the same property names, in the same order, with the same
// values (fdtget -t bx prints bytes).
func TestParseMatchesFdtget(t *testing.T) {
	fdtget := needTool(t, "fdtget")
	dir := t.TempDir()
	nodes := 0
	for _, f := range corpus(t) {
		if strings.HasSuffix(f, ".v16.dtb") {
			continue
		}
		tr, err := Parse(mustRead(t, f))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		_ = dir
		tr.Root.Walk(func(path string, n *Node) bool {
			nodes++
			lines := func(args ...string) []string {
				out, err := exec.Command(fdtget, append(args, f, path)...).Output()
				if err != nil {
					t.Errorf("%s %s: fdtget %v: %v", f, path, args, err)
					return nil
				}
				s := strings.TrimRight(string(out), "\n")
				if s == "" {
					return nil
				}
				return strings.Split(s, "\n")
			}
			var kids, props []string
			for _, c := range n.Children {
				kids = append(kids, c.Name)
			}
			for _, p := range n.Props {
				props = append(props, p.Name)
			}
			if got := lines("-l"); !equalStrings(got, kids) {
				t.Errorf("%s %s: subnodes %v, fdtget %v", f, path, kids, got)
			}
			if got := lines("-p"); !equalStrings(got, props) {
				t.Errorf("%s %s: properties %v, fdtget %v", f, path, props, got)
			}
			for _, p := range n.Props {
				if len(p.Value) == 0 {
					continue
				}
				out, err := exec.Command(fdtget, "-t", "bx", f, path, p.Name).Output()
				if err != nil {
					t.Errorf("%s %s %s: %v", f, path, p.Name, err)
					continue
				}
				var want []string
				for _, c := range p.Value {
					want = append(want, fmt.Sprintf("%x", c))
				}
				if got := strings.Fields(string(out)); !equalStrings(got, want) {
					t.Errorf("%s %s %s: value %v, fdtget %v", f, path, p.Name, want, got)
				}
			}
			return true
		})
	}
	if nodes < 200 {
		t.Errorf("only %d nodes compared", nodes)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// dtc itself must read what Marshal wrote and write the same bytes back.
func TestDtcReadsOurBlobs(t *testing.T) {
	dtc := needTool(t, "dtc")
	rng := rand.New(rand.NewSource(7))
	dir := t.TempDir()
	trees := []*Tree{sampleTree()}
	for i := 0; i < 20; i++ {
		trees = append(trees, randomTree(rng))
	}
	for i, tr := range trees {
		b, err := tr.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, fmt.Sprintf("t%d.dtb", i))
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(dtc, "-q", "-I", "dtb", "-O", "dtb", "-b", fmt.Sprint(tr.BootCPUID), p).Output()
		if err != nil || !bytes.Equal(out, b) {
			t.Errorf("tree %d: dtc -I dtb -O dtb does not give our blob back (%v)", i, err)
		}
	}
}
