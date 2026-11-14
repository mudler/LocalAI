// SPDX-License-Identifier: MIT
package tools_test

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	"github.com/mudler/LocalAI/addons/office/internal/deck/pptx"
	"github.com/mudler/LocalAI/addons/office/internal/officecheck"
	"github.com/mudler/LocalAI/addons/office/internal/tools"
)

// zipEntries reads every part of a zip file.
func zipEntries(data []byte) map[string]string {
	GinkgoHelper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	Expect(err).NotTo(HaveOccurred())
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		Expect(err).NotTo(HaveOccurred())
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		Expect(err).NotTo(HaveOccurred())
		out[f.Name] = string(b)
	}
	return out
}

// rewrite returns data with the named parts changed by fn, in the same order.
func rewrite(data []byte, fns map[string]func(string) string) []byte {
	GinkgoHelper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	Expect(err).NotTo(HaveOccurred())
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		rc, err := f.Open()
		Expect(err).NotTo(HaveOccurred())
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		Expect(err).NotTo(HaveOccurred())
		if fn := fns[f.Name]; fn != nil {
			b = []byte(fn(string(b)))
		}
		w, err := zw.Create(f.Name)
		Expect(err).NotTo(HaveOccurred())
		_, _ = w.Write(b)
	}
	Expect(zw.Close()).To(Succeed())
	return out.Bytes()
}

const (
	brandFont   = "Montserrat"
	brandAccent = "0E5A63"
	builtinFont = "Bricolage Grotesque"
	builtinTint = "FF6B3D"
	ctPres      = "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"
	ctTmpl      = "application/vnd.openxmlformats-officedocument.presentationml.template.main+xml"
)

// brandTemplate is a deck with its own theme font and accent color, a
// 4:3 slide size and two slides of its own, as a company template has.
// asPotx declares its main part as a template.
func brandTemplate(asPotx bool) []byte {
	GinkgoHelper()
	slides := []deck.StudioSlide{
		{ID: "s1", Title: "Template cover", Layout: deck.LayoutTitle, Status: deck.SlideOK},
		{ID: "s2", Title: "Template sample", Layout: deck.LayoutText, Status: deck.SlideOK, Bullets: []deck.StudioBullet{{Text: "Sample bullet"}}},
	}
	var buf bytes.Buffer
	Expect(pptx.Write(&buf, slides, pptx.Options{Title: "Brand template", Style: deck.StylePaper}, nil)).To(Succeed())
	fns := map[string]func(string) string{
		"ppt/theme/theme1.xml": func(s string) string {
			return strings.ReplaceAll(strings.ReplaceAll(s, builtinFont, brandFont), builtinTint, brandAccent)
		},
		"ppt/presentation.xml": func(s string) string {
			i := strings.Index(s, "<p:sldSz ")
			j := i + strings.Index(s[i:], "/>") + 2
			return s[:i] + `<p:sldSz cx="9144000" cy="6858000"/>` + s[j:]
		},
	}
	if asPotx {
		fns["[Content_Types].xml"] = func(s string) string { return strings.ReplaceAll(s, ctPres, ctTmpl) }
	}
	return rewrite(buf.Bytes(), fns)
}

