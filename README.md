# cxparse — Go bindings for LiteParse

Go bindings for [LiteParse](https://github.com/run-llama/liteparse): fast,
local PDF/document parsing with spatial text, bounding boxes, markdown
output, page screenshots, complexity detection and optional OCR.

**No cgo.** The Go package loads the native libraries at runtime with
[purego](https://github.com/ebitengine/purego). Your program builds with
`CGO_ENABLED=0` and cross-compiles like any other Go program.

```
Go (liteparse pkg) ──purego──▶ libliteparse_ffi  (Rust, C ABI over liteparse 2.17.0)
                                      └─dlopen──▶ libpdfium
```

## Layout

| Path | What |
|------|------|
| `liteparse/` | The Go package: `import "github.com/caxqueiroz/cxparse/liteparse"` |
| `ffi/` | Rust crate exposing a small C ABI (`lp_*` functions) over the `liteparse` crate |
| `cmd/liteparse-fetch/` | Downloads prebuilt native libraries for a release |
| `examples/parse/` | CLI example |
| `lib/` | Native libraries (`make native` or `liteparse-fetch`; git-ignored) |

## Get the native libraries

### Prebuilt (no Rust needed)

Each `v*` release has prebuilt libraries for darwin/arm64, linux/amd64 and
linux/arm64. Fetch the set matching the
version in your `go.mod` into `./lib`:

```bash
go run github.com/caxqueiroz/cxparse/cmd/liteparse-fetch@v0.1.0 -dir lib
```

The command checks the archive against the release's `SHA256SUMS` before
unpacking it. Use `-os` and `-arch` to fetch for another platform, for
example when building a Docker image. The Go package itself never touches
the network.

### From source

Needs a Rust toolchain (edition 2024, so Rust 1.85 or newer).

```bash
make native        # builds ffi/ and copies libliteparse_ffi + libpdfium into ./lib
make test          # Rust + Go tests
make dist          # packages ./lib as dist/liteparse-native_<os>_<arch>.tar.gz
```

PDFium is downloaded by the `liteparse-pdfium-sys` build script from
`run-llama/pdfium-binaries` (tag `chromium/8058`). `make native` copies it
next to the FFI library.

Built-in Tesseract OCR is off by default. It compiles leptonica and
tesseract from source. Enable it with `make native CARGO_FEATURES=tesseract`.
Without it, use an HTTP OCR server (`WithOCRServer`) that implements
LiteParse's [OCR API spec](https://github.com/run-llama/liteparse/blob/main/OCR_API_SPEC.md).

## Usage

```go
p, err := liteparse.New(
    liteparse.WithOutputFormat(liteparse.FormatMarkdown),
    liteparse.WithOCR(false),
)
if err != nil {
    return err
}
defer p.Close()

res, err := p.ParseFile(ctx, "report.pdf") // or p.ParseBytes(ctx, data)
if err != nil {
    return err
}
fmt.Println(res.Text) // whole document as markdown

for _, page := range res.Pages {
    for _, it := range page.TextItems {
        fmt.Printf("%q at (%.0f,%.0f) %.0fx%.0f\n", it.Text, it.X, it.Y, it.Width, it.Height)
    }
}
```

Any `func(*liteparse.Config)` is an option, so every LiteParse setting is
reachable:

```go
p, err := liteparse.New(func(c *liteparse.Config) {
    c.ExtractBlocks = true       // headings/paragraphs/tables with bboxes
    c.ExtractFormFields = true
    c.EmitWordBoxes = true
    c.TargetPages = "1-5"
})
```

Other operations:

```go
shots, err := p.ScreenshotFile(ctx, "doc.pdf", "1-3")  // PNG bytes in shots[i].PNG
stats, err := p.IsComplexFile(ctx, "doc.pdf")          // cheap: does it need OCR?
if stats[0].NeedsOCR { /* route to OCR */ }
```

Run the example:

```bash
go run ./examples/parse -format markdown liteparse/testdata/sample.pdf
```

### Behaviour notes

- **Concurrency:** a `Parser` is safe for concurrent use. PDFium is
  single-threaded, so the PDFium part of concurrent calls is serialized
  natively. OCR and layout still run in parallel.
- **Cancellation:** a canceled `ctx` stops the call at its next await point,
  such as format conversion or OCR. A synchronous PDFium section runs to
  completion first.
- **Errors:** match them with `errors.Is`: `ErrPDF`, `ErrIO`, `ErrOCR`,
  `ErrConfig`, `ErrConversion`, `ErrInvalidArgument`, `ErrPDFiumUnavailable`,
  `ErrLibraryNotFound`, `ErrClosed`.
- **Logging:** `New` sets `Quiet: true` so the native side doesn't write
  progress to stderr. Use `WithQuiet(false)` to see it.
- **Non-PDF inputs:** DOCX, XLSX and PPTX need LibreOffice installed. Images
  are converted natively.

## Finding the native libraries at runtime

The FFI library is searched in this order:

1. `liteparse.Load(liteparse.LoadOptions{LibPath: ..., PDFiumPath: ...})`, if called before first use
2. `$LITEPARSE_LIB_PATH` (a file, or a directory containing it)
3. the executable's directory, then `<exe dir>/lib`
4. `./lib`
5. the system loader path (`DYLD_LIBRARY_PATH`, `LD_LIBRARY_PATH`, `PATH`)

PDFium is loaded from the same directory as the FFI library when present.
Otherwise the native side checks `$PDFIUM_LIB_PATH` (a directory) and then
the system loader path.

To deploy, ship both libraries next to your binary, or in a `lib/` folder
beside it.

## Platforms

Tested locally on macOS arm64. `.github/workflows/native.yml` builds and
tests (Rust tests, then Go tests with `CGO_ENABLED=0`) on darwin/arm64,
linux/amd64 and linux/arm64. Intel macOS and Windows go through the same
code paths (the Go package cross-compiles for both), but no prebuilt
libraries are published for them; build those with `make native`. When you push a
`v*` tag, it also publishes the archives and `SHA256SUMS` to the GitHub
release.

## Releasing

```bash
git tag v0.1.0 && git push origin v0.1.0
```

The workflow attaches the archives to the release. After that,
`liteparse-fetch@v0.1.0` works.

## Troubleshooting

- **`mis-aligned LINKEDIT string pool` on macOS.** rustc runs the first
  `strip` on your PATH, and an old cctools `strip` (from conda or MacPorts)
  corrupts the dylib. `ffi/Cargo.toml` sets `strip = "none"` to avoid this.
  If you need a smaller library, strip it yourself with `/usr/bin/strip -x`.

## License

Apache-2.0, the same as LiteParse. See [LICENSE](LICENSE).
