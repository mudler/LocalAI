package pptxedit

import (
	"archive/zip"
	"bytes"
	"io"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// withoutSlides drops every slide and notes page of a deck, as a template
// saved without slides comes. asPotx declares the main part as a template.
func withoutSlides(data []byte, asPotx bool) []byte {
	GinkgoHelper()
	sldIDs := regexp.MustCompile(`<p:sldIdLst>.*?</p:sldIdLst>`)
	slideRel := regexp.MustCompile(`<Relationship [^>]*Type="[^"]*/slide"[^>]*/>`)
	slideCT := regexp.MustCompile(`<Override PartName="/ppt/(slides|notesSlides)/[^"]*"[^>]*/>`)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	Expect(err).NotTo(HaveOccurred())
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "ppt/slides/") || strings.HasPrefix(f.Name, "ppt/notesSlides/") {
			continue
		}
		rc, err := f.Open()
		Expect(err).NotTo(HaveOccurred())
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		Expect(err).NotTo(HaveOccurred())
		s := string(b)
		switch f.Name {
		case "ppt/presentation.xml":
			s = sldIDs.ReplaceAllString(s, "")
		case "ppt/_rels/presentation.xml.rels":
			s = slideRel.ReplaceAllString(s, "")
		case contentTypes:
			s = slideCT.ReplaceAllString(s, "")
			if asPotx {
				s = strings.ReplaceAll(s, ctPresMain, ctTemplateMain)
			}
		}
		w, err := zw.Create(f.Name)
		Expect(err).NotTo(HaveOccurred())
		_, _ = w.Write([]byte(s))
	}
	Expect(zw.Close()).To(Succeed())
	return out.Bytes()
}

var _ = Describe("templates", func() {
	It("opens a template without slides and builds a deck on it", func() {
		tmpl := withoutSlides(sourceDeck(), true)
		_, err := Open(tmpl)
		Expect(err).To(HaveOccurred(), "Open must keep refusing a deck without slides")

		d, err := OpenTemplate(tmpl)
		Expect(err).NotTo(HaveOccurred(), "OpenTemplate")
		Expect(d.SlideCount()).To(Equal(0))
		Expect(d.AddSlide("Title and Content", "First", []string{"One"})).To(Succeed())
		d.SetDocTitle(`Plan & "more"`)
		out, texts := result(d)
		Expect(texts).To(HaveLen(1))
		Expect(texts[0]).To(ContainSubstring("One"))
		_, parts := zipParts(out)
		Expect(string(parts[contentTypes])).To(ContainSubstring(ctPresMain))
		Expect(string(parts[contentTypes])).NotTo(ContainSubstring(ctTemplateMain))
		Expect(string(parts["docProps/core.xml"])).To(ContainSubstring("<dc:title>Plan &amp; &#34;more&#34;</dc:title>"))
		re, err := Open(out)
		Expect(err).NotTo(HaveOccurred(), "the result opens as a deck")
		Expect(re.SlideCount()).To(Equal(1))
	})

	It("finds layouts by type, whatever their name", func() {
		d, _ := openSource()
		name, ok := d.LayoutOfType("obj", "tx")
		Expect(ok).To(BeTrue())
		Expect(name).To(Equal("Title and Content"))
		_, ok = d.LayoutOfType("title")
		Expect(ok).To(BeFalse(), "the source deck has no title layout")

		// A German template: the names differ, the types do not.
		src := rewriteZip(sourceDeck(), map[string]func(string) string{
			"ppt/slideLayouts/slideLayout1.xml": func(s string) string {
				s = strings.Replace(s, `type="blank"`, `type="title"`, 1)
				return regexp.MustCompile(`<p:cSld name="[^"]*"`).ReplaceAllString(s, `<p:cSld name="Titelfolie"`)
			},
			"ppt/slideLayouts/slideLayout2.xml": func(s string) string {
				return strings.Replace(s, `name="Title and Content"`, `name="Titel und Inhalt"`, 1)
			},
		})
		d, err := Open(src)
		Expect(err).NotTo(HaveOccurred())
		name, ok = d.LayoutOfType("title")
		Expect(ok).To(BeTrue())
		Expect(name).To(Equal("Titelfolie"))
		name, ok = d.LayoutOfType("obj", "tx")
		Expect(ok).To(BeTrue())
		Expect(name).To(Equal("Titel und Inhalt"))
		Expect(d.AddSlide(name, "Ziele", []string{"Eins"})).To(Succeed())
		_, texts := result(d)
		Expect(texts[len(texts)-1]).To(ContainSubstring("Eins"))
	})
})
