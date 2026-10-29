package liteparse

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const samplePDF = "testdata/sample.pdf"

// TestMain points the loader at ../lib (where `make native` stages the
// libraries) unless the caller already configured a location.
func TestMain(m *testing.M) {
	if os.Getenv(EnvLibPath) == "" {
		if abs, err := filepath.Abs("../lib"); err == nil {
			_ = os.Setenv(EnvLibPath, abs)
		}
	}
	os.Exit(m.Run())
}

func newParser(t *testing.T, opts ...Option) *Parser {
	t.Helper()
	p, err := New(append([]Option{WithOCR(false)}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestVersion(t *testing.T) {
	v, err := Version()
	if err != nil {
		t.Fatal(err)
	}
	if v == "" {
		t.Fatal("empty version")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OCRLanguage != "eng" || cfg.MaxPages != 1000 || cfg.OutputFormat != FormatJSON || !cfg.Quiet {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestParseFormats(t *testing.T) {
	tests := []struct {
		name   string
		format OutputFormat
		check  func(t *testing.T, res *Result)
	}{
		{
			name:   "json",
			format: FormatJSON,
			check: func(t *testing.T, res *Result) {
				if len(res.Pages[0].TextItems) == 0 {
					t.Fatal("no text items")
				}
				it := res.Pages[0].TextItems[0]
				if it.Width <= 0 || it.Height <= 0 {
					t.Fatalf("bad bbox: %+v", it)
				}
			},
		},
		{
			name:   "text",
			format: FormatText,
			check: func(t *testing.T, res *Result) {
				if strings.TrimSpace(res.Pages[0].Text) == "" {
					t.Fatal("empty page text")
				}
			},
		},
		{
			name:   "markdown",
			format: FormatMarkdown,
			check: func(t *testing.T, res *Result) {
				if res.Pages[0].Markdown == "" {
					t.Fatal("empty page markdown")
				}
				if !strings.Contains(res.Text, res.Pages[0].Markdown) {
					t.Fatal("document text is not the joined markdown")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newParser(t, WithOutputFormat(tt.format))
			res, err := p.ParseFile(context.Background(), samplePDF)
			if err != nil {
				t.Fatal(err)
			}
			if res.TotalPages == 0 || len(res.Pages) == 0 {
				t.Fatalf("no pages: %+v", res)
			}
			if strings.TrimSpace(res.Text) == "" {
				t.Fatal("empty document text")
			}
			tt.check(t, res)
		})
	}
}

func TestParseBytesMatchesFile(t *testing.T) {
	data, err := os.ReadFile(samplePDF)
	if err != nil {
		t.Fatal(err)
	}
	p := newParser(t)
	fromFile, err := p.ParseFile(context.Background(), samplePDF)
	if err != nil {
		t.Fatal(err)
	}
	fromBytes, err := p.ParseBytes(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if fromFile.Text != fromBytes.Text {
		t.Fatal("bytes and file parses differ")
	}
}

func TestScreenshot(t *testing.T) {
	p := newParser(t, WithDPI(72))
	shots, err := p.ScreenshotFile(context.Background(), samplePDF, "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(shots) != 1 || shots[0].PageNumber != 1 {
		t.Fatalf("unexpected screenshots: %d", len(shots))
	}
	img, err := png.Decode(bytes.NewReader(shots[0].PNG))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	if b := img.Bounds(); b.Dx() != shots[0].Width || b.Dy() != shots[0].Height {
		t.Fatalf("png is %v, want %dx%d", b, shots[0].Width, shots[0].Height)
	}
}

func TestIsComplex(t *testing.T) {
	p := newParser(t)
	stats, err := p.IsComplexFile(context.Background(), samplePDF)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) == 0 || stats[0].PageNumber != 1 || stats[0].Layout == nil {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestExtractionOptions(t *testing.T) {
	p := newParser(t, func(c *Config) {
		c.ExtractFormFields = true
		c.ExtractBlocks = true
		c.ExtractTextMetadata = true
		c.EmitWordBoxes = true
		c.IncludeComplexity = true
	})
	res, err := p.ParseFile(context.Background(), "testdata/filled_acroform.pdf")
	if err != nil {
		t.Fatal(err)
	}
	page := res.Pages[0]
	if len(page.FormFields) == 0 {
		t.Error("no form fields")
	}
	if page.Blocks == nil {
		t.Error("blocks not extracted")
	}
	if page.Complexity == nil {
		t.Error("complexity not attached")
	}
	if res.FormType == nil {
		t.Error("form type missing")
	}
	if len(page.TextItems) == 0 {
		t.Fatal("no text items")
	}
	if page.TextItems[0].FontIsBuggy == nil {
		t.Error("text metadata missing")
	}
	words := 0
	for _, it := range page.TextItems {
		words += len(it.Words)
	}
	if words == 0 {
		t.Error("word boxes missing")
	}
}

func TestErrors(t *testing.T) {
	p := newParser(t)
	ctx := context.Background()

	if _, err := p.ParseFile(ctx, "testdata/does-not-exist.pdf"); err == nil {
		t.Error("expected error for missing file")
	}
	if _, err := p.ParseBytes(ctx, []byte("not a pdf")); err == nil {
		t.Error("expected error for garbage bytes")
	}
	if _, err := p.ParseBytes(ctx, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("empty input: got %v, want ErrInvalidArgument", err)
	}
	if _, err := p.ScreenshotFile(ctx, samplePDF, "3-1"); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("bad page range: got %v, want ErrInvalidArgument", err)
	}
	if _, err := New(func(c *Config) { c.OutputFormat = "yaml" }); !errors.Is(err, ErrConfig) {
		t.Errorf("bad format: got %v, want ErrConfig", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.ParseFile(canceled, samplePDF); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled ctx: got %v", err)
	}

	_ = p.Close()
	if _, err := p.ParseFile(ctx, samplePDF); !errors.Is(err, ErrClosed) {
		t.Errorf("closed parser: got %v, want ErrClosed", err)
	}
}

func TestConcurrentParses(t *testing.T) {
	p := newParser(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			if _, err := p.ParseFile(ctx, samplePDF); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
