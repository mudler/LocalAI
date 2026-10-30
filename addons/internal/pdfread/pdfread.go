// Package pdfread reads PDF files: page count, page text, whether a page has
// text at all, and page rendering to PNG.
//
// It runs PDFium (github.com/klippa-app/go-pdfium) through its WebAssembly
// backend (wazero). The package needs no cgo and cross-builds for darwin and
// windows. The platform's production extraction uses the same backend.
//
// Each open document holds one PDFium instance from a shared pool. The pool
// keeps at most four instances, so at most four documents can be open at the
// same time. Close every document when you are done with it.
//
// The package never writes temporary files. Every public function turns a
// panic into an error.
package pdfread

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	pdfium_errors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

// MaxBytes is the largest input Open and OpenWithPassword accept. It bounds
// the memory one document can take.
const MaxBytes = 256 << 20

// Render DPI limits. Values outside the range are clamped.
const (
	MinDPI = 36
	MaxDPI = 300
)

// ErrTooLarge is returned by Open when the input is over MaxBytes.
var ErrTooLarge = errors.New("pdfread: input larger than 256 MiB")

// ErrNotPDF is returned by Open when the input does not look like a PDF.
var ErrNotPDF = errors.New("pdfread: input is not a PDF")

// ErrPasswordRequired is returned when the document is encrypted and no
// password was given.
var ErrPasswordRequired = errors.New("pdfread: document is encrypted and needs a password")

// ErrWrongPassword is returned when the document is encrypted and the given
// password is rejected.
var ErrWrongPassword = errors.New("pdfread: wrong password for encrypted document")

// ErrClosed is returned by methods of a Doc after Close.
var ErrClosed = errors.New("pdfread: document is closed")

// ErrPageRange is returned for a page number outside 1..Pages().
var ErrPageRange = errors.New("pdfread: page out of range")

const (
	pdfMagic     = "%PDF-"
	magicWindow  = 1024 // the spec allows junk before the header
	openTimeout  = 30 * time.Second
	poolMaxTotal = 4
)

var (
	poolOnce sync.Once
	pool     pdfium.Pool
	poolErr  error
)

func initPool() {
	poolOnce.Do(func() {
		pool, poolErr = webassembly.Init(webassembly.Config{
			MinIdle:  0,
			MaxIdle:  1,
			MaxTotal: poolMaxTotal,
			// The guest's stdout and stderr are discarded so PDFium
			// warnings do not reach the host's terminal.
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
	})
}

// Doc is an open PDF document. It is safe for concurrent use. Close it when
// done.
type Doc struct {
	mu     sync.Mutex
	inst   pdfium.Pdfium
	handle references.FPDF_DOCUMENT
	pages  int
	closed bool
}

// Open parses a PDF held in data. It is OpenWithPassword with an empty
// password, so an encrypted document returns ErrPasswordRequired.
func Open(data []byte) (*Doc, error) {
	return OpenWithPassword(data, "")
}

// OpenWithPassword parses a PDF held in data, decrypting it with password when
// it is encrypted. With an empty password, an encrypted document returns
// ErrPasswordRequired; a non-empty password that is rejected returns
// ErrWrongPassword. A password given for an unencrypted document is ignored.
// The bytes are copied into the PDFium instance, so the caller may reuse data
// after the call returns.
func OpenWithPassword(data []byte, password string) (doc *Doc, err error) {
	defer recoverErr(&err)

	if len(data) > MaxBytes {
		return nil, ErrTooLarge
	}
	head := data
	if len(head) > magicWindow {
		head = head[:magicWindow]
	}
	if !bytes.Contains(head, []byte(pdfMagic)) {
		return nil, ErrNotPDF
	}

	initPool()
	if poolErr != nil {
		return nil, fmt.Errorf("pdfread: pdfium init: %w", poolErr)
	}

	inst, err := pool.GetInstance(openTimeout)
	if err != nil {
		return nil, fmt.Errorf("pdfread: pdfium instance: %w", err)
	}

	buf := data
	req := &requests.OpenDocument{File: &buf}
	if password != "" {
		req.Password = &password
	}
	resp, err := inst.OpenDocument(req)
	if err != nil {
		_ = inst.Close()
		if errors.Is(err, pdfium_errors.ErrPassword) {
			if password == "" {
				return nil, ErrPasswordRequired
			}
			return nil, ErrWrongPassword
		}
		return nil, fmt.Errorf("pdfread: open: %w", err)
	}

	count, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: resp.Document})
	if err != nil {
		_, _ = inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: resp.Document})
		_ = inst.Close()
		return nil, fmt.Errorf("pdfread: page count: %w", err)
	}

	return &Doc{
		inst:   inst,
		handle: resp.Document,
		pages:  count.PageCount,
	}, nil
}

// Pages returns the number of pages. It returns 0 after Close.
func (d *Doc) Pages() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0
	}
	return d.pages
}

// Text returns the text of one page. Pages are numbered from 1.
func (d *Doc) Text(page int) (text string, err error) {
	defer recoverErr(&err)

	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkPage(page); err != nil {
		return "", err
	}

	resp, err := d.inst.GetPageText(&requests.GetPageText{
		Page: d.pageRef(page),
	})
	if err != nil {
		return "", fmt.Errorf("pdfread: text of page %d: %w", page, err)
	}
	return resp.Text, nil
}

// HasText reports whether a page has extractable text. A scanned page, which
// holds only images, returns false. The caller can send such a page to vision
// or OCR.
func (d *Doc) HasText(page int) (ok bool, err error) {
	text, err := d.Text(page)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(text) != "", nil
}

// Render draws one page as a PNG image. Pages are numbered from 1. The dpi is
// clamped to MinDPI..MaxDPI.
func (d *Doc) Render(page int, dpi int) (out []byte, err error) {
	defer recoverErr(&err)

	if dpi < MinDPI {
		dpi = MinDPI
	}
	if dpi > MaxDPI {
		dpi = MaxDPI
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.checkPage(page); err != nil {
		return nil, err
	}

	resp, err := d.inst.RenderPageInDPI(&requests.RenderPageInDPI{
		DPI:  dpi,
		Page: d.pageRef(page),
	})
	if err != nil {
		return nil, fmt.Errorf("pdfread: render page %d: %w", page, err)
	}
	// The image lives in WebAssembly memory until CleanupFunc runs, so encode
	// it before cleanup.
	defer func() {
		if resp.CleanupFunc != nil {
			resp.CleanupFunc()
		}
	}()
	if resp.Result.Image == nil {
		return nil, fmt.Errorf("pdfread: render page %d: no image", page)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, resp.Result.Image); err != nil {
		return nil, fmt.Errorf("pdfread: encode page %d: %w", page, err)
	}
	return buf.Bytes(), nil
}

// Close releases the document and its PDFium instance. Calling Close more
// than once is safe.
func (d *Doc) Close() (err error) {
	defer recoverErr(&err)

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	_, _ = d.inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: d.handle})
	return d.inst.Close()
}

// checkPage requires d.mu to be held.
func (d *Doc) checkPage(page int) error {
	if d.closed {
		return ErrClosed
	}
	if page < 1 || page > d.pages {
		return fmt.Errorf("%w: %d of %d", ErrPageRange, page, d.pages)
	}
	return nil
}

// pageRef converts a 1-based page number to PDFium's 0-based index.
func (d *Doc) pageRef(page int) requests.Page {
	return requests.Page{
		ByIndex: &requests.PageByIndex{
			Document: d.handle,
			Index:    page - 1,
		},
	}
}

// recoverErr turns a panic into an error on the named return value.
func recoverErr(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("pdfread: internal error: %v", r)
	}
}
