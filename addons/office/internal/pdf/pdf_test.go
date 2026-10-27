package pdf

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// sampleSpec has two pages and every block kind.
func sampleSpec() Spec {
	return Spec{
		Title:  "Quarterly review",
		Author: "Test",
		Pages: []Page{
			{Blocks: []Block{
				Heading{Level: 1, Text: "Quarterly report"},
				Paragraph{Text: "The team shipped the release on time and closed the open reviews."},
				Heading{Level: 2, Text: "Highlights"},
				Bullets{Items: []string{"First highlight", "Second highlight"}},
				Heading{Level: 3, Text: "Figures"},
				Table{
					Header: []string{"Item", "Count"},
					Rows:   [][]string{{"Revenue", "42"}, {"Costs", "7"}},
				},
			}},
			{Blocks: []Block{
				Heading{Level: 1, Text: "Appendix"},
				Paragraph{Text: "Second page marker."},
			}},
		},
	}
}

// mustCreate creates a PDF or fails the test.
func mustCreate(t *testing.T, spec Spec) []byte {
	t.Helper()
	out, err := Create(spec)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return out
}

// onePageSpec returns a spec of n pages, each with one short paragraph.
func onePageSpec(n int, text string) Spec {
	s := Spec{Title: "t"}
	for i := 0; i < n; i++ {
		s.Pages = append(s.Pages, Page{Blocks: []Block{Paragraph{Text: fmt.Sprintf("%s %d", text, i+1)}}})
	}
	return s
}

