package pdfread

import (
	"bytes"
	"crypto/md5"
	"crypto/rc4"
	"errors"
	"fmt"
	"image/png"
	"strings"
	"testing"
)

// assemble writes a PDF from the bodies of objects 1..n. trailer holds the
// trailer entries after /Size, for example "/Root 1 0 R".
func assemble(objs [][]byte, trailer string) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objs))
	for i, obj := range objs {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n", i+1)
		buf.Write(obj)
		buf.WriteString("\nendobj\n")
	}

	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d %s >>\nstartxref\n%d\n%%%%EOF\n",
		len(objs)+1, trailer, xref)
	return buf.Bytes()
}

// pageObjects returns objects 1 to 4: the catalog, the page tree, a 300 by
// 144 point page whose content is object 5, and the Helvetica font as /F1.
func pageObjects() [][]byte {
	return [][]byte{
		[]byte("<< /Type /Catalog /Pages 2 0 R >>"),
		[]byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"),
		[]byte("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 144] " +
			"/Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>"),
		[]byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"),
	}
}

// streamObject is a stream object holding data as it is written.
func streamObject(data []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "<< /Length %d >>\nstream\n", len(data))
	b.Write(data)
	b.WriteString("\nendstream")
	return b.Bytes()
}

// buildPDF returns a one-page PDF whose page content stream is content. It is
// built by hand so the tests need no PDF writer.
func buildPDF(content string) []byte {
	objs := append(pageObjects(), streamObject([]byte(content)))
	return assemble(objs, "/Root 1 0 R")
}

// rc4XOR encrypts or decrypts src with RC4 under key.
func rc4XOR(key, src []byte) []byte {
	c, err := rc4.NewCipher(key)
	if err != nil {
		panic(err)
	}
	out := make([]byte, len(src))
	c.XORKeyStream(out, src)
	return out
}

// pdfPadding is the password padding string of PDF 1.7, section 7.6.3.3.
const pdfPadding = "\x28\xbf\x4e\x5e\x4e\x75\x8a\x41\x64\x00\x4e\x56\xff\xfa\x01\x08" +
	"\x2e\x2e\x00\xb6\xd0\x68\x3e\x80\x2f\x0c\xa9\xfe\x64\x53\x69\x7a"

func padPassword(password string) []byte {
	out := make([]byte, 32)
	n := copy(out, password)
	copy(out[n:], pdfPadding)
	return out
}

// encryptedPDF returns the page of helloPDF encrypted with the RC4 40-bit
// standard security handler (revision 2). The user password is userPW and the
// owner password is ownerPW. Object 5, the content stream, is encrypted. The
// key derivation follows PDF 1.7, algorithms 1 to 4.
func encryptedPDF(content, userPW, ownerPW string) []byte {
	id := []byte("0123456789abcdef")

	// Algorithm 3: the owner key encrypts the user password into /O.
	ownerSum := md5.Sum(padPassword(ownerPW))
	o := rc4XOR(ownerSum[:5], padPassword(userPW))

	// Algorithm 2: the file key comes from the user password, /O, /P and the
	// first file identifier.
	h := md5.New()
	h.Write(padPassword(userPW))
	h.Write(o)
	h.Write([]byte{0xfc, 0xff, 0xff, 0xff}) // /P -4, all permissions
	h.Write(id)
	fileKey := h.Sum(nil)[:5]

	// Algorithm 4: /U is the padding string encrypted with the file key.
	u := rc4XOR(fileKey, []byte(pdfPadding))

	// Algorithm 1: each object is encrypted with its own key.
	num := 5
	oh := md5.New()
	oh.Write(fileKey)
	oh.Write([]byte{byte(num), byte(num >> 8), byte(num >> 16), 0, 0})
	objKey := oh.Sum(nil)[:10]
	encrypted := rc4XOR(objKey, []byte(content))

	objs := append(pageObjects(), streamObject(encrypted))
	objs = append(objs, []byte(fmt.Sprintf(
		"<< /Filter /Standard /V 1 /R 2 /Length 40 /O <%x> /U <%x> /P -4 >>", o, u)))
	return assemble(objs, fmt.Sprintf("/Root 1 0 R /Encrypt 6 0 R /ID [<%x> <%x>]", id, id))
}

func helloPDF() []byte {
	return buildPDF("BT /F1 24 Tf 20 60 Td (Hello PDF) Tj ET")
}

func blankPDF() []byte {
	return buildPDF("")
}

