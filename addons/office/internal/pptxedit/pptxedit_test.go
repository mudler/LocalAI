package pptxedit

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	"github.com/mudler/LocalAI/addons/office/internal/deck/pptx"
	"github.com/mudler/LocalAI/addons/office/internal/officecheck"
	"github.com/mudler/LocalAI/addons/office/internal/officeread"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const testLayout = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
	`<p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="obj" preserve="1">` +
	`<p:cSld name="Title and Content"><p:spTree>` + groupHeader +
	`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Title 1"/><p:cNvSpPr/><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:t>Click to edit</a:t></a:r></a:p></p:txBody></p:sp>` +
	`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Content 2"/><p:cNvSpPr/><p:nvPr><p:ph idx="1"/></p:nvPr></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:t>Click to edit</a:t></a:r></a:p></p:txBody></p:sp>` +
	`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`

const (
	slide1Shapes = `<p:sp><p:nvSpPr><p:cNvPr id="90" name="Title 1"/><p:cNvSpPr/><p:nvPr><p:ph type="ctrTitle"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		`<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" b="1"/><a:t>Deck title</a:t></a:r></a:p></p:txBody></p:sp>`
	slide2Shapes = `<p:sp><p:nvSpPr><p:cNvPr id="90" name="Title 1"/><p:cNvSpPr/><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		`<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" sz="3000"/><a:t>Findings</a:t></a:r></a:p></p:txBody></p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="91" name="Body 2"/><p:cNvSpPr/><p:nvPr><p:ph idx="1"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		`<p:txBody><a:bodyPr/><a:lstStyle/>` +
		`<a:p><a:pPr marL="342900" indent="-342900"/><a:r><a:rPr lang="en-US" b="1"/><a:t>Alpha </a:t></a:r><a:r><a:rPr lang="en-US" i="1"/><a:t>beta</a:t></a:r></a:p>` +
		`<a:p><a:r><a:rPr lang="en-US"/><a:t>Gamma</a:t></a:r></a:p></p:txBody></p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="92" name="Footer 3"/><p:cNvSpPr/><p:nvPr><p:ph type="ftr" idx="11"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		`<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:t>Footer text</a:t></a:r></a:p></p:txBody></p:sp>`
)

// rewriteZip returns data with the named parts changed by fn, in the same order.
func rewriteZip(data []byte, fns map[string]func(string) string) []byte {
	GinkgoHelper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	Expect(err).NotTo(HaveOccurred())
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		rc, err := f.Open()
		Expect(err).NotTo(HaveOccurred())
		b, err := io.ReadAll(rc)
		rc.Close()
		Expect(err).NotTo(HaveOccurred())
		if fn := fns[f.Name]; fn != nil {
			b = []byte(fn(string(b)))
		}
		w, err := zw.Create(f.Name)
		Expect(err).NotTo(HaveOccurred())
		w.Write(b)
	}
	Expect(zw.Close()).To(Succeed())
	return out.Bytes()
}

func addShapes(shapes string) func(string) string {
	return func(s string) string {
		return strings.Replace(s, "</p:spTree>", shapes+"</p:spTree>", 1)
	}
}

// replaceShapes drops every shape of a slide written by deck/pptx and puts
// shapes in their place.
func replaceShapes(shapes string) func(string) string {
	return func(s string) string {
		start := strings.Index(s, groupHeader) + len(groupHeader)
		end := strings.Index(s, "</p:spTree>")
		return s[:start] + shapes + s[end:]
	}
}

// sourceDeck builds a three-slide deck with pptx.Write (slide 1 has notes),
// then gives it a layout with placeholders and title and body shapes on the
// first two slides, as a deck made by an office application has. The third
// slide has no title, so it has no title placeholder.
func sourceDeck() []byte {
	GinkgoHelper()
	slides := []deck.StudioSlide{
		{ID: "s1", Title: "Opening", Layout: deck.LayoutTitle, Status: deck.SlideOK, Notes: "Say hello first."},
		{ID: "s2", Title: "Second", Layout: deck.LayoutText, Status: deck.SlideOK, Bullets: []deck.StudioBullet{{Text: "Writer bullet"}}},
		{ID: "s3", Title: "", Layout: deck.LayoutText, Status: deck.SlideOK, Bullets: []deck.StudioBullet{{Text: "Last point"}}},
	}
	var buf bytes.Buffer
	Expect(pptx.Write(&buf, slides, pptx.Options{Title: "Source", Application: "test", Notes: true}, nil)).To(Succeed())
	return rewriteZip(buf.Bytes(), map[string]func(string) string{
		"ppt/slideLayouts/slideLayout2.xml": func(string) string { return testLayout },
		"ppt/slides/slide1.xml":             replaceShapes(slide1Shapes),
		"ppt/slides/slide2.xml":             replaceShapes(slide2Shapes),
	})
}

func zipParts(data []byte) (names []string, parts map[string][]byte) {
	GinkgoHelper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	Expect(err).NotTo(HaveOccurred())
	parts = map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		Expect(err).NotTo(HaveOccurred())
		b, err := io.ReadAll(rc)
		rc.Close()
		Expect(err).NotTo(HaveOccurred())
		names = append(names, f.Name)
		parts[f.Name] = b
	}
	return names, parts
}

func mustCheckClean(data []byte) {
	GinkgoHelper()
	Expect(officecheck.Check(data)).To(BeEmpty(), "officecheck problems")
}

// result returns the bytes of d, checked by officecheck, and the text of each slide.
func result(d *Deck) ([]byte, []string) {
	GinkgoHelper()
	out, err := d.Bytes()
	Expect(err).NotTo(HaveOccurred(), "Bytes")
	mustCheckClean(out)
	doc, err := officeread.Read(out)
	Expect(err).NotTo(HaveOccurred(), "officeread")
	var texts []string
	for _, p := range doc.Parts {
		texts = append(texts, p.Text)
	}
	return out, texts
}

func openSource() (*Deck, []byte) {
	GinkgoHelper()
	src := sourceDeck()
	mustCheckClean(src)
	d, err := Open(src)
	Expect(err).NotTo(HaveOccurred(), "Open")
	return d, src
}

func contains(lines []string, s string) bool {
	for _, l := range lines {
		if l == s {
			return true
		}
	}
	return false
}

// assertSameParts fails unless every part of a and b outside changed is
// byte-identical and the common parts keep their order.
func assertSameParts(a, b []byte, changed map[string]bool) {
	GinkgoHelper()
	an, ap := zipParts(a)
	bn, bp := zipParts(b)
	var common []string
	for _, n := range an {
		if _, ok := bp[n]; ok {
			common = append(common, n)
		}
	}
	var bcommon []string
	for _, n := range bn {
		if _, ok := ap[n]; ok {
			bcommon = append(bcommon, n)
		}
	}
	Expect(strings.Join(common, "|")).To(Equal(strings.Join(bcommon, "|")), "part order changed")
	for _, n := range common {
		if changed[n] {
			Expect(bytes.Equal(ap[n], bp[n])).To(BeFalse(), "%s should have changed", n)
			continue
		}
		Expect(bytes.Equal(ap[n], bp[n])).To(BeTrue(), "%s changed but should not have", n)
	}
}

var _ = Describe("pptxedit", func() {
	It("opens a deck and lists its slides", func() {
		d, src := openSource()
		got := d.Slides()
		Expect(got).To(HaveLen(3))
		for i, s := range got {
			Expect(s.Index).To(Equal(i+1), "slide %d Index", i+1)
		}
		Expect(got[0].Title).To(Equal("Deck title"))
		Expect(got[1].Title).To(Equal("Findings"))
		Expect(got[2].Title).To(Equal(""))
		Expect(contains(got[1].Text, "Alpha beta")).To(BeTrue(), "slide 2 text: %q", got[1].Text)
		Expect(contains(got[1].Text, "Gamma")).To(BeTrue(), "slide 2 text: %q", got[1].Text)
		Expect(contains(got[1].Text, "Footer text")).To(BeFalse(), "footer placeholder text must not be listed: %q", got[1].Text)
		// Text matches what officeread reads.
		doc, err := officeread.Read(src)
		Expect(err).NotTo(HaveOccurred())
		for i, s := range got {
			want := strings.Split(doc.Parts[i].Text, "\n")
			if i == 0 { // officeread appends the notes after a "Notes:" line
				want = want[:len(want)-2]
			}
			Expect(strings.Join(s.Text, "\n")).To(Equal(strings.Join(want, "\n")), "slide %d text vs officeread (%q)", i+1, doc.Parts[i].Text)
		}
		// Opening and saving without an edit changes nothing.
		out, err := d.Bytes()
		Expect(err).NotTo(HaveOccurred())
		assertSameParts(src, out, nil)
	})

	It("sets slide titles and keeps run properties", func() {
		d, src := openSource()
		Expect(d.SetTitle(1, `R&D <review> "2026"`)).To(Succeed())
		Expect(d.SetTitle(2, "What we found")).To(Succeed())
		out, texts := result(d)
		Expect(texts[0]).To(ContainSubstring(`R&D <review> "2026"`))
		Expect(texts[0]).NotTo(ContainSubstring("Deck title"))
		Expect(texts[1]).To(ContainSubstring("What we found"))
		Expect(texts[1]).NotTo(ContainSubstring("Findings"))
		Expect(texts[1]).To(ContainSubstring("Alpha beta"), "body of slide 2 changed")
		re, err := Open(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(re.Slides()[0].Title).To(Equal(`R&D <review> "2026"`))
		_, parts := zipParts(out)
		Expect(string(parts["ppt/slides/slide1.xml"])).To(ContainSubstring(`<a:rPr lang="en-US" b="1"/>`), "run properties of the title were lost")
		assertSameParts(src, out, map[string]bool{"ppt/slides/slide1.xml": true, "ppt/slides/slide2.xml": true})
	})

	It("drops extra paragraphs of a title", func() {
		src := rewriteZip(sourceDeck(), map[string]func(string) string{
			"ppt/slides/slide2.xml": func(s string) string {
				return strings.Replace(s, `<a:t>Findings</a:t></a:r></a:p>`, `<a:t>Findings</a:t></a:r></a:p><a:p><a:r><a:t>Second line</a:t></a:r></a:p>`, 1)
			},
		})
		d, err := Open(src)
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Slides()[1].Title).To(Equal("Findings\nSecond line"))
		Expect(d.SetTitle(2, "One")).To(Succeed())
		_, texts := result(d)
		Expect(texts[1]).NotTo(ContainSubstring("Second line"))
		Expect(texts[1]).To(HavePrefix("One\nAlpha beta\n"))
	})

	It("sets a title on a shape without a text body", func() {
		src := rewriteZip(sourceDeck(), map[string]func(string) string{
			"ppt/slides/slide1.xml": func(s string) string {
				i := strings.Index(s, `<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" b="1"/><a:t>Deck title`)
				j := strings.Index(s[i:], "</p:txBody>") + len("</p:txBody>")
				return s[:i] + s[i+j:]
			},
		})
		d, err := Open(src)
		Expect(err).NotTo(HaveOccurred())
		Expect(d.SetTitle(1, "Fresh")).To(Succeed())
		_, texts := result(d)
		Expect(texts[0]).To(ContainSubstring("Fresh"))
	})

	It("sets a paragraph and keeps the formatting of its first run", func() {
		d, src := openSource()
		Expect(d.SetParagraph(2, "Body 2", 0, "Delta & <epsilon>\nnext line")).To(Succeed())
		out, texts := result(d)
		Expect(texts[1]).To(ContainSubstring("Delta & <epsilon>\nnext line"))
		Expect(texts[1]).NotTo(ContainSubstring("Alpha"), "old runs remain")
		Expect(texts[1]).NotTo(ContainSubstring("beta"), "old runs remain")
		Expect(texts[1]).To(ContainSubstring("Gamma"), "other paragraph lost")
		_, parts := zipParts(out)
		x := string(parts["ppt/slides/slide2.xml"])
		Expect(x).To(ContainSubstring(`<a:pPr marL="342900" indent="-342900"/><a:r><a:rPr lang="en-US" b="1"/><a:t>Delta &amp; &lt;epsilon&gt;</a:t></a:r><a:br/>`), "formatting of the first run not kept")
		Expect(x).NotTo(ContainSubstring(`i="1"`), "the properties of the dropped run remain")
		assertSameParts(src, out, map[string]bool{"ppt/slides/slide2.xml": true})

		// The second paragraph, and an emptied one.
		Expect(d.SetParagraph(2, "Body 2", 1, "")).To(Succeed())
		_, texts = result(d)
		Expect(texts[1]).NotTo(ContainSubstring("Gamma"))
	})

	It("adds slides from a layout", func() {
		d, src := openSource()
		Expect(d.AddSlide("Title and Content", "Next steps & risks", []string{"Ship it", "Watch <errors>", "Rest"})).To(Succeed())
		out, texts := result(d)
		Expect(texts).To(HaveLen(4))
		for _, want := range []string{"Next steps & risks", "Ship it", "Watch <errors>", "Rest"} {
			Expect(texts[3]).To(ContainSubstring(want), "new slide lacks %q", want)
		}
		re, err := Open(out)
		Expect(err).NotTo(HaveOccurred())
		s := re.Slides()[3]
		Expect(s.Index).To(Equal(4))
		Expect(s.Title).To(Equal("Next steps & risks"))
		Expect(s.Text).To(HaveLen(4))
		names, parts := zipParts(out)
		Expect(names[len(names)-1]).To(Equal("ppt/slides/_rels/slide4.xml.rels"), "new parts are not at the end")
		Expect(names[len(names)-2]).To(Equal("ppt/slides/slide4.xml"), "new parts are not at the end")
		Expect(string(parts["docProps/app.xml"])).To(ContainSubstring("<Slides>4</Slides>"), "slide count in app.xml not updated")
		assertSameParts(src, out, map[string]bool{
			"[Content_Types].xml":             true,
			"ppt/presentation.xml":            true,
			"ppt/_rels/presentation.xml.rels": true,
			"docProps/app.xml":                true,
		})

		// An empty bullet list gives an empty body; the title may be empty.
		Expect(d.AddSlide("Title and Content", "", nil)).To(Succeed())
		_, texts = result(d)
		Expect(texts).To(HaveLen(5))
	})

	It("deletes a slide with its notes and keeps ids unique", func() {
		d, _ := openSource()
		// Slide 1 has notes: the notes page must go with it.
		Expect(d.DeleteSlide(1)).To(Succeed())
		out, texts := result(d)
		Expect(texts).To(HaveLen(2))
		all := strings.Join(texts, "\n")
		for _, gone := range []string{"Deck title", "Opening", "Say hello first."} {
			Expect(all).NotTo(ContainSubstring(gone), "deleted slide text remains")
		}
		Expect(texts[0]).To(ContainSubstring("Findings"), "slide 2 did not move up")
		names, parts := zipParts(out)
		for _, n := range names {
			Expect(n == "ppt/slides/slide1.xml" || n == "ppt/slides/_rels/slide1.xml.rels" || strings.Contains(n, "notesSlide")).To(BeFalse(), "part %s should be gone", n)
		}
		for n, b := range parts {
			if n != "[Content_Types].xml" && !strings.Contains(n, "notesMaster") {
				Expect(string(b)).NotTo(ContainSubstring("slide1.xml"), "%s still mentions slide1.xml", n)
			}
		}
		Expect(string(parts["docProps/app.xml"])).To(ContainSubstring("<Slides>2</Slides>"), "slide count in app.xml not updated")
		Expect(string(parts["[Content_Types].xml"])).NotTo(ContainSubstring("slide1.xml"), "content type overrides remain")
		Expect(string(parts["[Content_Types].xml"])).NotTo(ContainSubstring("notesSlide1"), "content type overrides remain")
		// Layout, master and theme stay.
		for _, keep := range []string{"ppt/slideLayouts/slideLayout1.xml", "ppt/slideMasters/slideMaster1.xml", "ppt/theme/theme1.xml"} {
			Expect(parts).To(HaveKey(keep), "%s was removed", keep)
		}

		// Delete a middle slide of a bigger deck, then add again: ids stay unique.
		Expect(d.AddSlide("Title and Content", "Again", []string{"x"})).To(Succeed())
		Expect(d.DeleteSlide(1)).To(Succeed())
		Expect(d.AddSlide("Title and Content", "Once more", nil)).To(Succeed())
		_, texts = result(d)
		Expect(texts).To(HaveLen(3))
		Expect(texts[1]).To(ContainSubstring("Again"))
		Expect(texts[2]).To(ContainSubstring("Once more"))
		out, _ = result(d)
		_, parts = zipParts(out)
		pres := string(parts["ppt/presentation.xml"])
		Expect(strings.Count(pres, "<p:sldId ")).To(Equal(3), "sldIdLst: %s", pres)
	})

	It("drops the images owned by a deleted slide", func() {
		src := sourceDeck()
		src = rewriteZip(src, map[string]func(string) string{
			"ppt/slides/_rels/slide3.xml.rels": func(s string) string {
				return strings.Replace(s, "</Relationships>", `<Relationship Id="rId9" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="../media/image9.png"/></Relationships>`, 1)
			},
		})
		// Add the media part with one more rewrite pass that appends it.
		var buf bytes.Buffer
		zr, _ := zip.NewReader(bytes.NewReader(src), int64(len(src)))
		zw := zip.NewWriter(&buf)
		for _, f := range zr.File {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			w, _ := zw.Create(f.Name)
			w.Write(b)
		}
		w, _ := zw.Create("ppt/media/image9.png")
		w.Write([]byte("png"))
		zw.Close()
		d, err := Open(buf.Bytes())
		Expect(err).NotTo(HaveOccurred())
		Expect(d.DeleteSlide(3)).To(Succeed())
		out, _ := result(d)
		names, _ := zipParts(out)
		for _, n := range names {
			Expect(n).NotTo(ContainSubstring("image9"), "image used only by the deleted slide remains")
		}
	})

	It("rejects bad edits and keeps the last slide", func() {
		d, src := openSource()
		tests := []struct {
			name string
			do   func() error
		}{
			{"unknown shape", func() error { return d.SetParagraph(2, "Nope", 0, "x") }},
			{"paragraph out of range", func() error { return d.SetParagraph(2, "Body 2", 2, "x") }},
			{"negative paragraph", func() error { return d.SetParagraph(2, "Body 2", -1, "x") }},
			{"slide out of range (SetParagraph)", func() error { return d.SetParagraph(9, "Body 2", 0, "x") }},
			{"slide zero (SetParagraph)", func() error { return d.SetParagraph(0, "Body 2", 0, "x") }},
			{"slide out of range (SetTitle)", func() error { return d.SetTitle(4, "x") }},
			{"no title placeholder", func() error { return d.SetTitle(3, "x") }},
			{"unknown layout", func() error { return d.AddSlide("No such layout", "t", nil) }},
			{"slide out of range (DeleteSlide)", func() error { return d.DeleteSlide(4) }},
			{"slide negative (DeleteSlide)", func() error { return d.DeleteSlide(-1) }},
		}
		for _, tc := range tests {
			Expect(tc.do()).To(HaveOccurred(), tc.name)
		}
		// None of the failed edits changed the deck.
		out, err := d.Bytes()
		Expect(err).NotTo(HaveOccurred())
		assertSameParts(src, out, nil)

		// The last slide stays.
		Expect(d.DeleteSlide(1)).To(Succeed())
		Expect(d.DeleteSlide(1)).To(Succeed())
		Expect(d.DeleteSlide(1)).To(HaveOccurred(), "deleting the last slide must fail")
		Expect(d.Slides()).To(HaveLen(1))
		result(d)
	})

	It("needs placeholders in the layout to add a title or bullets", func() {
		// The writer's own layout is blank: no title and no body.
		var buf bytes.Buffer
		Expect(pptx.Write(&buf, []deck.StudioSlide{{ID: "a", Title: "A", Layout: deck.LayoutText, Status: deck.SlideOK}}, pptx.Options{}, nil)).To(Succeed())
		d, err := Open(buf.Bytes())
		Expect(err).NotTo(HaveOccurred())
		Expect(d.AddSlide("Blank", "title", nil)).To(HaveOccurred(), "a title for a layout without a title placeholder must fail")
		Expect(d.AddSlide("Blank", "", []string{"x"})).To(HaveOccurred(), "bullets for a layout without a body placeholder must fail")
		Expect(d.AddSlide("Blank", "", nil)).To(Succeed())
		Expect(d.Slides()).To(HaveLen(2), "blank slide not added")
		result(d)
	})

	It("rejects input that is not a presentation", func() {
		var plain bytes.Buffer
		zw := zip.NewWriter(&plain)
		w, _ := zw.Create("a.txt")
		w.Write([]byte("hello"))
		zw.Close()

		var traversal bytes.Buffer
		zw = zip.NewWriter(&traversal)
		w, _ = zw.Create("../evil.xml")
		w.Write([]byte("<a/>"))
		zw.Close()

		src := sourceDeck()
		broken := rewriteZip(src, map[string]func(string) string{
			"ppt/presentation.xml": func(s string) string { return s[:len(s)/2] },
		})
		noSlides := rewriteZip(src, map[string]func(string) string{
			"ppt/_rels/presentation.xml.rels": func(s string) string {
				return strings.ReplaceAll(s, `Target="slides/slide2.xml"`, `Target="slides/missing.xml"`)
			},
		})
		for name, data := range map[string][]byte{
			"nil":          nil,
			"text":         []byte("not a deck"),
			"plain zip":    plain.Bytes(),
			"traversal":    traversal.Bytes(),
			"broken xml":   broken,
			"missing part": noSlides,
			"truncated":    src[:len(src)/2],
		} {
			_, err := Open(data)
			Expect(err).To(HaveOccurred(), "%s: Open succeeded", name)
		}
	})

	It("is not fooled by an xlsx", func() {
		// A zip with content types and a main part that is not a presentation.
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, body := range map[string]string{
			"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
			"_rels/.rels":         `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
			"xl/workbook.xml":     `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"/>`,
		} {
			w, _ := zw.Create(name)
			w.Write([]byte(body))
		}
		zw.Close()
		_, err := Open(buf.Bytes())
		Expect(err).To(HaveOccurred(), "an xlsx must not open")
	})

	// Cut each XML part in half and run every operation: each must return,
	// with an error or not, and never panic.
	It("never panics on malformed parts", func() {
		src := sourceDeck()
		names, _ := zipParts(src)
		for _, name := range names {
			if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".rels") {
				continue
			}
			for _, cut := range []func(string) string{
				func(s string) string { return s[:len(s)/2] },
				func(s string) string { return "" },
				func(s string) string { return strings.ReplaceAll(s, "<", "<<") },
			} {
				data := rewriteZip(src, map[string]func(string) string{name: cut})
				d, err := Open(data)
				if err != nil {
					continue
				}
				_ = d.Slides()
				_ = d.SetTitle(1, "x")
				_ = d.SetParagraph(2, "Body 2", 0, "x")
				_ = d.AddSlide("Title and Content", "t", []string{"b"})
				_ = d.DeleteSlide(1)
				_, _ = d.Bytes()
			}
		}
	})
})
