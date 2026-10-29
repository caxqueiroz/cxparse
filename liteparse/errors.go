package liteparse

import (
	"errors"
	"fmt"
)

// Sentinel errors. Match them with errors.Is; the returned *Error carries the
// native message.
var (
	ErrLibraryNotFound   = errors.New("liteparse: native library not found")
	ErrInvalidArgument   = errors.New("liteparse: invalid argument")
	ErrConfig            = errors.New("liteparse: invalid config")
	ErrPDF               = errors.New("liteparse: pdf error")
	ErrIO                = errors.New("liteparse: io error")
	ErrOCR               = errors.New("liteparse: ocr error")
	ErrConversion        = errors.New("liteparse: conversion error")
	ErrPDFiumUnavailable = errors.New("liteparse: pdfium library unavailable")
	ErrCanceled          = errors.New("liteparse: canceled")
	ErrPanic             = errors.New("liteparse: native panic")
	ErrClosed            = errors.New("liteparse: parser is closed")
	errUnknown           = errors.New("liteparse: error")
)

// Status codes returned by the native library (LP_* in ffi/src/lib.rs).
const (
	codeOK                = 0
	codeInvalidArgument   = 1
	codeConfig            = 2
	codePDF               = 3
	codeIO                = 4
	codeOCR               = 5
	codeConversion        = 6
	codePDFiumUnavailable = 7
	codeCanceled          = 8
	codePanic             = 9
)

// Error is a failure reported by the native library.
type Error struct {
	Code    int
	Message string
	kind    error
}

func (e *Error) Error() string { return fmt.Sprintf("%v: %s", e.kind, e.Message) }

// Unwrap exposes the sentinel for errors.Is.
func (e *Error) Unwrap() error { return e.kind }

func newError(code int32, msg string) *Error {
	var kind error
	switch code {
	case codeInvalidArgument:
		kind = ErrInvalidArgument
	case codeConfig:
		kind = ErrConfig
	case codePDF:
		kind = ErrPDF
	case codeIO:
		kind = ErrIO
	case codeOCR:
		kind = ErrOCR
	case codeConversion:
		kind = ErrConversion
	case codePDFiumUnavailable:
		kind = ErrPDFiumUnavailable
	case codeCanceled:
		kind = ErrCanceled
	case codePanic:
		kind = ErrPanic
	default:
		kind = errUnknown
	}
	return &Error{Code: int(code), Message: msg, kind: kind}
}

// takeResult consumes a native (code, out) pair: the payload on success, an
// *Error otherwise. out is always freed.
func takeResult(code int32, out *byte) (string, error) {
	s := takeString(out)
	if code != codeOK {
		return "", newError(code, s)
	}
	return s, nil
}

func takeError(code int32, out *byte) error {
	_, err := takeResult(code, out)
	return err
}