func mustOpen(t *testing.T, data []byte) *Doc {
	t.Helper()
	doc, err := Open(data)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = doc.Close() })
	return doc
}

func TestOpenPagesText(t *testing.T) {
	doc := mustOpen(t, helloPDF())

	if got := doc.Pages(); got != 1 {
		t.Fatalf("Pages() = %d, want 1", got)
	}
	text, err := doc.Text(1)
	if err != nil {
		t.Fatalf("Text(1): %v", err)
	}
	if !strings.Contains(text, "Hello PDF") {
		t.Fatalf("Text(1) = %q, want it to contain %q", text, "Hello PDF")
	}
}

func TestHasText(t *testing.T) {
	doc := mustOpen(t, helloPDF())
	ok, err := doc.HasText(1)
	if err != nil {
		t.Fatalf("HasText(1): %v", err)
	}
	if !ok {
		t.Fatal("HasText(1) = false on a page with text")
	}

	blank := mustOpen(t, blankPDF())
	ok, err = blank.HasText(1)
	if err != nil {
		t.Fatalf("HasText(1) on blank page: %v", err)
	}
	if ok {
		t.Fatal("HasText(1) = true on a page with no text")
	}
}

func TestRenderPNG(t *testing.T) {
	doc := mustOpen(t, helloPDF())

	// At 72 dpi a 300 by 144 point page is 300 by 144 pixels.
	data, err := doc.Render(1, 72)
	if err != nil {
		t.Fatalf("Render(1, 72): %v", err)
	}
	if len(data) < 8 || !bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("Render output is not a PNG: % x", data[:min(8, len(data))])
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	if cfg.Width != 300 || cfg.Height != 144 {
		t.Fatalf("PNG size = %dx%d, want 300x144", cfg.Width, cfg.Height)
	}
	if len(data) < 200 {
		t.Fatalf("PNG is only %d bytes, too small for a rendered page", len(data))
	}
}

func TestRenderDPIClamp(t *testing.T) {
	doc := mustOpen(t, helloPDF())

	cases := []struct {
		dpi       int
		wantWidth int
	}{
		{dpi: 1, wantWidth: 150},     // clamped to 36 dpi: 300 * 36 / 72
		{dpi: 1000, wantWidth: 1250}, // clamped to 300 dpi: 300 * 300 / 72
	}
	for _, c := range cases {
		data, err := doc.Render(1, c.dpi)
		if err != nil {
			t.Fatalf("Render(1, %d): %v", c.dpi, err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("decode PNG at dpi %d: %v", c.dpi, err)
		}
		if cfg.Width != c.wantWidth {
			t.Errorf("dpi %d: width = %d, want %d", c.dpi, cfg.Width, c.wantWidth)
		}
	}
}

func TestPageRange(t *testing.T) {
	doc := mustOpen(t, helloPDF())

	for _, page := range []int{0, -1, 2} {
		if _, err := doc.Text(page); !errors.Is(err, ErrPageRange) {
			t.Errorf("Text(%d) error = %v, want ErrPageRange", page, err)
		}
		if _, err := doc.HasText(page); !errors.Is(err, ErrPageRange) {
			t.Errorf("HasText(%d) error = %v, want ErrPageRange", page, err)
		}
		if _, err := doc.Render(page, 72); !errors.Is(err, ErrPageRange) {
			t.Errorf("Render(%d) error = %v, want ErrPageRange", page, err)
		}
	}
}

func TestOpenRejectsNonPDF(t *testing.T) {
	cases := map[string][]byte{
		"nil":    nil,
		"empty":  {},
		"text":   []byte("this is not a pdf file at all"),
		"binary": bytes.Repeat([]byte{0x00, 0xff, 0x7f}, 100),
	}
	for name, data := range cases {
		doc, err := Open(data)
		if !errors.Is(err, ErrNotPDF) {
			if doc != nil {
				_ = doc.Close()
			}
			t.Errorf("%s: Open error = %v, want ErrNotPDF", name, err)
		}
	}
}

func TestOpenRejectsTooLarge(t *testing.T) {
	if MaxBytes != 256<<20 {
		t.Fatalf("MaxBytes = %d, want 256 MiB", MaxBytes)
	}
	data := make([]byte, MaxBytes+1)
	copy(data, pdfMagic)
	if _, err := Open(data); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Open(256 MiB + 1) error = %v, want ErrTooLarge", err)
	}
	if _, err := OpenWithPassword(data, "secret"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("OpenWithPassword(256 MiB + 1) error = %v, want ErrTooLarge", err)
	}
}