var _ = Describe("create-pptx with a template", func() {
	slides := []map[string]any{
		{"title": "Plan 2027"},
		{"title": "Goals", "bullets": []string{"Ship it", "Measure it"}},
	}

	for _, kind := range []string{".pptx", ".potx"} {
		It("uses the design of a "+kind+" template and only the slides asked for", func() {
			h := newHost()
			tmpl := brandTemplate(kind == ".potx")
			h.put("/templates/brand"+kind, tmpl)
			o, raw := call(h, tools.ToolCreatePptx, map[string]any{
				"path": "/plan.pptx", "title": "Plan", "template": "/templates/brand" + kind, "slides": slides,
			})
			Expect(o.OK).To(BeTrue(), "create-pptx = %s", raw)
			out := h.files["/plan.pptx"]
			Expect(officecheck.Check(out)).To(BeEmpty(), "the deck has problems")

			parts := zipEntries(out)
			theme := parts["ppt/theme/theme1.xml"]
			Expect(theme).To(ContainSubstring(`<a:latin typeface="`+brandFont+`"/>`), "theme font is not the template's")
			Expect(theme).To(ContainSubstring(`<a:srgbClr val="`+brandAccent+`"/>`), "theme color is not the template's")
			Expect(theme).NotTo(ContainSubstring(builtinFont), "the built-in theme leaked in")
			Expect(parts["ppt/presentation.xml"]).To(ContainSubstring(`<p:sldSz cx="9144000" cy="6858000"/>`), "slide size is not the template's")
			Expect(parts["[Content_Types].xml"]).To(ContainSubstring(ctPres), "the main part is not a presentation")
			Expect(parts["[Content_Types].xml"]).NotTo(ContainSubstring(ctTmpl), "the main part is still a template")
			Expect(parts["docProps/core.xml"]).To(ContainSubstring("<dc:title>Plan</dc:title>"), "deck title")

			_, read := call(h, tools.ToolReadPptx, map[string]any{"path": "/plan.pptx"})
			for _, want := range []string{"Plan 2027", "Goals", "Ship it", "Measure it"} {
				Expect(read).To(ContainSubstring(want), "deck lacks %q: %s", want, read)
			}
			for _, gone := range []string{"Template cover", "Template sample", "Sample bullet"} {
				Expect(read).NotTo(ContainSubstring(gone), "a template slide was kept: %s", read)
			}
			Expect(read).To(ContainSubstring(`"total":2`), "slide count: %s", read)
			Expect(h.files["/templates/brand"+kind]).To(Equal(tmpl), "the template was changed")
		})
	}

	It("is unchanged without a template", func() {
		h := newHost()
		o, raw := call(h, tools.ToolCreatePptx, map[string]any{"path": "/plain.pptx", "title": "Plain", "slides": slides})
		Expect(o.OK).To(BeTrue(), "create-pptx = %s", raw)
		want := []deck.StudioSlide{
			{ID: "s1", Title: "Plan 2027", Layout: deck.LayoutTitle, Bullets: []deck.StudioBullet{}, Sources: []string{}, Status: deck.SlideOK},
			{ID: "s2", Title: "Goals", Layout: deck.LayoutText, Bullets: []deck.StudioBullet{{Text: "Ship it", Evidence: []string{}}, {Text: "Measure it", Evidence: []string{}}}, Sources: []string{}, Status: deck.SlideOK},
		}
		var buf bytes.Buffer
		Expect(pptx.Write(&buf, want, pptx.Options{Title: "Plain", Style: deck.StylePaper}, nil)).To(Succeed())
		Expect(h.files["/plain.pptx"]).To(Equal(buf.Bytes()), "the deck without a template differs from the built-in writer")
		Expect(zipEntries(h.files["/plain.pptx"])["ppt/theme/theme1.xml"]).To(ContainSubstring(builtinFont))
	})

	It("refuses a template it must not or cannot use, and saves nothing", func() {
		h := newHost()
		h.put("/templates/brand.pptx", brandTemplate(false))
		h.put("/notes.docx", docxBytes("x"))
		h.put("/fake.pptx", []byte("not a zip"))
		noContent := rewrite(brandTemplate(false), map[string]func(string) string{
			"ppt/slideLayouts/slideLayout2.xml": func(s string) string { return strings.Replace(s, `type="obj"`, `type="blank"`, 1) },
		})
		h.put("/templates/bare.pptx", noContent)
		for name, tmpl := range map[string]string{
			"escaping path":        "../brand.pptx",
			"escaping deep path":   "/templates/../../brand.pptx",
			"wrong extension":      "/notes.docx",
			"missing file":         "/templates/absent.potx",
			"not a deck":           "/fake.pptx",
			"no content layout":    "/templates/bare.pptx",
			"too long":             "/" + strings.Repeat("a", 600) + ".pptx",
			"nul byte in the path": "/templates/br\x00and.pptx",
		} {
			o, raw := call(h, tools.ToolCreatePptx, map[string]any{"path": "/out.pptx", "template": tmpl, "slides": slides})
			Expect(o.OK).To(BeFalse(), "%s: answered %s, want a refusal", name, raw)
			Expect(o.Error).NotTo(BeEmpty(), "%s: answered %s, want a refusal", name, raw)
			Expect(h.files).NotTo(HaveKey("/out.pptx"), "%s: a deck was saved", name)
		}
		// The control: the same call with the good template is saved.
		o, raw := call(h, tools.ToolCreatePptx, map[string]any{"path": "/out.pptx", "template": "/templates/brand.pptx", "slides": slides})
		Expect(o.OK).To(BeTrue(), "the good template was refused: %s", raw)
	})
})
