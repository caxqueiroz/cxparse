package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func makeArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFetch(t *testing.T) {
	name := archiveName("linux", "amd64")
	good := makeArchive(t, map[string]string{"libliteparse_ffi.so": "ffi", "./libpdfium.so": "pdfium", "._libpdfium.so": "xattrs"})
	goodSum := fmt.Sprintf("%x", sha256.Sum256(good))

	tests := []struct {
		name      string
		archive   []byte
		sums      string
		wantErr   string
		wantFiles []string
	}{
		{
			name:      "ok",
			archive:   good,
			sums:      fmt.Sprintf("%s  %s\nabc  other.tar.gz\n", goodSum, name),
			wantFiles: []string{"libliteparse_ffi.so", "libpdfium.so"},
		},
		{
			name:      "binary-mode checksum line",
			archive:   good,
			sums:      fmt.Sprintf("%s *%s\n", strings.ToUpper(goodSum), name),
			wantFiles: []string{"libliteparse_ffi.so", "libpdfium.so"},
		},
		{
			name:    "checksum mismatch",
			archive: good,
			sums:    fmt.Sprintf("%064d  %s\n", 0, name),
			wantErr: "checksum mismatch",
		},
		{
			name:    "platform not released",
			archive: good,
			sums:    "abc  liteparse-native_plan9_386.tar.gz\n",
			wantErr: "no prebuilt libraries",
		},
		{
			name:    "path traversal",
			archive: makeArchive(t, map[string]string{"../evil.so": "x"}),
			wantErr: "unexpected archive entry",
		},
		{
			name:    "nested path",
			archive: makeArchive(t, map[string]string{"sub/lib.so": "x"}),
			wantErr: "unexpected archive entry",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sums := tt.sums
			if sums == "" {
				sums = fmt.Sprintf("%x  %s\n", sha256.Sum256(tt.archive), name)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1.2.3/" + checksumsFile:
					_, _ = w.Write([]byte(sums))
				case "/v1.2.3/" + name:
					_, _ = w.Write(tt.archive)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			dir := filepath.Join(t.TempDir(), "lib")
			files, err := fetch(context.Background(), srv.Client(), options{
				baseURL: srv.URL, version: "v1.2.3", goos: "linux", goarch: "amd64", dir: dir,
			})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got error %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(files)
			if !slices.Equal(files, tt.wantFiles) {
				t.Fatalf("files = %v, want %v", files, tt.wantFiles)
			}
			got, err := os.ReadFile(filepath.Join(dir, "libpdfium.so"))
			if err != nil || string(got) != "pdfium" {
				t.Fatalf("libpdfium.so = %q, %v", got, err)
			}
		})
	}
}

func TestFetchMissingRelease(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	_, err := fetch(context.Background(), srv.Client(), options{
		baseURL: srv.URL, version: "v9.9.9", goos: "linux", goarch: "amd64", dir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("got %v, want 404 error", err)
	}
}
