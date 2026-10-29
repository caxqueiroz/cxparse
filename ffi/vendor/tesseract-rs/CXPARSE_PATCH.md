# Vendored tesseract-rs 0.2.0 (patched)

Copy of [tesseract-rs](https://crates.io/crates/tesseract-rs) 0.2.0 (MIT,
see LICENSE), used through `[patch.crates-io]` in `ffi/Cargo.toml`.

Changes from upstream:

- The `zip` build dependency is bumped from 2.4.2 to 9.0.5. Versions up to
  9.0.4 are affected by a critical "zip cloaking" advisory, and every
  published tesseract-rs release depends on an affected version.
- `build.rs` extracts archive entries with `enclosed_name()` instead of
  `mangled_name()`, so absolute and `..` paths are rejected.
- Test and bench targets are removed from the manifest; their sources aren't
  vendored.

Delete this directory and the patch entry once liteparse depends on a
tesseract-rs release with a fixed zip.