const (
	testUserPassword  = "secret"
	testOwnerPassword = "owner-secret"
)

func TestOpenEncryptedWithPassword(t *testing.T) {
	data := encryptedPDF("BT /F1 24 Tf 20 60 Td (Hello PDF) Tj ET", testUserPassword, testOwnerPassword)
	doc, err := OpenWithPassword(data, testUserPassword)
	if err != nil {
		t.Fatalf("OpenWithPassword(encrypted, right password): %v", err)
	}
	t.Cleanup(func() { _ = doc.Close() })

	if got := doc.Pages(); got != 1 {
		t.Fatalf("Pages() = %d, want 1", got)
	}
	text, err := doc.Text(1)
	if err != nil {
		t.Fatalf("Text(1): %v", err)
	}
	if !strings.Contains(text, "Hello PDF") {
		t.Fatalf("Text(1) = %q, want it to contain %q", text, "Hello PDF")
	}
}

func TestOpenEncryptedNoPassword(t *testing.T) {
	data := encryptedPDF("BT /F1 24 Tf 20 60 Td (Hello PDF) Tj ET", testUserPassword, testOwnerPassword)
	for name, open := range map[string]func() (*Doc, error){
		"Open":                   func() (*Doc, error) { return Open(data) },
		"OpenWithPassword(\"\")": func() (*Doc, error) { return OpenWithPassword(data, "") },
	} {
		doc, err := open()
		if doc != nil {
			_ = doc.Close()
			t.Errorf("%s: got a document for an encrypted file without a password", name)
		}
		if !errors.Is(err, ErrPasswordRequired) {
			t.Errorf("%s: error = %v, want ErrPasswordRequired", name, err)
		}
	}
}

func TestOpenEncryptedWrongPassword(t *testing.T) {
	data := encryptedPDF("BT /F1 24 Tf 20 60 Td (Hello PDF) Tj ET", testUserPassword, testOwnerPassword)
	doc, err := OpenWithPassword(data, "not-the-password")
	if doc != nil {
		_ = doc.Close()
		t.Fatal("got a document for a wrong password")
	}
	if !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("error = %v, want ErrWrongPassword", err)
	}
	if errors.Is(err, ErrPasswordRequired) {
		t.Fatal("a wrong password must not report ErrPasswordRequired")
	}
}

func TestOpenPlainWithPassword(t *testing.T) {
	doc, err := OpenWithPassword(helloPDF(), "")
	if err != nil {
		t.Fatalf("OpenWithPassword(plain, empty): %v", err)
	}
	defer doc.Close()
	if got := doc.Pages(); got != 1 {
		t.Fatalf("Pages() = %d, want 1", got)
	}

	doc2, err := OpenWithPassword(helloPDF(), "unused")
	if err != nil {
		t.Fatalf("OpenWithPassword(plain, password): %v", err)
	}
	defer doc2.Close()
	if text, err := doc2.Text(1); err != nil || !strings.Contains(text, "Hello PDF") {
		t.Fatalf("Text(1) = %q, %v; want it to contain %q", text, err, "Hello PDF")
	}
}

func TestClosed(t *testing.T) {
	doc, err := Open(helloPDF())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := doc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := doc.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := doc.Text(1); !errors.Is(err, ErrClosed) {
		t.Fatalf("Text after Close error = %v, want ErrClosed", err)
	}
	if _, err := doc.Render(1, 72); !errors.Is(err, ErrClosed) {
		t.Fatalf("Render after Close error = %v, want ErrClosed", err)
	}
	if got := doc.Pages(); got != 0 {
		t.Fatalf("Pages after Close = %d, want 0", got)
	}
}

// TestNoPanicOnPrefixes opens every prefix of a valid PDF. Truncated input
// must produce an error or a usable document, never a panic.
func TestNoPanicOnPrefixes(t *testing.T) {
	good := helloPDF()
	for i := 0; i <= len(good); i++ {
		doc, err := Open(good[:i])
		if err != nil {
			continue
		}
		_, _ = doc.Text(1)
		_, _ = doc.Render(1, 36)
		_ = doc.Close()
	}
}

func TestNoPanicOnGarbageAfterHeader(t *testing.T) {
	junk := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("0 obj << /Type garbage >> endobj\x00\xff"), 50)...)
	doc, err := Open(junk)
	if err != nil {
		return
	}
	defer doc.Close()
	_, _ = doc.Text(1)
	_, _ = doc.Render(1, 72)
}
