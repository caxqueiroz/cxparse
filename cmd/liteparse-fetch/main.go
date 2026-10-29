// Command liteparse-fetch downloads the prebuilt native libraries
// (libliteparse_ffi and libpdfium) for a cxparse release, verifies their
// SHA-256 checksum and unpacks them into a directory the liteparse package
// searches at runtime.
//
//	go run github.com/caxqueiroz/cxparse/cmd/liteparse-fetch@v0.1.0 -dir lib
//
// When run as module@version the release version is taken from the build
// info, so the libraries always match the Go package version.
package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://github.com/caxqueiroz/cxparse/releases/download"
	checksumsFile  = "SHA256SUMS"
	// maxArchiveSize bounds downloads; the archives are tens of MB.
	maxArchiveSize = 512 << 20
)

type options struct {
	baseURL string
	version string
	goos    string
	goarch  string
	dir     string
}

func main() {
	opts := options{}
	flag.StringVar(&opts.version, "version", moduleVersion(), "release tag to download, e.g. v0.1.0")
	flag.StringVar(&opts.dir, "dir", "lib", "directory to unpack the libraries into")
	flag.StringVar(&opts.goos, "os", runtime.GOOS, "target operating system")
	flag.StringVar(&opts.goarch, "arch", runtime.GOARCH, "target architecture")
	flag.StringVar(&opts.baseURL, "base-url", defaultBaseURL, "release download base URL (for mirrors)")
	timeout := flag.Duration("timeout", 5*time.Minute, "download timeout")
	flag.Parse()

	if opts.version == "" {
		slog.Error("unknown release version: pass -version, or run as github.com/caxqueiroz/cxparse/cmd/liteparse-fetch@<version>")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	files, err := fetch(ctx, http.DefaultClient, opts)
	if err != nil {
		slog.Error("fetch native libraries", "version", opts.version, "error", err)
		os.Exit(1)
	}
	slog.Info("native libraries installed", "version", opts.version, "dir", opts.dir, "files", files)
}

// moduleVersion returns the cxparse version this binary was built from, or
// "" for local (devel) builds.
func moduleVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi.Main.Version == "" || bi.Main.Version == "(devel)" {
		return ""
	}
	return bi.Main.Version
}

// archiveName is the release asset holding the libraries for goos/goarch.
func archiveName(goos, goarch string) string {
	return fmt.Sprintf("liteparse-native_%s_%s.tar.gz", goos, goarch)
}

// fetch downloads, verifies and unpacks the release archive, returning the
// names of the files written.
func fetch(ctx context.Context, client *http.Client, opts options) ([]string, error) {
	base := strings.TrimSuffix(opts.baseURL, "/") + "/" + opts.version + "/"
	name := archiveName(opts.goos, opts.goarch)

	sums, err := download(ctx, client, base+checksumsFile)
	if err != nil {
		return nil, fmt.Errorf("download checksums: %w", err)
	}
	want, err := lookupChecksum(sums, name)
	if err != nil {
		return nil, err
	}

	archive, err := download(ctx, client, base+name)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", name, err)
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("%s: checksum mismatch: got %x, want %s", name, got, want)
	}

	files, err := extract(archive, opts.dir)
	if err != nil {
		return nil, fmt.Errorf("unpack %s: %w", name, err)
	}
	return files, nil
}

func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if len(body) > maxArchiveSize {
		return nil, fmt.Errorf("get %s: response larger than %d bytes", url, maxArchiveSize)
	}
	return body, nil
}

// lookupChecksum finds name in a sha256sum-format file.
func lookupChecksum(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read checksums: %w", err)
	}
	return "", fmt.Errorf("no prebuilt libraries for this platform: %s not in %s", name, checksumsFile)
}

// extract writes the regular files of a gzipped tar into dir. Archives are
// flat; entries with directory components are rejected.
func extract(archive []byte, dir string) ([]string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}

	var files []string
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.TrimPrefix(hdr.Name, "./")
		if name == "" || name != filepath.Base(name) || name == ".." {
			return nil, fmt.Errorf("unexpected archive entry %q", hdr.Name)
		}
		if strings.HasPrefix(name, "._") {
			continue // macOS AppleDouble metadata
		}
		if err := writeFile(filepath.Join(dir, name), tr); err != nil {
			return nil, err
		}
		files = append(files, name)
	}
	if len(files) == 0 {
		return nil, errors.New("archive contains no files")
	}
	return files, nil
}

// writeFile writes r to path via a temporary file, so a running program never
// sees a half-written library.
func writeFile(path string, r io.Reader) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".liteparse-fetch-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := io.Copy(tmp, io.LimitReader(r, maxArchiveSize)); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("install %s: %w", path, err)
	}
	return nil
}
