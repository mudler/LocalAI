// Package pdf creates, merges, splits, rotates, stamps and fills PDF files.
// Creation uses go-pdf/fpdf. Everything else uses pdfcpu, through its
// in-memory API. No temporary file is written.
//
// This is a nested module (its own go.mod). Its two third-party
// dependencies stay out of the builds of the root module.
//
// Page numbers are 1-based. Every public function returns an error for
// malformed bytes, bad page numbers and bad arguments. A panic inside a
// library call is turned into an error.
package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// Size limits. Input over MaxInputBytes is refused. A Spec over MaxPages
// pages or MaxBlocks blocks is refused.
const (
	MaxInputBytes = 64 << 20
	MaxPages      = 1000
	MaxBlocks     = 2000
)

// errEmptyInput is returned when a function gets no bytes.
var errEmptyInput = errors.New("pdf: input is empty")

// checkInput refuses empty input and input over MaxInputBytes.
func checkInput(data []byte) error {
	if len(data) == 0 {
		return errEmptyInput
	}
	if len(data) > MaxInputBytes {
		return fmt.Errorf("pdf: input is %d bytes, the limit is %d", len(data), MaxInputBytes)
	}
	return nil
}

// guard runs fn and turns a panic into an error. Every pdfcpu and fpdf call
// goes through it.
func guard(op string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("pdf: %s: internal error: %v", op, r)
		}
	}()
	return fn()
}

// Check validates the bytes with pdfcpu. It returns nil when the file is a
// valid PDF.
func Check(data []byte) error {
	if err := checkInput(data); err != nil {
		return err
	}
	return guard("check", func() error {
		if err := validate(bytes.NewReader(data)); err != nil {
			return fmt.Errorf("pdf: check: %w", err)
		}
		return nil
	})
}

// Pages returns the number of pages of a PDF.
func Pages(data []byte) (int, error) {
	if err := checkInput(data); err != nil {
		return 0, err
	}
	var n int
	err := guard("pages", func() error {
		count, err := pageCount(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("pdf: pages: %w", err)
		}
		n = count
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// checkPage returns an error when a 1-based page number is outside 1..n.
func checkPage(page, n int) error {
	if page < 1 || page > n {
		return fmt.Errorf("pdf: page %d is outside 1..%d", page, n)
	}
	return nil
}

// writeTo is a helper for the pdfcpu calls that write to an io.Writer.
func writeTo(op string, fn func(w io.Writer) error) ([]byte, error) {
	var out bytes.Buffer
	err := guard(op, func() error {
		if err := fn(&out); err != nil {
			return fmt.Errorf("pdf: %s: %w", op, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