// pageContent returns the decoded content stream of one page.
func pageContent(t *testing.T, data []byte, page int) string {
	t.Helper()
	ctx, err := api.ReadValidateAndOptimize(bytes.NewReader(data), model.NewDefaultConfiguration())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	r, err := pdfcpu.ExtractPageContent(ctx, page)
	if err != nil {
		t.Fatalf("content of page %d: %v", page, err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	return string(b)
}

// rotationOf returns the /Rotate value of one page, 0 when it is not set.
func rotationOf(t *testing.T, data []byte, page int) int {
	t.Helper()
	ctx, err := api.ReadValidateAndOptimize(bytes.NewReader(data), model.NewDefaultConfiguration())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	d, _, _, err := ctx.PageDict(page, false)
	if err != nil {
		t.Fatalf("page %d dict: %v", page, err)
	}
	if r := d.IntEntry("Rotate"); r != nil {
		return *r
	}
	return 0
}

// formPDF builds a one-page PDF with one AcroForm text field per name. It is
// written by hand so the test does not depend on a fixture file.
func formPDF(names ...string) []byte {
	var fields, annots string
	for i := range names {
		fields += fmt.Sprintf("%d 0 R ", 5+i)
		annots += fmt.Sprintf("%d 0 R ", 5+i)
	}
	objs := []string{
		fmt.Sprintf("<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [%s] /DA (/Helv 0 Tf 0 g) /DR << /Font << /Helv 4 0 R >> >> >> >>", fields),
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Resources << /Font << /Helv 4 0 R >> >> /Annots [%s] >>", annots),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for i, name := range names {
		y := 150 - 30*i
		objs = append(objs, fmt.Sprintf("<< /Type /Annot /Subtype /Widget /FT /Tx /T (%s) /Rect [20 %d 200 %d] /F 4 /DA (/Helv 12 Tf 0 g) /P 3 0 R >>", name, y, y+20))
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

func TestCreateBlocks(t *testing.T) {
	data := mustCreate(t, sampleSpec())
	if err := Check(data); err != nil {
		t.Fatalf("Check: %v", err)
	}
	n, err := Pages(data)
	if err != nil || n != 2 {
		t.Fatalf("Pages = %d, %v; want 2", n, err)
	}
	first := pageContent(t, data, 1)
	for _, want := range []string{"Quarterly report", "Highlights", "First highlight", "Revenue"} {
		if !strings.Contains(first, want) {
			t.Errorf("page 1 content does not contain %q", want)
		}
	}
	if !strings.Contains(pageContent(t, data, 2), "Second page marker") {
		t.Errorf("page 2 content does not contain its paragraph")
	}
}

func TestCreateEachSpecPageStartsAPage(t *testing.T) {
	data := mustCreate(t, onePageSpec(3, "short"))
	if n, err := Pages(data); err != nil || n != 3 {
		t.Fatalf("Pages = %d, %v; want 3", n, err)
	}
}

func TestCreateWrapsLongTextAcrossPages(t *testing.T) {
	long := strings.Repeat("lorem ipsum dolor sit amet ", 800)
	data := mustCreate(t, Spec{Pages: []Page{{Blocks: []Block{Paragraph{Text: long}}}}})
	n, err := Pages(data)
	if err != nil || n < 2 {
		t.Fatalf("Pages = %d, %v; want more than one page", n, err)
	}
	if err := Check(data); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCreateTableRowsBreakAcrossPages(t *testing.T) {
	var rows [][]string
	for i := 0; i < 200; i++ {
		rows = append(rows, []string{fmt.Sprintf("row %d", i), "value"})
	}
	data := mustCreate(t, Spec{Pages: []Page{{Blocks: []Block{
		Table{Header: []string{"Name", "Value"}, Rows: rows},
	}}}})
	if n, err := Pages(data); err != nil || n < 2 {
		t.Fatalf("Pages = %d, %v; want more than one page", n, err)
	}
	if err := Check(data); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCreateLatin1Accepted(t *testing.T) {
	data := mustCreate(t, Spec{Pages: []Page{{Blocks: []Block{
		Paragraph{Text: "Caffè, città, für Ön: ¡olé! Ünïcödé"},
	}}}})
	if err := Check(data); err != nil {
		t.Fatalf("Check: %v", err)
	}
	// The built-in font takes single bytes (cp1252), not UTF-8.
	c := pageContent(t, data, 1)
	if !strings.Contains(c, "Caff\xe8") {
		t.Errorf("content does not hold the single byte of è")
	}
	if strings.Contains(c, "Caff\xc3\xa8") {
		t.Errorf("content holds UTF-8 bytes for è")
	}
}

func TestCreateBulletGlyphIsCP1252(t *testing.T) {
	data := mustCreate(t, Spec{Pages: []Page{{Blocks: []Block{Bullets{Items: []string{"alpha"}}}}}})
	if !strings.Contains(pageContent(t, data, 1), "(\x95)Tj") {
		t.Errorf("bullet is not the cp1252 bullet byte")
	}
}

func TestCreateControlCharacterIsError(t *testing.T) {
	for _, s := range []string{"bell\a", "c1 \u0085 control"} {
		spec := Spec{Pages: []Page{{Blocks: []Block{Paragraph{Text: s}}}}}
		if _, err := Create(spec); err == nil {
			t.Errorf("%q: Create succeeded, want an error", s)
		}
	}
}

func TestCreateNonLatin1WithoutFontIsError(t *testing.T) {
	spec := Spec{Pages: []Page{{Blocks: []Block{Paragraph{Text: "price 5 €"}}}}}
	_, err := Create(spec)
	if err == nil || !strings.Contains(err.Error(), "U+20AC") {
		t.Fatalf("Create = %v; want an error naming U+20AC", err)
	}
}

func TestCreateInvalidFontIsError(t *testing.T) {
	spec := Spec{FontTTF: []byte("not a font"), Pages: []Page{{Blocks: []Block{Paragraph{Text: "x"}}}}}
	if _, err := Create(spec); err == nil {
		t.Fatal("Create with a broken font: want an error")
	}
}

func TestCreateFontNotDecodedAsText(t *testing.T) {
	// A file that is not a font is refused before fpdf sees it.
	spec := Spec{FontTTF: []byte("%PDF-1.4 not a font at all"), Pages: []Page{{Blocks: []Block{Paragraph{Text: "x"}}}}}
	if _, err := Create(spec); err == nil || !strings.Contains(err.Error(), "TrueType") {
		t.Fatalf("Create = %v; want a font type error", err)
	}
}

// TestCreateWithUnicodeFont runs only when OFFICE_PDF_TEST_TTF names a TrueType
// font file. The module ships no font.
func TestCreateWithUnicodeFont(t *testing.T) {
	path := os.Getenv("OFFICE_PDF_TEST_TTF")
	if path == "" {
		t.Skip("set OFFICE_PDF_TEST_TTF to a TrueType font to run this test") //nolint:forbidigo // plain testing.T test; this package has no Ginkgo suite
	}
	ttf, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read font: %v", err)
	}
	data := mustCreate(t, Spec{FontTTF: ttf, Pages: []Page{{Blocks: []Block{
		Heading{Level: 1, Text: "中文 and αβγ"},
		Paragraph{Text: "Any text is allowed with a Unicode font."},
	}}}})
	if n, err := Pages(data); err != nil || n != 1 {
		t.Fatalf("Pages = %d, %v; want 1", n, err)
	}
}

func TestCreateValidationErrors(t *testing.T) {
	big := make([]Page, MaxPages+1)
	for i := range big {
		big[i] = Page{}
	}
	manyBlocks := make([]Block, MaxBlocks+1)
	for i := range manyBlocks {
		manyBlocks[i] = Paragraph{Text: "x"}
	}
	cases := map[string]Spec{
		"no pages":          {},
		"heading level 4":   {Pages: []Page{{Blocks: []Block{Heading{Level: 4, Text: "x"}}}}},
		"heading level 0":   {Pages: []Page{{Blocks: []Block{Heading{Level: 0, Text: "x"}}}}},
		"empty heading":     {Pages: []Page{{Blocks: []Block{Heading{Level: 1, Text: " "}}}}},
		"empty bullets":     {Pages: []Page{{Blocks: []Block{Bullets{}}}}},
		"empty bullet item": {Pages: []Page{{Blocks: []Block{Bullets{Items: []string{"a", ""}}}}}},
		"table no header":   {Pages: []Page{{Blocks: []Block{Table{}}}}},
		"table ragged row":  {Pages: []Page{{Blocks: []Block{Table{Header: []string{"a", "b"}, Rows: [][]string{{"x"}}}}}}},
		"nil block":         {Pages: []Page{{Blocks: []Block{nil}}}},
		"too many pages":    {Pages: big},
		"too many blocks":   {Pages: []Page{{Blocks: manyBlocks}}},
	}
	for name, spec := range cases {
		if _, err := Create(spec); err == nil {
			t.Errorf("%s: Create succeeded, want an error", name)
		}
	}
}

func TestMergeCountsPages(t *testing.T) {
	a := mustCreate(t, onePageSpec(2, "a"))
	b := mustCreate(t, onePageSpec(3, "b"))
	m, err := Merge(a, b)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if n, err := Pages(m); err != nil || n != 5 {
		t.Fatalf("Pages = %d, %v; want 5", n, err)
	}
	if err := Check(m); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !strings.Contains(pageContent(t, m, 3), "b 1") {
		t.Errorf("merged page 3 is not the first page of the second part")
	}
}

func TestSplitOnePagePerPart(t *testing.T) {
	data := mustCreate(t, onePageSpec(3, "part"))
	parts, err := Split(data)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("Split returned %d parts; want 3", len(parts))
	}
	for i, p := range parts {
		if n, err := Pages(p); err != nil || n != 1 {
			t.Errorf("part %d: Pages = %d, %v; want 1", i+1, n, err)
		}
		if err := Check(p); err != nil {
			t.Errorf("part %d: Check: %v", i+1, err)
		}
	}
	if !strings.Contains(pageContent(t, parts[1], 1), "part 2") {
		t.Errorf("part 2 does not hold page 2")
	}
}

func TestRotateChangesOnlySelectedPages(t *testing.T) {
	data := mustCreate(t, onePageSpec(2, "r"))
	out, err := Rotate(data, []int{1}, 90)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if got := rotationOf(t, out, 1); got != 90 {
		t.Errorf("page 1 rotation = %d; want 90", got)
	}
	if got := rotationOf(t, out, 2); got != 0 {
		t.Errorf("page 2 rotation = %d; want 0", got)
	}
	all, err := Rotate(data, nil, 270)
	if err != nil {
		t.Fatalf("Rotate all: %v", err)
	}
	for p := 1; p <= 2; p++ {
		if got := rotationOf(t, all, p); got != 270 {
			t.Errorf("page %d rotation = %d; want 270", p, got)
		}
	}
}

func TestRotateErrors(t *testing.T) {
	data := mustCreate(t, onePageSpec(2, "r"))
	cases := map[string]struct {
		pages   []int
		degrees int
	}{
		"degrees 45":    {nil, 45},
		"degrees 0":     {nil, 0},
		"degrees -90":   {nil, -90},
		"page 0":        {[]int{0}, 90},
		"page past end": {[]int{3}, 90},
		"page negative": {[]int{-1}, 90},
	}
	for name, c := range cases {
		if _, err := Rotate(data, c.pages, c.degrees); err == nil {
			t.Errorf("%s: Rotate succeeded, want an error", name)
		}
	}
}

func TestWatermarkStampsEveryPage(t *testing.T) {
	data := mustCreate(t, onePageSpec(2, "w"))
	out, err := Watermark(data, "DRAFT")
	if err != nil {
		t.Fatalf("Watermark: %v", err)
	}
	if err := Check(out); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(out) <= len(data) {
		t.Errorf("watermarked output is %d bytes, input is %d; want larger", len(out), len(data))
	}
}

func TestWatermarkErrors(t *testing.T) {
	data := mustCreate(t, onePageSpec(1, "w"))
	if _, err := Watermark(data, ""); err == nil {
		t.Error("empty text: want an error")
	}
	if _, err := Watermark(data, "€"); err == nil {
		t.Error("non-Latin-1 text: want an error")
	}
}

func TestFieldsAndFillFields(t *testing.T) {
	form := formPDF("customer", "notes")
	if err := Check(form); err != nil {
		t.Fatalf("fixture Check: %v", err)
	}
	fields, err := Fields(form)
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if len(fields) != 2 || fields[0].Name != "customer" || fields[1].Name != "notes" {
		t.Fatalf("Fields = %+v; want customer and notes", fields)
	}

	out, err := FillFields(form, map[string]string{"customer": "Ada Lovelace"})
	if err != nil {
		t.Fatalf("FillFields: %v", err)
	}
	if err := Check(out); err != nil {
		t.Fatalf("Check after fill: %v", err)
	}
	filled, err := Fields(out)
	if err != nil {
		t.Fatalf("Fields after fill: %v", err)
	}
	for _, f := range filled {
		if f.Name == "customer" && f.Value != "Ada Lovelace" {
			t.Errorf("customer = %q; want Ada Lovelace", f.Value)
		}
		if f.Name == "notes" && f.Value != "" {
			t.Errorf("notes = %q; want it untouched", f.Value)
		}
	}
}

func TestFillFieldsErrors(t *testing.T) {
	form := formPDF("customer")
	if _, err := FillFields(form, map[string]string{"unknown": "x"}); err == nil {
		t.Error("unknown field name: want an error")
	}
	if _, err := FillFields(form, nil); err == nil {
		t.Error("no values: want an error")
	}
}

func TestFieldsOnPDFWithoutForm(t *testing.T) {
	fields, err := Fields(mustCreate(t, onePageSpec(1, "f")))
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("Fields = %+v; want none", fields)
	}
}

func TestNotPDFInputIsError(t *testing.T) {
	junk := []byte("this is not a pdf file")
	checks := map[string]func() error{
		"Check":      func() error { return Check(junk) },
		"Pages":      func() error { _, err := Pages(junk); return err },
		"Split":      func() error { _, err := Split(junk); return err },
		"Merge":      func() error { _, err := Merge(junk); return err },
		"Rotate":     func() error { _, err := Rotate(junk, nil, 90); return err },
		"Watermark":  func() error { _, err := Watermark(junk, "x"); return err },
		"Fields":     func() error { _, err := Fields(junk); return err },
		"FillFields": func() error { _, err := FillFields(junk, map[string]string{"a": "b"}); return err },
		"empty":      func() error { return Check(nil) },
	}
	for name, f := range checks {
		if err := f(); err == nil {
			t.Errorf("%s: no error for bad input", name)
		}
	}
}

func TestInputOverLimitIsError(t *testing.T) {
	big := make([]byte, MaxInputBytes+1)
	if err := Check(big); err == nil {
		t.Error("input over the limit: want an error")
	}
}

// TestNoPanicOnPrefixes cuts a good PDF at many lengths and calls every
// operation on each cut. A truncated file may still be readable, because
// pdfcpu rebuilds a broken cross-reference table, so only the absence of a
// panic is checked here.
func TestNoPanicOnPrefixes(t *testing.T) {
	good := mustCreate(t, sampleSpec())
	step := len(good) / 150
	if step < 1 {
		step = 1
	}
	for n := 0; n <= len(good); n += step {
		p := good[:n]
		_ = Check(p)
		_, _ = Pages(p)
		_, _ = Split(p)
		_, _ = Merge(p, good)
		_, _ = Rotate(p, []int{1}, 90)
		_, _ = Watermark(p, "x")
		_, _ = Fields(p)
		_, _ = FillFields(p, map[string]string{"x": "y"})
	}
}
