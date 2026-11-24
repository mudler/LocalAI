// SPDX-License-Identifier: MIT
package tools

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/internal/pdfread"
	"github.com/mudler/LocalAI/pkg/web/webtools"
)

// MaxPDFPages bounds the pages fetch reads of one PDF.
const MaxPDFPages = 500

// PDFText returns the text of a PDF, page by page, each page under a
// "Page N" line. It reads with PDFium in WebAssembly (the reader the Office
// add-on uses), so no cgo is needed. It stops once the text is longer than
// fetch can return, and says how many pages it did not read. A scanned PDF has
// no text.
func PDFText(data []byte) (string, error) {
	doc, err := pdfread.Open(data)
	if err != nil {
		switch {
		case errors.Is(err, pdfread.ErrNotPDF):
			return "", errors.New("the file is not a PDF")
		case errors.Is(err, pdfread.ErrPasswordRequired):
			return "", errors.New("the PDF is protected by a password")
		}
		return "", errors.New("the PDF is damaged or uses a form the reader does not know")
	}
	defer func() { _ = doc.Close() }()
	n := doc.Pages()
	var b strings.Builder
	runes := 0
	read := 0
	for p := 1; p <= n && p <= MaxPDFPages; p++ {
		t, err := doc.Text(p)
		if err != nil {
			return "", fmt.Errorf("page %d of the PDF could not be read", p)
		}
		read = p
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "Page %d\n\n%s", p, t)
		runes += utf8.RuneCountInString(t)
		// Past the most fetch returns, more pages only cost time: the text is
		// cut anyway.
		if runes > webtools.HardMaxChars {
			break
		}
	}
	if read < n {
		fmt.Fprintf(&b, "\n\n[%d of %d pages not read]", n-read, n)
	}
	return b.String(), nil
}
