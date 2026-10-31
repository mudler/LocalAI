// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestFiles() *files.Service {
	dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return files.New(ws, files.WithBlobs(blobs))
}

// raw sends a request with a body of any type.
func (s *studioServer) raw(user, method, path, contentType string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if user != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: s.sessions[user]})
	}
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	return rec
}

func q(path string) string { return url.QueryEscape(path) }

func (s *studioServer) putFile(user, path, content string) *httptest.ResponseRecorder {
	return s.raw(user, http.MethodPut, "/api/chats/files/content?path="+q(path), "application/octet-stream", strings.NewReader(content))
}

func (s *studioServer) names(user, dir string) []string {
	rec := s.do(user, http.MethodGet, "/api/chats/files?path="+q(dir), nil)
	Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	out := []string{}
	for _, e := range decodeJSON(rec)["entries"].([]any) {
		out = append(out, e.(map[string]any)["name"].(string))
	}
	return out
}

var _ = Describe("Chats files endpoints", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("list", http.MethodGet, "/api/chats/files"),
		Entry("content", http.MethodGet, "/api/chats/files/content?path=/a"),
		Entry("upload", http.MethodPut, "/api/chats/files/content?path=/a"),
		Entry("folder", http.MethodPost, "/api/chats/files/folders"),
		Entry("move", http.MethodPost, "/api/chats/files/move"),
		Entry("delete", http.MethodDelete, "/api/chats/files?path=/a"),
		Entry("trash", http.MethodGet, "/api/chats/files/trash"),
		Entry("empty the trash", http.MethodDelete, "/api/chats/files/trash"),
		Entry("purge one trash entry", http.MethodDelete, "/api/chats/files/trash/3"),
		Entry("undo", http.MethodPost, "/api/chats/files/undo"),
		Entry("history", http.MethodGet, "/api/chats/files/history"),
		Entry("search", http.MethodGet, "/api/chats/files/search?q=a"),
	)

	It("stores a file and reads it back, listing it in its folder", func() {
		rec := s.putFile("alice", "/Notes/a.txt", "hello")
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("path", "/Notes/a.txt"))
		Expect(s.names("alice", "/Notes")).To(Equal([]string{"a.txt"}))
		Expect(s.names("alice", "/")).To(ContainElement("Notes"))

		got := s.do("alice", http.MethodGet, "/api/chats/files/content?path="+q("/Notes/a.txt"), nil)
		Expect(got.Code).To(Equal(http.StatusOK))
		Expect(got.Body.String()).To(Equal("hello"))
	})

	It("marks the folders LocalAI manages, and moves old settings files into LocalAI/ before listing the home", func() {
		Expect(s.putFile("alice", "/ui.json", `{"theme":"light"}`).Code).To(Equal(http.StatusOK))
		Expect(s.putFile("alice", "/Notes/a.txt", "mine").Code).To(Equal(http.StatusOK))
		managed := func(dir string) map[string]string {
			rec := s.do("alice", http.MethodGet, "/api/chats/files?path="+q(dir), nil)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			out := map[string]string{}
			for _, e := range decodeJSON(rec)["entries"].([]any) {
				m := e.(map[string]any)
				v, _ := m["managed"].(string)
				out[m["name"].(string)] = v
			}
			return out
		}
		Expect(managed("/")).To(Equal(map[string]string{"Chats": "chats", "LocalAI": "localai", "Notes": ""}))
		Expect(managed("/LocalAI")).To(Equal(map[string]string{"ui.json": "localai"}))
		Expect(managed("/Notes")).To(Equal(map[string]string{"a.txt": ""}))
	})

	It("serves files so that a browser cannot run them", func() {
		Expect(s.putFile("alice", "/page.html", "<script>alert(1)</script>").Code).To(Equal(http.StatusOK))
		rec := s.do("alice", http.MethodGet, "/api/chats/files/content?path="+q("/page.html")+"&inline=1", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"))
		Expect(rec.Header().Get("Content-Security-Policy")).To(ContainSubstring("sandbox"))
		Expect(rec.Header().Get("Content-Disposition")).To(HavePrefix("attachment"))
		Expect(rec.Header().Get("Content-Type")).NotTo(ContainSubstring("html"))

		Expect(s.putFile("alice", "/pic.png", "\x89PNG\r\n\x1a\n").Code).To(Equal(http.StatusOK))
		img := s.do("alice", http.MethodGet, "/api/chats/files/content?path="+q("/pic.png")+"&inline=1", nil)
		Expect(img.Header().Get("Content-Disposition")).To(HavePrefix("inline"))
	})

	It("serves an inline PDF for the preview pane with the same sandbox as every other type", func() {
		Expect(s.putFile("alice", "/doc.pdf", "%PDF-1.4\n%%EOF\n").Code).To(Equal(http.StatusOK))
		rec := s.do("alice", http.MethodGet, "/api/chats/files/content?path="+q("/doc.pdf")+"&inline=1", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Content-Type")).To(Equal("application/pdf"))
		Expect(rec.Header().Get("Content-Disposition")).To(HavePrefix("inline"))
		// The browser's PDF viewer works inside the sandbox, so the pane needs
		// no weaker policy for it.
		Expect(rec.Header().Get("Content-Security-Policy")).To(Equal("sandbox; default-src 'none'"))
		Expect(rec.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"))
	})

	It("keeps another user's files out of reach", func() {
		Expect(s.putFile("alice", "/secret.txt", "mine").Code).To(Equal(http.StatusOK))
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/api/chats/files?path=" + q("/secret.txt")},
			{http.MethodGet, "/api/chats/files/content?path=" + q("/secret.txt")},
			{http.MethodDelete, "/api/chats/files?path=" + q("/secret.txt")},
		} {
			Expect(s.do("bob", c.method, c.path, nil).Code).To(Equal(http.StatusNotFound), c.method+" "+c.path)
		}
		Expect(s.do("bob", http.MethodPost, "/api/chats/files/move", map[string]string{"from": "/secret.txt", "to": "/stolen.txt"}).Code).To(Equal(http.StatusNotFound))
		Expect(s.names("bob", "/")).NotTo(ContainElement("secret.txt"))
		// Naming a path outside the home is refused, not resolved.
		for _, p := range []string{"/../alice/secret.txt", "/a/../../alice", "/..", "/x\x00y"} {
			Expect(s.do("bob", http.MethodGet, "/api/chats/files?path="+q(p), nil).Code).To(Equal(http.StatusBadRequest), p)
		}
		// The trash and the history of one user show nothing of another's.
		Expect(s.do("alice", http.MethodDelete, "/api/chats/files?path="+q("/secret.txt"), nil).Code).To(Equal(http.StatusOK))
		trash := decodeJSON(s.do("bob", http.MethodGet, "/api/chats/files/trash", nil))["entries"].([]any)
		Expect(trash).To(BeEmpty())
		// Bob cannot undo Alice's deletion by guessing its revision.
		mine := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/files/trash", nil))["entries"].([]any)
		Expect(mine).To(HaveLen(1))
		rev := mine[0].(map[string]any)["rev"]
		rec := s.do("bob", http.MethodPost, "/api/chats/files/undo", map[string]any{"rev": rev})
		Expect(rec.Code).NotTo(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/files/trash", nil))["entries"].([]any)).To(HaveLen(1))
		Expect(s.names("alice", "/")).NotTo(ContainElement("secret.txt"))
	})

	It("keeps both files when a second upload does not start from the first", func() {
		Expect(s.putFile("alice", "/a.txt", "one").Code).To(Equal(http.StatusOK))
		rec := s.putFile("alice", "/a.txt", "two")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(rec)["conflict_of"]).To(Equal("/a.txt"))
		Expect(s.names("alice", "/")).To(HaveLen(3)) // Chats, a.txt and the copy
		got := s.do("alice", http.MethodGet, "/api/chats/files/content?path="+q("/a.txt"), nil)
		Expect(got.Body.String()).To(Equal("one"))
	})

	It("moves, deletes into the trash and restores", func() {
		Expect(s.putFile("alice", "/Docs/a.txt", "data").Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodPost, "/api/chats/files/folders", map[string]string{"path": "/Archive"}).Code).To(Equal(http.StatusOK))
		Expect(s.do("alice", http.MethodPost, "/api/chats/files/move", map[string]string{"from": "/Docs/a.txt", "to": "/Archive/b.txt"}).Code).To(Equal(http.StatusOK))
		Expect(s.names("alice", "/Archive")).To(Equal([]string{"b.txt"}))

		Expect(s.do("alice", http.MethodDelete, "/api/chats/files?path="+q("/Archive"), nil).Code).To(Equal(http.StatusOK))
		Expect(s.names("alice", "/")).NotTo(ContainElement("Archive"))
		trash := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/files/trash", nil))["entries"].([]any)
		Expect(trash).To(HaveLen(1))
		entry := trash[0].(map[string]any)
		Expect(entry["paths"]).To(ConsistOf("/Archive"))

		rec := s.do("alice", http.MethodPost, "/api/chats/files/undo", map[string]any{"rev": entry["rev"]})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(s.names("alice", "/Archive")).To(Equal([]string{"b.txt"}))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/files/trash", nil))["entries"].([]any)).To(BeEmpty())
	})

	Describe("emptying the Trash", func() {
		trashOf := func(user string) map[string]any {
			rec := s.do(user, http.MethodGet, "/api/chats/files/trash", nil)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			return decodeJSON(rec)
		}

		It("counts what the Trash holds, per entry and in total", func() {
			Expect(s.putFile("alice", "/Docs/a.txt", "12345").Code).To(Equal(http.StatusOK))
			Expect(s.putFile("alice", "/Docs/b.txt", "123").Code).To(Equal(http.StatusOK))
			Expect(s.putFile("alice", "/c.txt", "1").Code).To(Equal(http.StatusOK))
			Expect(s.do("alice", http.MethodDelete, "/api/chats/files?path="+q("/Docs"), nil).Code).To(Equal(http.StatusOK))
			Expect(s.do("alice", http.MethodDelete, "/api/chats/files?path="+q("/c.txt"), nil).Code).To(Equal(http.StatusOK))

			tr := trashOf("alice")
			Expect(tr["totals"]).To(Equal(map[string]any{"files": 3.0, "folders": 1.0, "bytes": 9.0}))
			byPath := map[string]map[string]any{}
			for _, e := range tr["entries"].([]any) {
				m := e.(map[string]any)
				byPath[m["paths"].([]any)[0].(string)] = m
			}
			Expect(byPath["/Docs"]).To(HaveKeyWithValue("files", BeNumerically("==", 2)))
			Expect(byPath["/Docs"]).To(HaveKeyWithValue("folders", BeNumerically("==", 1)))
			Expect(byPath["/Docs"]).To(HaveKeyWithValue("bytes", BeNumerically("==", 8)))
			Expect(byPath["/c.txt"]).To(HaveKeyWithValue("bytes", BeNumerically("==", 1)))
		})

		It("deletes one entry for good and leaves the rest restorable", func() {
			Expect(s.putFile("alice", "/a.txt", "aaaa").Code).To(Equal(http.StatusOK))
			Expect(s.putFile("alice", "/b.txt", "bb").Code).To(Equal(http.StatusOK))
			s.do("alice", http.MethodDelete, "/api/chats/files?path="+q("/a.txt"), nil)
			s.do("alice", http.MethodDelete, "/api/chats/files?path="+q("/b.txt"), nil)
			var aRev, bRev any
			for _, e := range trashOf("alice")["entries"].([]any) {
				m := e.(map[string]any)
				if m["paths"].([]any)[0] == "/a.txt" {
					aRev = m["rev"]
				} else {
					bRev = m["rev"]
				}
			}

			rec := s.do("alice", http.MethodDelete, "/api/chats/files/trash/"+fmt.Sprint(int64(aRev.(float64))), nil)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			Expect(decodeJSON(rec)).To(Equal(map[string]any{"files": 1.0, "folders": 0.0, "bytes": 4.0}))
			Expect(trashOf("alice")["entries"].([]any)).To(HaveLen(1))
			// The purged deletion cannot be restored; the other one can.
			Expect(s.do("alice", http.MethodPost, "/api/chats/files/undo", map[string]any{"rev": aRev}).Code).To(Equal(http.StatusNotFound))
			Expect(s.do("alice", http.MethodPost, "/api/chats/files/undo", map[string]any{"rev": bRev}).Code).To(Equal(http.StatusOK))
			Expect(s.names("alice", "/")).To(ContainElement("b.txt"))
			Expect(s.names("alice", "/")).NotTo(ContainElement("a.txt"))
		})

		It("empties the Trash and keeps live files", func() {
			Expect(s.putFile("alice", "/keep.txt", "keep").Code).To(Equal(http.StatusOK))
			Expect(s.putFile("alice", "/gone.txt", "gone").Code).To(Equal(http.StatusOK))
			s.do("alice", http.MethodDelete, "/api/chats/files?path="+q("/gone.txt"), nil)

			rec := s.do("alice", http.MethodDelete, "/api/chats/files/trash", nil)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			Expect(decodeJSON(rec)).To(HaveKeyWithValue("files", BeNumerically("==", 1)))
			tr := trashOf("alice")
			Expect(tr["entries"].([]any)).To(BeEmpty())
			Expect(tr["totals"]).To(Equal(map[string]any{"files": 0.0, "folders": 0.0, "bytes": 0.0}))
			Expect(s.names("alice", "/")).To(ContainElement("keep.txt"))
			// Emptying an empty Trash is fine.
			Expect(s.do("alice", http.MethodDelete, "/api/chats/files/trash", nil).Code).To(Equal(http.StatusOK))
		})

		It("never touches the Trash of another user", func() {
			Expect(s.putFile("alice", "/a.txt", "alice").Code).To(Equal(http.StatusOK))
			s.do("alice", http.MethodDelete, "/api/chats/files?path="+q("/a.txt"), nil)
			rev := trashOf("alice")["entries"].([]any)[0].(map[string]any)["rev"]

			Expect(s.do("bob", http.MethodDelete, "/api/chats/files/trash/"+fmt.Sprint(int64(rev.(float64))), nil).Code).To(Equal(http.StatusNotFound))
			Expect(decodeJSON(s.do("bob", http.MethodDelete, "/api/chats/files/trash", nil))).To(HaveKeyWithValue("files", BeNumerically("==", 0)))
			Expect(trashOf("alice")["entries"].([]any)).To(HaveLen(1), "bob emptied alice's Trash")
			Expect(s.do("alice", http.MethodDelete, "/api/chats/files/trash/abc", nil).Code).To(Equal(http.StatusBadRequest))
		})
	})

	It("searches the user's own files by name and by text", func() {
		Expect(s.putFile("alice", "/Notes/budget.txt", "we discussed the Budget\nand hiring").Code).To(Equal(http.StatusOK))
		Expect(s.putFile("bob", "/Notes/other.txt", "budget of bob").Code).To(Equal(http.StatusOK))
		rec := s.do("alice", http.MethodGet, "/api/chats/files/search?q=budget", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		hits := decodeJSON(rec)["hits"].([]any)
		paths := []string{}
		for _, h := range hits {
			paths = append(paths, h.(map[string]any)["path"].(string))
		}
		Expect(paths).To(Equal([]string{"/Notes/budget.txt", "/Notes/budget.txt"})) // the name, then the line
		Expect(hits[1].(map[string]any)).To(HaveKeyWithValue("line", BeNumerically("==", 1)))
		Expect(s.do("alice", http.MethodGet, "/api/chats/files/search?q=", nil).Code).To(Equal(http.StatusBadRequest))
	})

	It("lists the history of a folder", func() {
		Expect(s.putFile("alice", "/Docs/a.txt", "data").Code).To(Equal(http.StatusOK))
		rec := s.do("alice", http.MethodGet, "/api/chats/files/history?path="+q("/Docs"), nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["entries"].([]any)).NotTo(BeEmpty())
	})

	It("answers 409 with the reason on every route when files are off", func() {
		off := newStudioServerFiles(files.Disabled("There is no data path, so files are off."))
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/api/chats/files"},
			{http.MethodGet, "/api/chats/files/content?path=/a"},
			{http.MethodGet, "/api/chats/files/trash"},
			{http.MethodDelete, "/api/chats/files/trash"},
			{http.MethodGet, "/api/chats/files/history"},
		} {
			rec := off.do("alice", c.method, c.path, nil)
			Expect(rec.Code).To(Equal(http.StatusConflict), c.path)
			Expect(rec.Body.String()).To(ContainSubstring("There is no data path"))
		}
		Expect(off.putFile("alice", "/a.txt", "x").Code).To(Equal(http.StatusConflict))
		list := decodeJSON(off.do("alice", http.MethodGet, "/api/chats", nil))
		Expect(list).To(HaveKeyWithValue("files", false))
		Expect(list["files_reason"]).To(ContainSubstring("no data path"))
	})

	Describe("attachments", func() {
		attach := func(user, id, text, name, body string) *httptest.ResponseRecorder {
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			Expect(mw.WriteField("text", text)).To(Succeed())
			fw, err := mw.CreateFormFile("files", name)
			Expect(err).NotTo(HaveOccurred())
			_, _ = fw.Write([]byte(body))
			Expect(mw.Close()).To(Succeed())
			return s.raw(user, http.MethodPost, "/api/chats/"+id+"/messages", mw.FormDataContentType(), &buf)
		}

		It("saves an attached file in the chat folder and starts the turn", func() {
			id := s.create("alice")
			rec := attach("alice", id, "read this", "notes.txt", "alpha")
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			Expect(decodeJSON(rec)).To(HaveKeyWithValue("started", true))
			Eventually(func() int { return len(s.harness.Sent()) }).Should(Equal(1))
			Expect(s.harness.Sent()[0]).To(And(ContainSubstring("read this"), ContainSubstring("notes.txt")))
			folders := s.names("alice", "/Chats")
			Expect(folders).To(HaveLen(1))
			Expect(s.names("alice", "/Chats/"+folders[0])).To(Equal([]string{"notes.txt"}))
		})

		It("saves an attachment sent while a turn runs and queues the message with its path", func() {
			id := s.create("alice")
			Expect(s.do("alice", http.MethodPost, "/api/chats/"+id+"/messages", map[string]string{"text": "go"}).Code).To(Equal(http.StatusOK))
			Eventually(func() int { return len(s.harness.Sent()) }).Should(Equal(1))
			rec := attach("alice", id, "more", "x.txt", "x")
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			Expect(decodeJSON(rec)).To(And(HaveKeyWithValue("started", false), HaveKey("pending_id")))
			folders := s.names("alice", "/Chats")
			Expect(folders).To(HaveLen(1))
			Expect(s.names("alice", "/Chats/"+folders[0])).To(Equal([]string{"x.txt"}))
			conv := s.do("alice", http.MethodGet, "/api/chats/"+id, nil)
			Expect(conv.Body.String()).To(ContainSubstring("x.txt"))
		})

		It("refuses an attachment when files are off", func() {
			off := newStudioServerFiles(files.Disabled("files are off here"))
			id := off.create("alice")
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			_ = mw.WriteField("text", "see")
			fw, _ := mw.CreateFormFile("files", "x.txt")
			_, _ = fw.Write([]byte("x"))
			_ = mw.Close()
			rec := off.raw("alice", http.MethodPost, "/api/chats/"+id+"/messages", mw.FormDataContentType(), &buf)
			Expect(rec.Code).To(Equal(http.StatusConflict))
			Expect(rec.Body.String()).To(ContainSubstring("files are off here"))
		})
	})
})

// errBody is a request body whose read fails the way echo's body limit does.
type errBody struct{ err error }

func (b errBody) Read([]byte) (int, error) { return 0, b.err }
func (b errBody) Close() error             { return nil }

var _ = Describe("An upload over the server's body limit", func() {
	It("answers 413 and keeps no file, not a 500", func() {
		s := newStudioServer()
		req := s.request("alice", http.MethodPut, "/api/chats/files/content?path=/huge.bin", "")
		req.Body = errBody{err: echo.ErrStatusRequestEntityTooLarge}
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusRequestEntityTooLarge), rec.Body.String())
		Expect(s.do("alice", http.MethodGet, "/api/chats/files/content?path=/huge.bin", nil).Code).To(Equal(http.StatusNotFound))
	})
})
