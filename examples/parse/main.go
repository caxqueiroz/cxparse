// Command parse prints a document as markdown (or json/text) using liteparse.
//
//	go run ./examples/parse -format markdown liteparse/testdata/sample.pdf
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/caxqueiroz/cxparse/liteparse"
)

func main() {
	format := flag.String("format", "markdown", "output format: markdown, text or json")
	pages := flag.String("pages", "", `page selection, e.g. "1-3,7"`)
	ocrURL := flag.String("ocr-url", "", "HTTP OCR server URL (enables OCR)")
	timeout := flag.Duration("timeout", time.Minute, "parse timeout")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: parse [flags] <file>")
		os.Exit(2)
	}

	opts := []liteparse.Option{
		liteparse.WithOutputFormat(liteparse.OutputFormat(*format)),
		liteparse.WithTargetPages(*pages),
		liteparse.WithOCR(false),
	}
	if *ocrURL != "" {
		opts = append(opts, liteparse.WithOCRServer(*ocrURL, nil))
	}

	p, err := liteparse.New(opts...)
	if err != nil {
		slog.Error("create parser", "error", err)
		os.Exit(1)
	}
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	res, err := p.ParseFile(ctx, flag.Arg(0))
	if err != nil {
		slog.Error("parse", "error", err)
		os.Exit(1)
	}

	if *format == string(liteparse.FormatJSON) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			slog.Error("encode", "error", err)
			os.Exit(1)
		}
		return
	}
	fmt.Println(res.Text)
}
