# Test data

`dtc/*.dtb` and `fuzz/FuzzParse/dtc-*` are the `tests/*.dts` sources of the
[dtc](https://github.com/dgibson/dtc) test suite (v1.7.2, GPL-2.0-or-later with
the dtc project's own terms) compiled by `dtc -I dts -O dtb`, as version 17
blobs (`NAME.dtb`) and version 16 blobs (`NAME.v16.dtb`). They are test inputs
only: no dtc source is compiled into this module or its binary. Regenerate
them with

    go run ./internal/mkcorpus -dtc-src <dtc checkout>

The other files under `fuzz/FuzzParse` are findings and hand-made hostile
blobs of this repository, under its own licence.
