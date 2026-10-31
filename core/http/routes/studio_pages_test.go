// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats pages endpoints", func() {
	var s *studioServer
	BeforeEach(func() {
		s = newStudioServer()
		Expect(s.putFile("alice", "/pages/tasks/index.html", "<p>tasks</p>").Code).To(Equal(http.StatusOK))
		Expect(s.putFile("alice", "/pages/tasks/page.json", `{"title":"Tasks board"}`).Code).To(Equal(http.StatusOK))
		Expect(s.putFile("alice", "/pages/tasks/tasks.json", `[]`).Code).To(Equal(http.StatusOK))
		Expect(s.putFile("alice", "/secret.txt", "outside").Code).To(Equal(http.StatusOK))
	})

	It("lists the caller's pages and serves one with its HTML", func() {
		rec := s.do("alice", http.MethodGet, "/api/chats/pages", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		list := decodeJSON(rec)["pages"].([]any)
		Expect(list).To(HaveLen(1))
		p := list[0].(map[string]any)
		Expect(p["name"]).To(Equal("tasks"))
		Expect(p["title"]).To(Equal("Tasks board"))
		Expect(p["source"]).To(Equal("own"))
		Expect(p["author"]).To(Equal("you"))

		rec = s.do("alice", http.MethodGet, "/api/chats/pages/tasks", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["html"]).To(Equal("<p>tasks</p>"))
		Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))
	})

	It("never shows one user's pages to another", func() {
		rec := s.do("bob", http.MethodGet, "/api/chats/pages", nil)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(rec)["pages"]).To(BeEmpty())
		for _, path := range []string{"/api/chats/pages/tasks", "/api/chats/pages/tasks/file?path=tasks.json", "/api/chats/pages/tasks/files"} {
			Expect(s.do("bob", http.MethodGet, path, nil).Code).To(Equal(http.StatusNotFound), path)
		}
		Expect(s.do("bob", http.MethodPut, "/api/chats/pages/tasks/file", map[string]any{"path": "x.json", "content": "{}"}).Code).To(Equal(http.StatusNotFound))
	})

	It("needs the pages feature, which an administrator always has", func() {
		Expect(s.do("dave", http.MethodGet, "/api/chats/pages", nil).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("dave", http.MethodGet, "/api/chats/page-starters", nil).Code).To(Equal(http.StatusForbidden))
		Expect(s.do("carol", http.MethodGet, "/api/chats/pages", nil).Code).To(Equal(http.StatusForbidden), "carol has no chats")
		Expect(s.do("root", http.MethodGet, "/api/chats/pages", nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("", http.MethodGet, "/api/chats/pages", nil).Code).To(Equal(http.StatusUnauthorized))
	})

	It("reads, writes and lists the page's own files through the bridge routes", func() {
		rec := s.do("alice", http.MethodPut, "/api/chats/pages/tasks/file", map[string]any{"path": "tasks.json", "content": `[{"t":1}]`})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["path"]).To(Equal("tasks.json"))
		rec = s.do("alice", http.MethodGet, "/api/chats/pages/tasks/file?path=tasks.json", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["content"]).To(Equal(`[{"t":1}]`))
		rec = s.do("alice", http.MethodGet, "/api/chats/pages/tasks/files", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["entries"]).To(HaveLen(3))
	})

	It("reads an image of the page as base64 for the UI, and only an image", func() {
		Expect(s.putFile("alice", "/pages/tasks/logo.png", "\x89PNG\r\n").Code).To(Equal(http.StatusOK))
		rec := s.do("alice", http.MethodGet, "/api/chats/pages/tasks/file?encoding=base64&path=logo.png", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		got := decodeJSON(rec)
		Expect(got["content"]).To(Equal("iVBORw0K"))
		Expect(got["encoding"]).To(Equal("base64"))
		Expect(got["media_type"]).To(Equal("image/png"))
		Expect(rec.Header().Get("Cache-Control")).To(Equal("no-store"))
		rec = s.do("alice", http.MethodGet, "/api/chats/pages/tasks/file?encoding=base64&path=tasks.json", nil)
		Expect(rec.Code).To(Equal(http.StatusUnsupportedMediaType), rec.Body.String())
		Expect(s.do("alice", http.MethodGet, "/api/chats/pages/tasks/file?encoding=hex&path=logo.png", nil).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodGet, "/api/chats/pages/tasks/file?encoding=base64&path="+q("../../secret.png"), nil).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("bob", http.MethodGet, "/api/chats/pages/tasks/file?encoding=base64&path=logo.png", nil).Code).To(Equal(http.StatusNotFound))
		// Without encoding the file is read as text, as the bridge does.
		Expect(s.do("alice", http.MethodGet, "/api/chats/pages/tasks/file?path=logo.png", nil).Code).To(Equal(http.StatusUnsupportedMediaType))
	})

	DescribeTable("refuses a path outside the page's folder",
		func(path string) {
			rec := s.do("alice", http.MethodGet, "/api/chats/pages/tasks/file?path="+q(path), nil)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
			Expect(rec.Body.String()).NotTo(ContainSubstring("outside"))
			rec = s.do("alice", http.MethodPut, "/api/chats/pages/tasks/file", map[string]any{"path": path, "content": "x"})
			Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
			rec = s.do("alice", http.MethodGet, "/api/chats/pages/tasks/files?dir="+q(path), nil)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), rec.Body.String())
		},
		Entry("parent", "../../secret.txt"),
		Entry("absolute", "/secret.txt"),
		Entry("backslash", `..\..\secret.txt`),
		Entry("control character", "tasks\x00.json"),
	)

	It("refuses a write to the page's own code, a large file and a bad name", func() {
		rec := s.do("alice", http.MethodPut, "/api/chats/pages/tasks/file", map[string]any{"path": "index.html", "content": "<p>x</p>"})
		Expect(rec.Code).To(Equal(http.StatusForbidden), rec.Body.String())
		rec = s.do("alice", http.MethodPut, "/api/chats/pages/tasks/file", map[string]any{"path": "big.txt", "content": strings.Repeat("x", 1<<20+1)})
		Expect(rec.Code).To(Equal(http.StatusRequestEntityTooLarge), rec.Body.String())
		Expect(s.do("alice", http.MethodGet, "/api/chats/pages/Tasks", nil).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodGet, "/api/chats/pages/nope", nil).Code).To(Equal(http.StatusNotFound))
	})

	It("refuses a write of code or a document from a page, while the Files route and data files work", func() {
		for _, path := range []string{"app.js", "x.MJS", "sub/style.css", "sub/index.htm", "icon.svg", "app.js.", "app%2ejs", "page.json"} {
			rec := s.do("alice", http.MethodPut, "/api/chats/pages/tasks/file", map[string]any{"path": path, "content": "alert(1)"})
			Expect(rec.Code).To(Equal(http.StatusForbidden), path+" "+rec.Body.String())
			Expect(rec.Body.String()).To(ContainSubstring("a page can write data files only"), path)
		}
		for _, path := range []string{"data.json", "notes/a.txt", "app.js.bak"} {
			rec := s.do("alice", http.MethodPut, "/api/chats/pages/tasks/file", map[string]any{"path": path, "content": "{}"})
			Expect(rec.Code).To(Equal(http.StatusOK), path+" "+rec.Body.String())
		}
		// The person, the Files page and the assistant's file tools write the same names.
		Expect(s.putFile("alice", "/pages/tasks/app.js", "console.log(1)").Code).To(Equal(http.StatusOK))
		Expect(s.putFile("alice", "/pages/tasks/style.css", "p{}").Code).To(Equal(http.StatusOK))
		rec := s.do("alice", http.MethodGet, "/api/chats/pages/tasks/file?path=app.js", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["content"]).To(Equal("console.log(1)"))
	})

	It("sends a page's message into the page's own chat, where it shows as a user message", func() {
		chat := s.create("alice")
		Expect(chat).NotTo(BeEmpty())
		rec := s.do("alice", http.MethodPost, "/api/chats/pages/tasks/chat", map[string]any{"text": "what is next?"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		out := decodeJSON(rec)
		pageChat := out["chat_id"].(string)
		Expect(pageChat).NotTo(Equal(chat))
		Expect(out["title"]).To(Equal("Page: Tasks board"))
		detail := s.detail("alice", pageChat)
		Expect(detail["origin"]).To(Equal("page"))
		Expect(detail["model"]).To(Equal("test-model"), "the model of the most recent chat")

		// The second message goes to the same chat.
		rec = s.do("alice", http.MethodPost, "/api/chats/pages/tasks/chat", map[string]any{"text": "and then?"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["chat_id"]).To(Equal(pageChat))
		Eventually(func() string {
			return s.do("alice", http.MethodGet, "/api/chats/"+pageChat+"/messages", nil).Body.String() +
				s.do("alice", http.MethodGet, "/api/chats/"+pageChat, nil).Body.String()
		}).Should(And(ContainSubstring(`From the page \"Tasks board\" (pages/tasks)`), ContainSubstring("and then?")))
	})

	It("sends to a chat the person picked, never to another user's", func() {
		chat := s.create("alice")
		rec := s.do("alice", http.MethodPost, "/api/chats/pages/tasks/chat", map[string]any{"text": "hi", "chat_id": chat})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["chat_id"]).To(Equal(chat))
		other := s.create("bob")
		Expect(s.do("alice", http.MethodPost, "/api/chats/pages/tasks/chat", map[string]any{"text": "hi", "chat_id": other}).Code).To(Equal(http.StatusNotFound))
	})

	It("asks for a model when there is no chat to take one from, and refuses empty or long text", func() {
		Expect(s.do("alice", http.MethodPost, "/api/chats/pages/tasks/chat", map[string]any{"text": "hi"}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPost, "/api/chats/pages/tasks/chat", map[string]any{"text": "hi", "model": "no-such-model"}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPost, "/api/chats/pages/tasks/chat", map[string]any{"text": " ", "model": "test-model"}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodPost, "/api/chats/pages/tasks/chat", map[string]any{"text": strings.Repeat("x", 4001), "model": "test-model"}).Code).To(Equal(http.StatusBadRequest))
		rec := s.do("alice", http.MethodPost, "/api/chats/pages/tasks/chat", map[string]any{"text": "hi", "model": "test-model"})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	})

	It("rate limits the messages of a page", func() {
		s.create("alice")
		codes := []int{}
		for i := 0; i < 6; i++ {
			codes = append(codes, s.do("alice", http.MethodPost, "/api/chats/pages/tasks/chat", map[string]any{"text": "hi"}).Code)
		}
		Expect(codes).To(ContainElement(http.StatusTooManyRequests))
	})

	It("lists the starters and copies one into pages/", func() {
		rec := s.do("bob", http.MethodGet, "/api/chats/page-starters", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["starters"]).NotTo(BeEmpty())
		rec = s.do("bob", http.MethodPost, "/api/chats/page-starters/task-board/copy", nil)
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(decodeJSON(rec)["name"]).To(Equal("task-board"))
		Expect(s.do("bob", http.MethodGet, "/api/chats/pages/task-board", nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("bob", http.MethodPost, "/api/chats/page-starters/nope/copy", nil).Code).To(Equal(http.StatusNotFound))
	})
})
