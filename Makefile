# Builds the native liteparse FFI library and stages it, together with the
# PDFium library it loads, into ./lib. The Go package finds both there (or
# next to your executable) with no environment variables.

CARGO_FEATURES ?=
LIB_DIR        := lib
UNAME_S        := $(shell uname -s)

ifeq ($(UNAME_S),Darwin)
  FFI_LIB    := libliteparse_ffi.dylib
  PDFIUM_LIB := libpdfium.dylib
else
  FFI_LIB    := libliteparse_ffi.so
  PDFIUM_LIB := libpdfium.so
endif

GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
DIST   := dist/liteparse-native_$(GOOS)_$(GOARCH).tar.gz

.PHONY: native dist test lint clean

## native: build the Rust FFI library (add CARGO_FEATURES=tesseract for built-in OCR)
native:
	cd ffi && cargo build --release $(if $(CARGO_FEATURES),--features $(CARGO_FEATURES))
	mkdir -p $(LIB_DIR)
	cp ffi/target/release/$(FFI_LIB) $(LIB_DIR)/
	# liteparse-pdfium-sys copies the PDFium it downloaded into deps/.
	cp ffi/target/release/deps/$(PDFIUM_LIB) $(LIB_DIR)/
	@echo "staged native libraries in $(LIB_DIR)/"

## dist: package ./lib as the release archive liteparse-fetch downloads
dist: native
	mkdir -p dist
	COPYFILE_DISABLE=1 tar -czf $(DIST) -C $(LIB_DIR) $(FFI_LIB) $(PDFIUM_LIB)
	@echo "wrote $(DIST)"

## test: run Rust and Go tests
test: native
	cd ffi && cargo test --release
	LITEPARSE_LIB_PATH=$(CURDIR)/$(LIB_DIR) go test -race ./...

lint:
	go vet ./...
	golangci-lint run ./...

clean:
	cd ffi && cargo clean
	rm -rf $(LIB_DIR) dist
