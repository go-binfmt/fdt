# go-binfmt/fdt

[![CI](https://github.com/go-binfmt/fdt/actions/workflows/ci.yml/badge.svg)](https://github.com/go-binfmt/fdt/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-binfmt/fdt.svg)](https://pkg.go.dev/github.com/go-binfmt/fdt)
[![License: BSD-3-Clause](https://img.shields.io/badge/license-BSD--3--Clause-0A6E96)](LICENSE)

A pure-Go reader and writer for **devicetree blobs** (DTB, format version 17):
the format of `dtc -O dtb` and of the FIT images U-Boot loads. No cgo, no
libfdt, no `dtc` on the build host.

```
go install github.com/go-binfmt/fdt/cmd/fdt@latest

fdt check board.dtb                    # parse, refuse if malformed
fdt dump  board.dtb                    # devicetree source on stdout
fdt ls    board.dtb /cpus              # subnodes
fdt get   board.dtb /cpus/cpu@0 reg    # a property (-t s|u32|u64|x picks the format)
```

```go
import "github.com/go-binfmt/fdt"

t, err := fdt.Parse(blob)            // every bound checked; errors wrap fdt.ErrFormat
cpu, _ := t.Find("/cpus/cpu@0")
reg, _ := cpu.Uint32s("reg")
cpu.SetString("status", "okay")
out, _ := t.Marshal()                // byte-identical to dtc for the same tree
```

The library is a tree: `Tree{BootCPUID, Reservations, Root}`, `Node{Name, Props,
Children}`, `Prop{Name, Value}`. `Find`/`Lookup`/`Child` resolve paths (a
component without `@` matches `name@unit-address`, as libfdt's
`fdt_subnode_offset` does), the typed getters (`Uint32`, `Uint64`, `Cell`,
`Uint32s`, `String`, `StringList`, `Bytes`) refuse a value of the wrong shape
(`ErrValue`), and the setters (`Set*`, `AddChild`, `Ensure`, `DeleteProp`,
`DeleteChild`) build or edit a tree for later FIT use.

## Reading

`Parse` takes untrusted bytes. Every header field, the reservation map, the
structure block and the strings block are checked against the input length
without overflowing, so the result is the same on 32-bit hosts; nesting deeper
than `MaxDepth` (128), duplicate node or property names, a property name
outside the strings block, misaligned offsets, overlapping blocks, a truncated
or unknown token, a second root and a property after a subnode are all refused. Version 17 and 16 blobs (`last_comp_version` 16) are read; anything
older is `ErrVersion`. Nothing is truncated or guessed.

## Writing

`Marshal` writes the layout `dtc` writes, including its string table (a name
that is the tail of one already present reuses it). The result is
**byte-identical** to `dtc -I dts -O dtb` for the same tree. CI proves it
against the real `dtc` on random trees and on every blob of dtc's own test
suite, and a tree that would not parse back is refused.

## Assurance

* **Differential**: writes against `dtc`, reads against `fdtget`, in CI with the
  tools required (`FDT_REQUIRE_DTC`), so a runner that loses them fails instead
  of skipping.
* **Fuzzed**: `FuzzParse`, with the dtc test suite compiled to blobs as the
  committed corpus (`testdata/fuzz/FuzzParse`); CI replays it and fuzzes 60 s per
  run. A local 60 s run did 37.7 million executions with no finding.
* **100 % statement coverage** of the library and the command, gated in CI, and a
  mutation sweep (`mutsweep`) of every refusal.
* **govulncheck** in CI (`vuln.yml`, daily as well).
* **Portable**: tests run on amd64, 32-bit (`386`), big-endian `s390x` under
  qemu, and macOS; the build matrix covers Linux (amd64, arm64, riscv64,
  ppc64le, s390x, loong64, 386), macOS, Windows and the BSDs.

## Performance

Single core, interleaved rounds, against libfdt (C, `-O2`): `fdt.Parse` against
`fdt_check_full` plus a visit of every node and property, `Marshal` against the
`fdt_create` ... `fdt_finish` replay of the same tree. `go run ./internal/bench
-dir <dir>` reproduces it; CI runs it pinned (`taskset -c 0`) and prints the
table in the job summary.

BENCH_TABLE

## Licence

BSD-3-Clause. `testdata/dtc` and the matching fuzz seeds are compiled from the
dtc test suite (GPL-2.0-or-later) and are test data only; see
`testdata/NOTICE.md`.
