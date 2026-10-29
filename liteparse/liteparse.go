// Package liteparse is a Go binding for LiteParse
// (https://github.com/run-llama/liteparse), a fast local PDF/document parser
// with spatial text extraction, bounding boxes, markdown output, screenshots
// and optional OCR.
//
// The binding needs no cgo: it loads the liteparse FFI shared library and
// PDFium at runtime via purego. See Load for how they are located.
//
//	p, err := liteparse.New(liteparse.WithOutputFormat(liteparse.FormatMarkdown))
//	if err != nil { ... }
//	defer p.Close()
//	res, err := p.ParseFile(ctx, "doc.pdf")
//	fmt.Println(res.Text)
package liteparse

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
)

// Version returns the version of the liteparse core the native library wraps.
func Version() (string, error) {
	if err := ensureLoaded(); err != nil {
		return "", err
	}
	return goString(lpVersion()), nil
}

// Parser parses documents with a fixed configuration. It is safe for
// concurrent use. PDFium itself is single-threaded, so the PDFium portion of
// concurrent calls is serialized natively; OCR and layout run in parallel.
type Parser struct {
	mu      sync.RWMutex
	handle  uintptr
	cfg     Config
	cleanup runtime.Cleanup
}

// New creates a Parser from DefaultConfig with opts applied.
func New(opts ...Option) (*Parser, error) {
	cfg, err := DefaultConfig()
	if err != nil {
		return nil, err
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("liteparse: encode config: %w", err)
	}

	var handle uintptr
	var out *byte
	code := lpParserNew(cString(string(body)), &handle, &out)
	if err := takeError(code, out); err != nil {
		return nil, err
	}

	p := &Parser{handle: handle, cfg: cfg}
	p.cleanup = runtime.AddCleanup(p, func(h uintptr) { lpParserFree(h) }, handle)
	return p, nil
}

// Config returns the configuration the parser was created with.
func (p *Parser) Config() Config { return p.cfg }

// Close releases the native parser. It waits for in-flight calls and is safe
// to call more than once.
func (p *Parser) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.handle != 0 {
		p.cleanup.Stop()
		lpParserFree(p.handle)
		p.handle = 0
	}
	return nil
}

// input is either a file path or in-memory bytes.
type input struct {
	path string
	data []byte
}

func (in input) args() (path *byte, data *byte, n uintptr) {
	if in.data == nil {
		return cString(in.path), nil, 0
	}
	if len(in.data) == 0 {
		return nil, nil, 0
	}
	return nil, &in.data[0], uintptr(len(in.data))
}

// ParseFile parses the document at path. Non-PDF inputs (DOCX, XLSX, PPTX,
// images) are converted first, which requires LibreOffice for office formats.
func (p *Parser) ParseFile(ctx context.Context, path string) (*Result, error) {
	return p.parse(ctx, input{path: path})
}

// ParseBytes parses an in-memory document.
func (p *Parser) ParseBytes(ctx context.Context, data []byte) (*Result, error) {
	return p.parse(ctx, input{data: nonNil(data)})
}

func (p *Parser) parse(ctx context.Context, in input) (*Result, error) {
	var res Result
	err := p.call(ctx, &res, func(handle, cancel uintptr, out **byte) int32 {
		path, data, n := in.args()
		code := lpParse(handle, path, data, n, cancel, out)
		runtime.KeepAlive(in.data)
		return code
	})
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// ScreenshotFile renders pages of the document at path as PNG. pages is a
// selection such as "1-3,7"; empty renders every page. Config.DPI sets the
// resolution.
func (p *Parser) ScreenshotFile(ctx context.Context, path, pages string) ([]Screenshot, error) {
	return p.screenshot(ctx, input{path: path}, pages)
}

// ScreenshotBytes renders pages of an in-memory document as PNG.
func (p *Parser) ScreenshotBytes(ctx context.Context, data []byte, pages string) ([]Screenshot, error) {
	return p.screenshot(ctx, input{data: nonNil(data)}, pages)
}

func (p *Parser) screenshot(ctx context.Context, in input, pages string) ([]Screenshot, error) {
	var shots []Screenshot
	err := p.call(ctx, &shots, func(handle, cancel uintptr, out **byte) int32 {
		path, data, n := in.args()
		code := lpScreenshot(handle, path, data, n, cString(pages), cancel, out)
		runtime.KeepAlive(in.data)
		return code
	})
	return shots, err
}

// IsComplexFile cheaply computes per-page complexity signals for the document
// at path, without a full parse. Use it to route documents to OCR or a
// heavier pipeline.
func (p *Parser) IsComplexFile(ctx context.Context, path string) ([]PageComplexity, error) {
	return p.isComplex(ctx, input{path: path})
}

// IsComplexBytes computes per-page complexity signals for an in-memory
// document.
func (p *Parser) IsComplexBytes(ctx context.Context, data []byte) ([]PageComplexity, error) {
	return p.isComplex(ctx, input{data: nonNil(data)})
}

func (p *Parser) isComplex(ctx context.Context, in input) ([]PageComplexity, error) {
	var stats []PageComplexity
	err := p.call(ctx, &stats, func(handle, cancel uintptr, out **byte) int32 {
		path, data, n := in.args()
		code := lpIsComplex(handle, path, data, n, cancel, out)
		runtime.KeepAlive(in.data)
		return code
	})
	return stats, err
}

// call runs a native entry point with ctx wired to a cancel token, then
// decodes the JSON payload into dst.
func (p *Parser) call(ctx context.Context, dst any, fn func(handle, cancel uintptr, out **byte) int32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.handle == 0 {
		return ErrClosed
	}

	var cancel uintptr
	if ctx.Done() != nil {
		cancel = lpCancelNew()
		done := make(chan struct{})
		stop := context.AfterFunc(ctx, func() {
			defer close(done)
			lpCancelTrigger(cancel)
		})
		defer func() {
			if !stop() {
				<-done
			}
			lpCancelFree(cancel)
		}()
	}

	var out *byte
	payload, err := takeResult(fn(p.handle, cancel, &out), out)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("%w: %w", ctxErr, err)
		}
		return err
	}
	if err := json.Unmarshal([]byte(payload), dst); err != nil {
		return fmt.Errorf("liteparse: decode result: %w", err)
	}
	return nil
}

// nonNil keeps "bytes input" distinct from "path input" for empty slices.
func nonNil(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}
