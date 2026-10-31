package media_test

import (
	"archive/zip"
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func zipBytes(parts ...string) string {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i+1 < len(parts); i += 2 {
		w, err := zw.Create(parts[i])
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Write([]byte(parts[i+1]))
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(zw.Close()).To(Succeed())
	return buf.String()
}

// pythonWorkbook is one sheet written the way some Python libraries write it:
// an absolute target for the sheet and inline strings, no shared strings.
func pythonWorkbook(cells string) string {
	ns := `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	return zipBytes(
		"xl/workbook.xml", `<workbook `+ns+`><sheets><sheet name="Index" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="/xl/worksheets/sheet1.xml" Id="rId1"/></Relationships>`,
		"xl/worksheets/sheet1.xml", `<worksheet `+ns+`><sheetData>`+cells+`</sheetData></worksheet>`)
}

var _ = Describe("read_document", func() {
	It("reads a workbook with inline strings and an absolute sheet path", func() {
		fx := newFixture("", "")
		fx.put("/index.xlsx", pythonWorkbook(`<row r="1"><c r="A1" t="inlineStr"><is><t>file</t></is></c><c r="B1" t="inlineStr"><is><t>folder</t></is></c></row>`+
			`<row r="2"><c r="A2" t="inlineStr"><is><t>q1.pdf</t></is></c><c r="B2" t="inlineStr"><is><t>quotes</t></is></c></row>`))
		out, err := run(fx.tool("read_document"), map[string]any{"path": "/index.xlsx"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("## Sheet \"Index\" (2 rows, 2 columns)\nfile\tfolder\nq1.pdf\tquotes"))
	})

	It("says that an empty workbook has no text, not that it is a scan", func() {
		fx := newFixture("", "")
		fx.put("/empty.xlsx", zipBytes("word/document.xml", "<x/>"))
		_, err := run(fx.tool("read_document"), map[string]any{"path": "/empty.xlsx"})
		Expect(err).To(MatchError("empty.xlsx is not a valid xlsx file"))

		fx.put("/blank.docx", zipBytes("word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body/></w:document>`))
		_, err = run(fx.tool("read_document"), map[string]any{"path": "/blank.docx"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("blank.docx contains no text"))
		Expect(err.Error()).NotTo(ContainSubstring("scan"))
	})

	It("says that a PDF with no text layer is probably a scan", func() {
		fx := newFixture("", "")
		pdf := "%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n" +
			"3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n"
		fx.put("/scan.pdf", pdf)
		_, err := run(fx.tool("read_document"), map[string]any{"path": "/scan.pdf"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("scan.pdf is a PDF with no text layer"))
		Expect(err.Error()).To(ContainSubstring("describe_image"))
	})
})
