package liteparse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// testPNG returns an image-only page, so every parse sends it to OCR.
func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 400, 200))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	for x := 40; x < 360; x++ {
		for y := 80; y < 120; y++ {
			img.SetGray(x, y, color.Gray{})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ocrRequest is what the fake OCR server saw.
type ocrRequest struct {
	header   string
	language string
	isPNG    bool
}

func TestOCRServer(t *testing.T) {
	var (
		mu   sync.Mutex
		reqs []ocrRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer func() { _ = f.Close() }()
		_, err = png.DecodeConfig(f)
		mu.Lock()
		reqs = append(reqs, ocrRequest{
			header:   r.Header.Get("X-Test"),
			language: r.FormValue("language"),
			isPNG:    err == nil,
		})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"text": "HELLO", "bbox": []float64{40, 80, 200, 120}, "confidence": 0.99},
				{"text": "OCR", "bbox": []float64{220, 80, 360, 120}, "confidence": 0.98},
			},
		})
	}))
	defer srv.Close()

	p := newParser(t,
		WithOCRServer(srv.URL, Headers{"X-Test": "yes"}),
		WithOCRLanguage("deu"),
		WithOutputFormat(FormatText),
	)
	res, err := p.ParseBytes(context.Background(), testPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "HELLO") || !strings.Contains(res.Text, "OCR") {
		t.Fatalf("OCR text missing from %q", res.Text)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(reqs) == 0 {
		t.Fatal("OCR server was not called")
	}
	for _, r := range reqs {
		if r.header != "yes" || !r.isPNG || r.language == "" {
			t.Errorf("unexpected OCR request: %+v", r)
		}
	}
}

func TestCancelDuringOCR(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		select { // hang until the client gives up
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	p := newParser(t, WithOCRServer(srv.URL, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := p.ParseBytes(ctx, testPNG(t))
		done <- err
	}()

	select {
	case <-started:
	case err := <-done:
		t.Fatalf("parse finished before reaching OCR: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("OCR server never called")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrCanceled) {
			t.Fatalf("got %v, want context.Canceled and ErrCanceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("parse did not stop after cancel")
	}

	// The parser stays usable after a canceled call.
	if _, err := p.ParseFile(context.Background(), samplePDF); err != nil {
		t.Fatalf("parse after cancel: %v", err)
	}
}
