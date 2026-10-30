// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// previewWorkbook is an .xlsx with one sheet whose first cell is a shared
// string.
func previewWorkbook(first string) string {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	add := func(name, body string) {
		f, err := w.Create(name)
		Expect(err).NotTo(HaveOccurred())
		_, _ = f.Write([]byte(body))
	}
	add("xl/workbook.xml", `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Budget" sheetId="1" r:id="rId1"/></sheets></workbook>`)
	add("xl/_rels/workbook.xml.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`)
	add("xl/sharedStrings.xml", `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>`+first+`</t></si></sst>`)
	add("xl/worksheets/sheet1.xml", `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1"><v>42</v></c></row></sheetData></worksheet>`)
	Expect(w.Close()).To(Succeed())
	return buf.String()
}

func previewDocx(text string) string {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create("word/document.xml")
	Expect(err).NotTo(HaveOccurred())
	_, _ = f.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`))
	Expect(w.Close()).To(Succeed())
	return buf.String()
}

var _ = Describe("Chats file preview endpoint", func() {
	var (
		s *studioServer
		f *files.Service
	)
	preview := func(user, path string) (int, map[string]any) {
		GinkgoHelper()
		rec := s.do(user, http.MethodGet, "/api/chats/files/preview?path="+q(path), nil)
		var body map[string]any
		if strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			body = decodeJSON(rec)
		}
		return rec.Code, body
	}
	BeforeEach(func() {
		f = newTestFiles()
		s = newStudioServerWith(f, func(d *studio.Deps) {
			d.FindUser = func(_ context.Context, ref string) (*auth.User, error) {
				for _, id := range []string{"alice", "bob", "root"} {
					if ref == id {
						return &auth.User{ID: id, Name: id}, nil
					}
				}
				return nil, studio.ErrNoUser
			}
		})
	})

	It("requires authentication", func() {
		Expect(s.do("", http.MethodGet, "/api/chats/files/preview?path=/a.csv", nil).Code).To(Equal(http.StatusUnauthorized))
	})

	It("reads a workbook, a CSV file and a Word document of the caller", func() {
		Expect(s.putFile("alice", "/Chats/Budget/budget.xlsx", previewWorkbook("Item")).Code).To(Equal(http.StatusOK))
		code, body := preview("alice", "/Chats/Budget/budget.xlsx")
		Expect(code).To(Equal(http.StatusOK), fmt.Sprint(body))
		Expect(body).To(HaveKeyWithValue("kind", "table"))
		sheet := body["sheets"].([]any)[0].(map[string]any)
		Expect(sheet).To(HaveKeyWithValue("name", "Budget"))
		Expect(sheet["rows"]).To(Equal([]any{[]any{"Item", "42"}}))
		Expect(sheet).To(HaveKeyWithValue("total_rows", BeNumerically("==", 1)))
		Expect(sheet).To(HaveKeyWithValue("total_cols", BeNumerically("==", 2)))

		Expect(s.putFile("alice", "/q1.csv", "\xef\xbb\xbfa,\"b,c\"\n1,2\n").Code).To(Equal(http.StatusOK))
		code, body = preview("alice", "/q1.csv")
		Expect(code).To(Equal(http.StatusOK))
		Expect(body["sheets"].([]any)[0].(map[string]any)["rows"]).To(Equal([]any{[]any{"a", "b,c"}, []any{"1", "2"}}))

		Expect(s.putFile("alice", "/report.docx", previewDocx("Summary")).Code).To(Equal(http.StatusOK))
		code, body = preview("alice", "/report.docx")
		Expect(code).To(Equal(http.StatusOK))
		Expect(body).To(HaveKeyWithValue("kind", "document"))
		Expect(body["blocks"]).To(Equal([]any{map[string]any{"type": "heading", "level": float64(1), "text": "Summary"}}))
	})

	It("follows a new version of the file, not a cached answer", func() {
		Expect(s.putFile("alice", "/t.csv", "old\n").Code).To(Equal(http.StatusOK))
		_, body := preview("alice", "/t.csv")
		Expect(body["sheets"].([]any)[0].(map[string]any)["rows"]).To(Equal([]any{[]any{"old"}}))
		Expect(s.raw("alice", http.MethodPut, "/api/chats/files/content?path="+q("/t.csv")+"&overwrite=1", "application/octet-stream", strings.NewReader("new\n")).Code).To(Equal(http.StatusOK))
		_, body = preview("alice", "/t.csv")
		Expect(body["sheets"].([]any)[0].(map[string]any)["rows"]).To(Equal([]any{[]any{"new"}}))
	})

	It("answers as the content endpoint does for paths it may not read", func() {
		Expect(s.putFile("alice", "/secret.csv", "a\n").Code).To(Equal(http.StatusOK))
		for _, c := range []struct {
			user, path string
			want       int
		}{
			{"bob", "/secret.csv", http.StatusNotFound},
			{"alice", "/missing.csv", http.StatusNotFound},
			{"bob", "/../alice/secret.csv", http.StatusBadRequest},
			{"bob", "/a/../../alice/secret.csv", http.StatusBadRequest},
			{"alice", "/x\x00y.csv", http.StatusBadRequest},
			{"alice", "/", http.StatusBadRequest},
		} {
			got, _ := preview(c.user, c.path)
			Expect(got).To(Equal(c.want), c.user+" "+c.path)
			content := s.do(c.user, http.MethodGet, "/api/chats/files/content?path="+q(c.path), nil).Code
			Expect(got).To(Equal(content), "the same answer as the content endpoint for "+c.path)
		}
	})

	It("keeps a shared folder out of reach, as the content endpoint does", func() {
		rec := s.do("root", http.MethodPost, "/api/chats/admin/shared-folders", map[string]any{"name": "Team"})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		id := fmt.Sprint(int64(decodeJSON(rec)["id"].(float64)))
		Expect(s.do("root", http.MethodPut, "/api/chats/admin/shared-folders/"+id+"/members/alice", map[string]any{"mode": "read"}).Code).To(Equal(http.StatusOK))
		_, err := f.Store().Put(context.Background(), f.Actor("root"), "/shared/Team/plan.csv", strings.NewReader("a,b\n"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		for _, p := range []string{"/shared/Team/plan.csv", "/../../shared/Team/plan.csv"} {
			got, _ := preview("alice", p)
			Expect(got).To(BeElementOf(http.StatusNotFound, http.StatusBadRequest), p)
			Expect(got).To(Equal(s.do("alice", http.MethodGet, "/api/chats/files/content?path="+q(p), nil).Code), p)
		}
	})

	It("refuses a type it does not read, a broken file, a folder and a file over 20 MiB", func() {
		Expect(s.putFile("alice", "/notes.txt", "hello").Code).To(Equal(http.StatusOK))
		code, body := preview("alice", "/notes.txt")
		Expect(code).To(Equal(http.StatusUnsupportedMediaType))
		Expect(body["error"]).To(HaveKeyWithValue("type", "unsupported_preview"))

		Expect(s.putFile("alice", "/broken.xlsx", "not a zip").Code).To(Equal(http.StatusOK))
		code, body = preview("alice", "/broken.xlsx")
		Expect(code).To(Equal(http.StatusUnprocessableEntity))
		Expect(body["error"]).To(HaveKeyWithValue("type", "invalid_file"))

		Expect(s.do("alice", http.MethodPost, "/api/chats/files/folders", map[string]string{"path": "/dir.csv"}).Code).To(Equal(http.StatusOK))
		code, _ = preview("alice", "/dir.csv")
		Expect(code).To(Equal(http.StatusBadRequest))

		big := strings.Repeat("a,b,c,d,e,f,g\n", (20<<20)/14+1)
		Expect(len(big)).To(BeNumerically(">", 20<<20))
		Expect(s.putFile("alice", "/big.csv", big).Code).To(Equal(http.StatusOK))
		code, body = preview("alice", "/big.csv")
		Expect(code).To(Equal(http.StatusRequestEntityTooLarge))
		Expect(body["error"]).To(HaveKeyWithValue("type", "too_large"))
	})
})
