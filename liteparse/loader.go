package liteparse

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Environment variables consulted when locating the native libraries.
const (
	// EnvLibPath names the liteparse FFI shared library file, or a directory
	// containing it.
	EnvLibPath = "LITEPARSE_LIB_PATH"
	// EnvPDFiumPath names the directory containing the PDFium shared library.
	// It is read by the native side; set it before the first parse.
	EnvPDFiumPath = "PDFIUM_LIB_PATH"
)

// LoadOptions overrides native library discovery. Zero values fall back to
// the default search (see Load).
type LoadOptions struct {
	// LibPath is the liteparse FFI shared library file (libliteparse_ffi.dylib,
	// libliteparse_ffi.so or liteparse_ffi.dll).
	LibPath string
	// PDFiumPath is the PDFium shared library file (libpdfium.dylib,
	// libpdfium.so or pdfium.dll).
	PDFiumPath string
}

// Native function table, populated once by load.
var (
	lpStringFree    func(s *byte)
	lpVersion       func() *byte
	lpDefaultConfig func(out **byte) int32
	lpLoadPDFium    func(path *byte, out **byte) int32
	lpParserNew     func(configJSON *byte, parser *uintptr, out **byte) int32
	lpParserFree    func(parser uintptr)
	lpParse         func(parser uintptr, path *byte, data *byte, n uintptr, cancel uintptr, out **byte) int32
	lpScreenshot    func(parser uintptr, path *byte, data *byte, n uintptr, pages *byte, cancel uintptr, out **byte) int32
	lpIsComplex     func(parser uintptr, path *byte, data *byte, n uintptr, cancel uintptr, out **byte) int32
	lpCancelNew     func() uintptr
	lpCancelTrigger func(token uintptr)
	lpCancelFree    func(token uintptr)
)

var (
	loadOnce sync.Once
	loadErr  error
)

// Load locates and loads the native libraries. Calling it is optional: every
// API loads lazily with default options on first use. Only the first call
// has any effect; later calls return the first call's result.
//
// The liteparse FFI library is searched in order: opts.LibPath,
// $LITEPARSE_LIB_PATH, the executable's directory, <exe dir>/lib, ./lib,
// then the system loader's search path.
//
// PDFium is loaded from opts.PDFiumPath when set. Otherwise, if a PDFium
// library sits next to the liteparse FFI library it is used; failing that,
// the native side searches $PDFIUM_LIB_PATH and the system loader's path.
func Load(opts LoadOptions) error {
	loadOnce.Do(func() { loadErr = load(opts) })
	return loadErr
}

func ensureLoaded() error { return Load(LoadOptions{}) }

func load(opts LoadOptions) error {
	lib, libPath, err := openFFI(opts.LibPath)
	if err != nil {
		return err
	}

	purego.RegisterLibFunc(&lpStringFree, lib, "lp_string_free")
	purego.RegisterLibFunc(&lpVersion, lib, "lp_version")
	purego.RegisterLibFunc(&lpDefaultConfig, lib, "lp_default_config")
	purego.RegisterLibFunc(&lpLoadPDFium, lib, "lp_load_pdfium")
	purego.RegisterLibFunc(&lpParserNew, lib, "lp_parser_new")
	purego.RegisterLibFunc(&lpParserFree, lib, "lp_parser_free")
	purego.RegisterLibFunc(&lpParse, lib, "lp_parse")
	purego.RegisterLibFunc(&lpScreenshot, lib, "lp_screenshot")
	purego.RegisterLibFunc(&lpIsComplex, lib, "lp_is_complex")
	purego.RegisterLibFunc(&lpCancelNew, lib, "lp_cancel_new")
	purego.RegisterLibFunc(&lpCancelTrigger, lib, "lp_cancel_trigger")
	purego.RegisterLibFunc(&lpCancelFree, lib, "lp_cancel_free")

	pdfium := opts.PDFiumPath
	if pdfium == "" && os.Getenv(EnvPDFiumPath) == "" && libPath != "" {
		if candidate := filepath.Join(filepath.Dir(libPath), pdfiumLibName()); fileExists(candidate) {
			pdfium = candidate
		}
	}
	if pdfium != "" {
		var out *byte
		code := lpLoadPDFium(cString(pdfium), &out)
		if err := takeError(code, out); err != nil {
			return fmt.Errorf("liteparse: load pdfium: %w", err)
		}
	}
	return nil
}

// openFFI dlopens the liteparse FFI library, returning its handle and, when
// resolved to a concrete file, its path.
func openFFI(explicit string) (uintptr, string, error) {
	name := ffiLibName()
	var candidates []string
	if explicit != "" {
		candidates = append(candidates, explicit)
	} else {
		if env := os.Getenv(EnvLibPath); env != "" {
			if st, err := os.Stat(env); err == nil && st.IsDir() {
				candidates = append(candidates, filepath.Join(env, name))
			} else {
				candidates = append(candidates, env)
			}
		}
		if exe, err := os.Executable(); err == nil {
			dir := filepath.Dir(exe)
			candidates = append(candidates, filepath.Join(dir, name), filepath.Join(dir, "lib", name))
		}
		candidates = append(candidates, filepath.Join("lib", name))
	}

	var errs []error
	for _, c := range candidates {
		if !fileExists(c) {
			continue
		}
		abs, err := filepath.Abs(c)
		if err != nil {
			abs = c
		}
		h, err := openLibrary(abs)
		if err == nil {
			return h, abs, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", abs, err))
	}
	if explicit == "" {
		// Let the system loader search (DYLD_LIBRARY_PATH, LD_LIBRARY_PATH, PATH...).
		h, err := openLibrary(name)
		if err == nil {
			return h, "", nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", name, err))
	}
	if len(errs) == 0 {
		errs = append(errs, fmt.Errorf("%s not found in %v", name, candidates))
	}
	return 0, "", fmt.Errorf("%w: set %s or call liteparse.Load: %w",
		ErrLibraryNotFound, EnvLibPath, errors.Join(errs...))
}

func ffiLibName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libliteparse_ffi.dylib"
	case "windows":
		return "liteparse_ffi.dll"
	default:
		return "libliteparse_ffi.so"
	}
}

func pdfiumLibName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libpdfium.dylib"
	case "windows":
		return "pdfium.dll"
	default:
		return "libpdfium.so"
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// cString returns a NUL-terminated copy of s.
func cString(s string) *byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return &b[0]
}

// goString copies a NUL-terminated C string. It does not free p.
func goString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}

// takeString copies and frees a string returned by the native side.
func takeString(p *byte) string {
	s := goString(p)
	lpStringFree(p)
	return s
}
