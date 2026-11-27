// Command fdt reads devicetree blobs: a small fdtget/fdtdump in pure Go.
//
//	fdt check blob.dtb
//	fdt dump  blob.dtb
//	fdt ls    blob.dtb /cpus
//	fdt get   blob.dtb /cpus/cpu@0 reg
//	fdt get   blob.dtb /cpus/cpu@0            # property names
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/go-binfmt/fdt"
	"github.com/spf13/cobra"
)

// Version may be set with -ldflags "-X main.Version=..."; otherwise the module
// version Go stamped into the binary is used, and "dev" for an unstamped build.
var Version = "dev"

// readBuildInfo is replaceable in tests.
var readBuildInfo = debug.ReadBuildInfo

func version() string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := readBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return Version
}

// osExit is replaceable in tests.
var osExit = os.Exit

// maxInput bounds what the command reads: a devicetree blob is kilobytes,
// and FIT images with embedded data are tens of megabytes at most.
var maxInput int64 = 1 << 30

func main() {
	cmd := RootCmd()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "fdt:", err)
		osExit(1)
	}
}

func load(cmd *cobra.Command, name string) (*fdt.Tree, int, error) {
	var r io.Reader = cmd.InOrStdin()
	if name != "-" {
		f, err := os.Open(name)
		if err != nil {
			return nil, 0, err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, maxInput+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(b)) > maxInput {
		return nil, 0, fmt.Errorf("input larger than %d bytes", maxInput)
	}
	t, err := fdt.Parse(b)
	return t, len(b), err
}

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "fdt",
		Short:         "read devicetree blobs (DTB v17)",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	var showVersion bool
	root.Flags().BoolVar(&showVersion, "version", false, "print the version")
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if showVersion {
			fmt.Fprintf(cmd.OutOrStdout(), "fdt %s\n", version())
			return nil
		}
		return cmd.Help()
	}

	root.AddCommand(&cobra.Command{
		Use:   "check FILE",
		Short: "parse FILE (- for stdin) and refuse it if it is malformed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, size, err := load(cmd, args[0])
			if err != nil {
				return err
			}
			nodes, props := 0, 0
			t.Root.Walk(func(_ string, n *fdt.Node) bool {
				nodes++
				props += len(n.Props)
				return true
			})
			fmt.Fprintf(cmd.OutOrStdout(), "ok: %d bytes, %d nodes, %d properties, %d reservations\n", size, nodes, props, len(t.Reservations))
			return nil
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "dump FILE",
		Short: "print FILE as devicetree source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, _, err := load(cmd, args[0])
			if err != nil {
				return err
			}
			return t.WriteDTS(cmd.OutOrStdout())
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "ls FILE [PATH]",
		Short: "list the subnodes of PATH (default /), one per line",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, _, err := load(cmd, args[0])
			if err != nil {
				return err
			}
			path := "/"
			if len(args) == 2 {
				path = args[1]
			}
			n, err := t.Find(path)
			if err != nil {
				return err
			}
			for _, c := range n.Children {
				fmt.Fprintln(cmd.OutOrStdout(), c.Name)
			}
			return nil
		},
	})

	get := &cobra.Command{
		Use:   "get FILE PATH [PROP]",
		Short: "print a property of the node at PATH, or the names of all its properties",
		Long: "Without PROP, prints the property names of the node, one per line. With PROP,\n" +
			"prints its value in the form -t asks for: auto (a string list when the value\n" +
			"is one, else 32-bit cells in hex, else bytes), s, u32, u64, x (bytes in hex).",
		Args: cobra.RangeArgs(2, 3),
	}
	typ := get.Flags().StringP("type", "t", "auto", "value format: auto, s, u32, u64 or x")
	get.RunE = func(cmd *cobra.Command, args []string) error {
		t, _, err := load(cmd, args[0])
		if err != nil {
			return err
		}
		n, err := t.Find(args[1])
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if len(args) == 2 {
			for _, p := range n.Props {
				fmt.Fprintln(out, p.Name)
			}
			return nil
		}
		s, err := format(n, args[2], *typ)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, s)
		return nil
	}
	root.AddCommand(get)
	return root
}

func format(n *fdt.Node, prop, typ string) (string, error) {
	switch typ {
	case "s":
		l, err := n.StringList(prop)
		return strings.Join(l, " "), err
	case "u32":
		v, err := n.Uint32s(prop)
		return joinf("%d", v), err
	case "u64":
		v, err := n.Uint64(prop)
		return fmt.Sprint(v), err
	case "x":
		v, err := n.Bytes(prop)
		return joinf("%02x", v), err
	case "auto":
		v, err := n.Bytes(prop)
		if err != nil {
			return "", err
		}
		if l, err := n.StringList(prop); err == nil && len(v) > 0 && printable(v) {
			return strings.Join(l, " "), nil
		}
		if c, err := n.Uint32s(prop); err == nil {
			return joinf("0x%x", c), nil
		}
		return joinf("%02x", v), nil
	}
	return "", errors.New("unknown -t " + typ + " (want auto, s, u32, u64 or x)")
}

func joinf[T any](f string, v []T) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = fmt.Sprintf(f, x)
	}
	return strings.Join(parts, " ")
}

// printable reports whether v is NUL-terminated printable ASCII strings.
func printable(v []byte) bool {
	if v[len(v)-1] != 0 {
		return false
	}
	for i, c := range v {
		if c == 0 && (i == 0 || v[i-1] == 0) {
			return false // an empty string
		}
		if c != 0 && (c < 0x20 || c > 0x7e) {
			return false
		}
	}
	return true
}
