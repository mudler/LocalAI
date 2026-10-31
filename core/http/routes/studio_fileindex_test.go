//go:build auth

package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/extract/extracttest"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/textindex"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
)

// newIndexedTestFiles is newTestFiles with the text index of the documents,
// on the same database, as the server builds it.
func newIndexedTestFiles() (*files.Service, *textindex.Indexer) {
	dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	st, err := textindex.NewGORM(db)
	Expect(err).NotTo(HaveOccurred())
	f := files.New(ws, files.WithBlobs(blobs))
	ix := textindex.New(textindex.Options{Store: st, Workspace: ws, Admin: workspace.Actor{Principal: files.AdminPrincipal}})
	f.SetTextIndex(ix)
	return f, ix
}

var _ = Describe("Finding an uploaded document by the words inside it", func() {
	It("finds a PDF with a generated name through the upload route, the search route and the chat's search_files", func() {
		f, ix := newIndexedTestFiles()
		s := newStudioServerWith(f, nil)
		const name = "/Downloads/3f2b8c1e-9a4d-4e7b-8c21-5d6e7f8a9b0c.pdf"
		pdf := extracttest.TextPDF("Invoice", "Boiler service visit on the third of March")
		rec := s.raw("alice", http.MethodPut, "/api/chats/files/content?path="+q(name), "application/octet-stream", bytes.NewReader(pdf))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())

		// Before the indexer read it, the search says it is being indexed.
		rec = s.do("alice", http.MethodGet, "/api/chats/files/search?q=boiler", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("indexing", BeNumerically("==", 1)))

		Expect(ix.Pass(context.Background())).To(Succeed())

		rec = s.do("alice", http.MethodGet, "/api/chats/files/search?q=boiler", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var res struct {
			Hits []struct {
				Path     string `json:"path"`
				Document bool   `json:"document"`
				Page     int    `json:"page"`
				Snippet  string `json:"snippet"`
			} `json:"hits"`
			Indexing int `json:"indexing"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &res)).To(Succeed())
		Expect(res.Indexing).To(BeZero())
		Expect(res.Hits).To(HaveLen(1))
		Expect(res.Hits[0].Path).To(Equal(name))
		Expect(res.Hits[0].Document).To(BeTrue())
		Expect(res.Hits[0].Page).To(Equal(2))
		Expect(res.Hits[0].Snippet).To(ContainSubstring("Boiler service visit"))

		// The tool a chat of alice's gets finds it too, and another user does not.
		search := func(user string) string {
			for _, t := range f.Tools(workspace.Actor{Principal: user, ChatID: "c1"}) {
				if t.Name == "search_files" {
					out, err := t.Run(context.Background(), json.RawMessage(`{"query":"third of march"}`))
					Expect(err).NotTo(HaveOccurred())
					return out
				}
			}
			Fail("no search_files tool")
			return ""
		}
		Expect(search("alice")).To(ContainSubstring(name + " (page 2, line"))
		Expect(f.EnsureHome(context.Background(), "bob")).To(Succeed())
		Expect(search("bob")).To(Equal("Nothing found."))
		rec = s.do("bob", http.MethodGet, "/api/chats/files/search?q=boiler", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["hits"]).To(BeEmpty())
	})
})
