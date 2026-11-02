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
	"testing"

	"github.com/mudler/LocalAI/addons/office/internal/docx"
	"github.com/mudler/LocalAI/addons/office/internal/pdf"
	"github.com/mudler/LocalAI/addons/office/internal/textinput"
)

func reviewDOCX(t *testing.T, blocks []docx.Block) map[string]string {
	t.Helper()
	var out bytes.Buffer
	if err := docx.Write(&out, docx.Document{Blocks: blocks}); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		result[f.Name] = string(b)
	}
	return result
}
func reviewPDFText(t *testing.T, blocks []pdf.Block, bbox bool) string {
	t.Helper()
	data, err := pdf.Create(pdf.Spec{Pages: []pdf.Page{{Blocks: blocks}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "review.pdf")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	flag := "-layout"
	if bbox {
		flag = "-bbox"
	}
	out, err := exec.Command("pdftotext", flag, path, "-").CombinedOutput()
	if err != nil {
		t.Fatalf("required PDF extraction: %v: %s", err, out)
	}
	return string(out)
}
func TestReviewDOCXBreaksAndTabs(t *testing.T) {
	d, err := textinput.ParseMarkdown(context.Background(), "**bold  \nline** and *italic*\n\n```\nfirst\t<&\nsecond\tend\n```\n")
	if err != nil {
		t.Fatal(err)
	}
	x := reviewDOCX(t, d.Docx)["word/document.xml"]
	if strings.Count(x, "<w:br/>") != 2 || strings.Count(x, "<w:tab/>") != 2 {
		t.Fatalf("missing semantic breaks/tabs: %s", x)
	}
	if strings.Contains(x, "&#xA;") || strings.Contains(x, "&#x9;") {
		t.Fatalf("literal controls in text: %s", x)
	}
	if !strings.Contains(x, `<w:rPr><w:b/></w:rPr><w:t xml:space="preserve">bold</w:t><w:br/></w:r><w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">line</w:t>`) {
		t.Fatalf("bold style did not span break: %s", x)
	}
	if !strings.Contains(x, "&lt;&amp;") || !strings.Contains(x, "<w:i/>") {
		t.Fatalf("lost escaping/styles: %s", x)
	}
}
func TestReviewOrderedSequences(t *testing.T) {
	d, err := textinput.ParseMarkdown(context.Background(), "0. zero\n1. one\n\n   7. nestedseven\n   8. nestedeight\n\n2. two\n\nseparator\n\n5. five\n6. six\n\nseparator\n\n0. restartzero\n")
	if err != nil {
		t.Fatal(err)
	}
	p := reviewDOCX(t, d.Docx)
	dec := xml.NewDecoder(strings.NewReader(p["word/numbering.xml"]))
	var starts []string
	for {
		token, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if e, ok := token.(xml.StartElement); ok && e.Name.Local == "start" {
			for _, a := range e.Attr {
				if a.Name.Local == "val" {
					starts = append(starts, a.Value)
				}
			}
		}
	}
	if strings.Join(starts, ",") != "1,0,1,7,8,2,5,6,0" {
		t.Fatalf("DOCX numbering starts = %v", starts)
	}
	text := reviewPDFText(t, d.PDF, false)
	for _, pair := range []string{"0. zero", "1. one", "7. nestedseven", "8. nestedeight", "2. two", "5. five", "6. six", "0. restartzero"} {
		if !strings.Contains(strings.Join(strings.Fields(text), " "), pair) {
			t.Errorf("missing %q in PDF: %s", pair, text)
		}
	}
	// Existing structured callers use zero as the omitted/default start.
	legacy := reviewDOCX(t, []docx.Block{docx.Bullets{Ordered: true, Items: []string{"legacy"}}})
	if strings.Contains(legacy["word/numbering.xml"], `<w:start w:val="0"/>`) {
		t.Fatal("legacy default changed")
	}
	if !strings.Contains(strings.Join(strings.Fields(reviewPDFText(t, []pdf.Block{pdf.Bullets{Ordered: true, Items: []string{"legacy"}}}, false)), " "), "1. legacy") {
		t.Fatal("legacy PDF default changed")
	}
}
func TestReviewRejectEmptyListItems(t *testing.T) {
	for i, source := range []string{"- \n", "0. \n", "- \n- secret-marker\n", "1. secret-marker\n2. \n", "- parent\n\n  - \n", "1. parent\n\n   2. \n", "-\n  - child\n"} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			d, err := textinput.ParseMarkdown(context.Background(), source)
			if err == nil {
				t.Fatalf("empty item accepted: %#v", d)
			}
			if !strings.Contains(err.Error(), "empty list item") || strings.Contains(err.Error(), "secret-marker") {
				t.Fatalf("not actionable/content-free: %v", err)
			}
			if len(d.Docx) != 0 || len(d.PDF) != 0 {
				t.Fatal("partial document returned")
			}
		})
	}
}
