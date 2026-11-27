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

Measured by the CI `bench` job (linux/amd64 runner, `taskset -c 0`, 2026-11-27, median ns/op, ratio = libfdt / fdt, so above 1 means fdt is faster):

| blob | operation | libfdt | fdt | libfdt / fdt |
|---|---|---:|---:|---:|
| board 40 nodes (7.3 KiB) | parse | 34.7 us | 20.7 us | 1.68x |
| board 40 nodes (7.3 KiB) | ParseShared | 34.7 us | 18.8 us | 1.85x |
| board 40 nodes (7.3 KiB) | write | 45.7 us | 17.8 us | 2.57x |
| board 40 nodes (7.3 KiB) | AppendMarshal, reused buffer | 45.7 us | 16.9 us | 2.71x |
| board 2000 nodes (349.9 KiB) | parse | 1.70 ms | 1.04 ms | 1.64x |
| board 2000 nodes (349.9 KiB) | ParseShared | 1.70 ms | 994.4 us | 1.71x |
| board 2000 nodes (349.9 KiB) | write | 2.22 ms | 669.6 us | 3.32x |
| board 2000 nodes (349.9 KiB) | AppendMarshal, reused buffer | 2.22 ms | 648.7 us | 3.43x |
| board 100000 nodes (17.1 MiB) | parse | 85.21 ms | 58.86 ms | 1.45x |
| board 100000 nodes (17.1 MiB) | ParseShared | 85.21 ms | 58.37 ms | 1.46x |
| board 100000 nodes (17.1 MiB) | write | 105.06 ms | 40.49 ms | 2.59x |
| board 100000 nodes (17.1 MiB) | AppendMarshal, reused buffer | 105.06 ms | 39.58 ms | 2.65x |
| FIT-like, 3 x 8 MiB data (24.0 MiB) | parse (copies the values) | 12.1 us | 4.25 ms | 0.00x |
| FIT-like, 3 x 8 MiB data (24.0 MiB) | ParseShared | 12.1 us | 7.0 us | 1.73x |
| FIT-like, 3 x 8 MiB data (24.0 MiB) | write (allocates 24 MiB) | 3.50 ms | 4.91 ms | 0.71x |
| FIT-like, 3 x 8 MiB data (24.0 MiB) | AppendMarshal, reused buffer | 3.50 ms | 566.9 us | 6.17x |

The two FIT-like rows below 1 are the owning calls: `Parse` copies 24 MiB of
values and `Marshal` allocates a 24 MiB buffer, which libfdt does not do
(it checks in place and writes into a caller buffer). For large payloads use
`ParseShared` and `AppendMarshal`, which do the same work as libfdt and are
faster.

## Licence

BSD-3-Clause. `testdata/dtc` and the matching fuzz seeds are compiled from the
dtc test suite (GPL-2.0-or-later) and are test data only; see
`testdata/NOTICE.md`.
