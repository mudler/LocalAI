package pdfread

import (
	"bytes"
	"crypto/md5"
	"crypto/rc4"
	"errors"
	"fmt"
	"image/png"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func mustOpen(data []byte) *Doc {
	GinkgoHelper()
	doc, err := Open(data)
	Expect(err).ToNot(HaveOccurred(), "Open")
	DeferCleanup(func() { _ = doc.Close() })
	return doc
}

const (
	testUserPassword  = "secret"
	testOwnerPassword = "owner-secret"
)

const helloContent = "BT /F1 24 Tf 20 60 Td (Hello PDF) Tj ET"

var _ = Describe("pdfread", func() {
	It("opens a document and reads its pages and text", func() {
		doc := mustOpen(helloPDF())

		Expect(doc.Pages()).To(Equal(1))
		text, err := doc.Text(1)
		Expect(err).ToNot(HaveOccurred())
		Expect(text).To(ContainSubstring("Hello PDF"))
	})

	It("reports whether a page has text", func() {
		doc := mustOpen(helloPDF())
		ok, err := doc.HasText(1)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue(), "HasText(1) on a page with text")

		blank := mustOpen(blankPDF())
		ok, err = blank.HasText(1)
		Expect(err).ToNot(HaveOccurred(), "HasText(1) on blank page")
		Expect(ok).To(BeFalse(), "HasText(1) on a page with no text")
	})

	It("renders a page as a PNG", func() {
		doc := mustOpen(helloPDF())

		// At 72 dpi a 300 by 144 point page is 300 by 144 pixels.
		data, err := doc.Render(1, 72)
		Expect(err).ToNot(HaveOccurred())
		Expect(len(data)).To(BeNumerically(">=", 8))
		Expect(data[:8]).To(Equal([]byte("\x89PNG\r\n\x1a\n")), "Render output is not a PNG")
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		Expect(err).ToNot(HaveOccurred(), "decode PNG")
		Expect(cfg.Width).To(Equal(300))
		Expect(cfg.Height).To(Equal(144))
		Expect(len(data)).To(BeNumerically(">=", 200), "PNG too small for a rendered page")
	})

	It("clamps the render DPI", func() {
		doc := mustOpen(helloPDF())

		cases := []struct {
			dpi       int
			wantWidth int
		}{
			{dpi: 1, wantWidth: 150},     // clamped to 36 dpi: 300 * 36 / 72
			{dpi: 1000, wantWidth: 1250}, // clamped to 300 dpi: 300 * 300 / 72
		}
		for _, c := range cases {
			data, err := doc.Render(1, c.dpi)
			Expect(err).ToNot(HaveOccurred(), "Render(1, %d)", c.dpi)
			cfg, err := png.DecodeConfig(bytes.NewReader(data))
			Expect(err).ToNot(HaveOccurred(), "decode PNG at dpi %d", c.dpi)
			Expect(cfg.Width).To(Equal(c.wantWidth), "dpi %d", c.dpi)
		}
	})

	It("returns ErrPageRange for pages out of range", func() {
		doc := mustOpen(helloPDF())

		for _, page := range []int{0, -1, 2} {
			_, err := doc.Text(page)
			Expect(err).To(MatchError(ErrPageRange), "Text(%d)", page)
			_, err = doc.HasText(page)
			Expect(err).To(MatchError(ErrPageRange), "HasText(%d)", page)
			_, err = doc.Render(page, 72)
			Expect(err).To(MatchError(ErrPageRange), "Render(%d)", page)
		}
	})

	It("rejects input that is not a PDF", func() {
		cases := map[string][]byte{
			"nil":    nil,
			"empty":  {},
			"text":   []byte("this is not a pdf file at all"),
			"binary": bytes.Repeat([]byte{0x00, 0xff, 0x7f}, 100),
		}
		for name, data := range cases {
			doc, err := Open(data)
			if doc != nil {
				_ = doc.Close()
			}
			Expect(err).To(MatchError(ErrNotPDF), "%s", name)
		}
	})

	It("rejects input that is too large", func() {
		Expect(MaxBytes).To(Equal(256<<20), "MaxBytes is 256 MiB")
		data := make([]byte, MaxBytes+1)
		copy(data, pdfMagic)
		_, err := Open(data)
		Expect(err).To(MatchError(ErrTooLarge), "Open(256 MiB + 1)")
		_, err = OpenWithPassword(data, "secret")
		Expect(err).To(MatchError(ErrTooLarge), "OpenWithPassword(256 MiB + 1)")
	})

	It("opens an encrypted document with the right password", func() {
		data := encryptedPDF(helloContent, testUserPassword, testOwnerPassword)
		doc, err := OpenWithPassword(data, testUserPassword)
		Expect(err).ToNot(HaveOccurred(), "OpenWithPassword(encrypted, right password)")
		DeferCleanup(func() { _ = doc.Close() })

		Expect(doc.Pages()).To(Equal(1))
		text, err := doc.Text(1)
		Expect(err).ToNot(HaveOccurred())
		Expect(text).To(ContainSubstring("Hello PDF"))
	})

	It("requires a password for an encrypted document", func() {
		data := encryptedPDF(helloContent, testUserPassword, testOwnerPassword)
		for name, open := range map[string]func() (*Doc, error){
			"Open":                   func() (*Doc, error) { return Open(data) },
			"OpenWithPassword(\"\")": func() (*Doc, error) { return OpenWithPassword(data, "") },
		} {
			doc, err := open()
			if doc != nil {
				_ = doc.Close()
			}
			Expect(doc).To(BeNil(), "%s: got a document for an encrypted file without a password", name)
			Expect(err).To(MatchError(ErrPasswordRequired), "%s", name)
		}
	})

	It("rejects a wrong password", func() {
		data := encryptedPDF(helloContent, testUserPassword, testOwnerPassword)
		doc, err := OpenWithPassword(data, "not-the-password")
		if doc != nil {
			_ = doc.Close()
		}
		Expect(doc).To(BeNil(), "got a document for a wrong password")
		Expect(err).To(MatchError(ErrWrongPassword))
		Expect(errors.Is(err, ErrPasswordRequired)).To(BeFalse(), "a wrong password must not report ErrPasswordRequired")
	})

	It("opens a plain document with any password", func() {
		doc, err := OpenWithPassword(helloPDF(), "")
		Expect(err).ToNot(HaveOccurred(), "OpenWithPassword(plain, empty)")
		DeferCleanup(func() { _ = doc.Close() })
		Expect(doc.Pages()).To(Equal(1))

		doc2, err := OpenWithPassword(helloPDF(), "unused")
		Expect(err).ToNot(HaveOccurred(), "OpenWithPassword(plain, password)")
		DeferCleanup(func() { _ = doc2.Close() })
		text, err := doc2.Text(1)
		Expect(err).ToNot(HaveOccurred())
		Expect(text).To(ContainSubstring("Hello PDF"))
	})

	It("returns ErrClosed after Close", func() {
		doc, err := Open(helloPDF())
		Expect(err).ToNot(HaveOccurred())
		Expect(doc.Close()).To(Succeed())
		Expect(doc.Close()).To(Succeed(), "second Close")
		_, err = doc.Text(1)
		Expect(err).To(MatchError(ErrClosed), "Text after Close")
		_, err = doc.Render(1, 72)
		Expect(err).To(MatchError(ErrClosed), "Render after Close")
		Expect(doc.Pages()).To(Equal(0), "Pages after Close")
	})

	// Opens every prefix of a valid PDF. Truncated input must produce an error
	// or a usable document, never a panic.
	It("does not panic on truncated prefixes", func() {
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
	})

	It("does not panic on garbage after the header", func() {
		junk := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("0 obj << /Type garbage >> endobj\x00\xff"), 50)...)
		doc, err := Open(junk)
		if err != nil {
			return
		}
		defer doc.Close()
		_, _ = doc.Text(1)
		_, _ = doc.Render(1, 72)
	})
})
