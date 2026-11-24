// SPDX-License-Identifier: MIT
package textinput_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/docx"
	"github.com/mudler/LocalAI/addons/office/internal/pdf"
	"github.com/mudler/LocalAI/addons/office/internal/textinput"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func reviewDOCX(blocks []docx.Block) map[string]string {
	GinkgoHelper()
	var out bytes.Buffer
	Expect(docx.Write(&out, docx.Document{Blocks: blocks})).To(Succeed())
	z, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	Expect(err).NotTo(HaveOccurred())
	result := map[string]string{}
	for _, f := range z.File {
		r, err := f.Open()
		Expect(err).NotTo(HaveOccurred())
		b, err := io.ReadAll(r)
		Expect(r.Close()).To(Succeed())
		Expect(err).NotTo(HaveOccurred())
		result[f.Name] = string(b)
	}
	return result
}
func reviewPDFText(blocks []pdf.Block, bbox bool) string {
	GinkgoHelper()
	data, err := pdf.Create(pdf.Spec{Pages: []pdf.Page{{Blocks: blocks}}})
	Expect(err).NotTo(HaveOccurred())
	path := filepath.Join(GinkgoT().TempDir(), "review.pdf")
	Expect(os.WriteFile(path, data, 0600)).To(Succeed())
	flag := "-layout"
	if bbox {
		flag = "-bbox"
	}
	out, err := exec.Command("pdftotext", flag, path, "-").CombinedOutput()
	if err != nil {
		Fail(fmt.Sprintf("required PDF extraction: %v: %s", err, out))
	}
	return string(out)
}

var _ = Describe("rendered Markdown regressions", func() {
	It("preserves semantic DOCX breaks, tabs, styles and escaping", func() {
		d, err := textinput.ParseMarkdown(context.Background(), "**bold  \nline** and *italic*\n\n```\nfirst\t<&\nsecond\tend\n```\n")
		Expect(err).NotTo(HaveOccurred())
		x := reviewDOCX(d.Docx)["word/document.xml"]
		if strings.Count(x, "<w:br/>") != 2 || strings.Count(x, "<w:tab/>") != 2 {
			Fail(fmt.Sprintf("missing semantic breaks/tabs: %s", x))
		}
		if strings.Contains(x, "&#xA;") || strings.Contains(x, "&#x9;") {
			Fail(fmt.Sprintf("literal controls in text: %s", x))
		}
		if !strings.Contains(x, `<w:rPr><w:b/></w:rPr><w:t xml:space="preserve">bold</w:t><w:br/></w:r><w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">line</w:t>`) {
			Fail(fmt.Sprintf("bold style did not span break: %s", x))
		}
		if !strings.Contains(x, "&lt;&amp;") || !strings.Contains(x, "<w:i/>") {
			Fail(fmt.Sprintf("lost escaping/styles: %s", x))
		}
	})
	It("preserves ordered sequences and legacy defaults in DOCX and PDF", func() {
		d, err := textinput.ParseMarkdown(context.Background(), "0. zero\n1. one\n\n   7. nestedseven\n   8. nestedeight\n\n2. two\n\nseparator\n\n5. five\n6. six\n\nseparator\n\n0. restartzero\n")
		Expect(err).NotTo(HaveOccurred())
		p := reviewDOCX(d.Docx)
		dec := xml.NewDecoder(strings.NewReader(p["word/numbering.xml"]))
		var starts []string
		for {
			token, err := dec.Token()
			if err == io.EOF {
				break
			}
			Expect(err).NotTo(HaveOccurred())
			if e, ok := token.(xml.StartElement); ok && e.Name.Local == "start" {
				for _, a := range e.Attr {
					if a.Name.Local == "val" {
						starts = append(starts, a.Value)
					}
				}
			}
		}
		if strings.Join(starts, ",") != "1,0,1,7,8,2,5,6,0" {
			Fail(fmt.Sprintf("DOCX numbering starts = %v", starts))
		}
		text := reviewPDFText(d.PDF, false)
		for _, pair := range []string{"0. zero", "1. one", "7. nestedseven", "8. nestedeight", "2. two", "5. five", "6. six", "0. restartzero"} {
			if !strings.Contains(strings.Join(strings.Fields(text), " "), pair) {
				Fail(fmt.Sprintf("missing %q in PDF: %s", pair, text))
			}
		}
		// Existing structured callers use zero as the omitted/default start.
		legacy := reviewDOCX([]docx.Block{docx.Bullets{Ordered: true, Items: []string{"legacy"}}})
		if strings.Contains(legacy["word/numbering.xml"], `<w:start w:val="0"/>`) {
			Fail("legacy default changed")
		}
		if !strings.Contains(strings.Join(strings.Fields(reviewPDFText([]pdf.Block{pdf.Bullets{Ordered: true, Items: []string{"legacy"}}}, false)), " "), "1. legacy") {
			Fail("legacy PDF default changed")
		}
	})
	Describe("empty list items", func() {
		for _, source := range []string{"- \n", "0. \n", "- \n- secret-marker\n", "1. secret-marker\n2. \n", "- parent\n\n  - \n", "1. parent\n\n   2. \n", "-\n  - child\n"} {
			It("rejects "+source, func() {
				d, err := textinput.ParseMarkdown(context.Background(), source)
				if err == nil {
					Fail(fmt.Sprintf("empty item accepted: %#v", d))
				}
				if !strings.Contains(err.Error(), "empty list item") || strings.Contains(err.Error(), "secret-marker") {
					Fail(fmt.Sprintf("not actionable/content-free: %v", err))
				}
				if len(d.Docx) != 0 || len(d.PDF) != 0 {
					Fail("partial document returned")
				}
			})
		}
	})
})
