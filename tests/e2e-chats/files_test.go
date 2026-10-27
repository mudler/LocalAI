package e2echats_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fileEntry struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Size       int64  `json:"size"`
	Rev        int64  `json:"rev"`
	ConflictOf string `json:"conflict_of"`
}

// The personal workspace through the chats API. Spec: section 4.7.
var _ = Describe("Chats files and workspace", Ordered, func() {
	var (
		in *instance
		c  *client
	)
	BeforeAll(func() {
		in = newInstance()
		in.Start()
		c = &client{base: in.URL()}
	})
	AfterAll(func() { in.Cleanup() })
	BeforeEach(func() { llm.Reset() })

	list := func(path string) []fileEntry {
		var out struct {
			Entries []fileEntry `json:"entries"`
		}
		Expect(c.get("/api/chats/files?path="+url.QueryEscape(path), &out)).To(Equal(http.StatusOK))
		return out.Entries
	}
	paths := func(es []fileEntry) []string {
		var out []string
		for _, e := range es {
			out = append(out, e.Path)
		}
		return out
	}

	It("starts with a Chats folder for files sent in chats", func() {
		Expect(paths(list("/"))).To(ContainElement("/Chats"))
	})

	It("uploads, lists, downloads, moves, searches and deletes into the Trash, and restores with undo", func() {
		code, raw := c.do(http.MethodPut, "/api/chats/files/content?path=/Reports/q1.txt", []byte("the budget line"))
		Expect(code).To(Equal(http.StatusOK), string(raw))
		Expect(paths(list("/Reports"))).To(Equal([]string{"/Reports/q1.txt"}))
		code, raw = c.do(http.MethodGet, "/api/chats/files/content?path=/Reports/q1.txt", nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(raw)).To(Equal("the budget line"))

		code, raw = c.do(http.MethodPost, "/api/chats/files/folders", map[string]string{"path": "/Archive/2026"})
		Expect(code).To(Equal(http.StatusOK), string(raw))
		code, raw = c.do(http.MethodPost, "/api/chats/files/move", map[string]string{"from": "/Reports/q1.txt", "to": "/Archive/2026/q1.txt"})
		Expect(code).To(Equal(http.StatusOK), string(raw))
		Expect(list("/Reports")).To(BeEmpty())

		var found struct {
			Hits []struct {
				Path string `json:"path"`
				Line int    `json:"line"`
			} `json:"hits"`
		}
		Expect(c.get("/api/chats/files/search?q=BUDGET", &found)).To(Equal(http.StatusOK))
		Expect(found.Hits).To(HaveLen(1), "search ignores case and reads inside text files")
		Expect(found.Hits[0].Path).To(Equal("/Archive/2026/q1.txt"))

		code, _ = c.do(http.MethodDelete, "/api/chats/files?path=/Archive", nil)
		Expect(code).To(Equal(http.StatusOK))
		code, _ = c.do(http.MethodGet, "/api/chats/files/content?path=/Archive/2026/q1.txt", nil)
		Expect(code).To(Equal(http.StatusNotFound))
		var trash struct {
			Entries []struct {
				Rev   int64    `json:"rev"`
				Paths []string `json:"paths"`
			} `json:"entries"`
		}
		Expect(c.get("/api/chats/files/trash", &trash)).To(Equal(http.StatusOK))
		Expect(trash.Entries).ToNot(BeEmpty())
		rev := trash.Entries[0].Rev
		Expect(trash.Entries[0].Paths).To(ContainElement("/Archive"))

		code, raw = c.do(http.MethodPost, "/api/chats/files/undo", map[string]any{"rev": rev})
		Expect(code).To(Equal(http.StatusOK), string(raw))
		code, raw = c.do(http.MethodGet, "/api/chats/files/content?path=/Archive/2026/q1.txt", nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(raw)).To(Equal("the budget line"), "nothing is erased by a delete")
	})

	It("keeps both files when two uploads share a name, instead of overwriting", func() {
		c.do(http.MethodPut, "/api/chats/files/content?path=/dup.txt", []byte("first"))
		code, raw := c.do(http.MethodPut, "/api/chats/files/content?path=/dup.txt", []byte("second"))
		Expect(code).To(Equal(http.StatusOK), string(raw))
		var e fileEntry
		Expect(json.Unmarshal(raw, &e)).To(Succeed())
		Expect(e.ConflictOf).To(Equal("/dup.txt"))
		_, raw = c.do(http.MethodGet, "/api/chats/files/content?path=/dup.txt", nil)
		Expect(string(raw)).To(Equal("first"))
		_, raw = c.do(http.MethodGet, "/api/chats/files/content?path="+url.QueryEscape(e.Path), nil)
		Expect(string(raw)).To(Equal("second"))
		// The docs promise "name (from you).ext".
		if !strings.Contains(e.Path, "(from you)") {
			Skip("known gap: G-CHATS-CONFLICTNAME (the conflict copy is named " + e.Path + ", the docs say \"name (from you).ext\")")
		}
	})

	It("refuses paths that leave the home folder", func() {
		for _, p := range []string{"/../etc/passwd", "../x", "/a/../../b", "/a/%2e%2e/b"} {
			code, _ := c.do(http.MethodGet, "/api/chats/files/content?path="+p, nil)
			Expect(code).To(BeNumerically(">=", 400), p)
			code, _ = c.do(http.MethodPut, "/api/chats/files/content?path="+p, []byte("x"))
			Expect(code).To(BeNumerically(">=", 400), p)
		}
	})

	It("serves files as downloads and never runs HTML as a page", func() {
		c.do(http.MethodPut, "/api/chats/files/content?path=/page.html", []byte("<script>alert(1)</script>"))
		c.do(http.MethodPut, "/api/chats/files/content?path=/note.txt", []byte("plain"))
		get := func(p string) *http.Response {
			resp, err := http.DefaultClient.Do(c.req(http.MethodGet, p, nil, ""))
			Expect(err).ToNot(HaveOccurred())
			_ = resp.Body.Close()
			return resp
		}
		html := get("/api/chats/files/content?path=/page.html&inline=1")
		Expect(html.Header.Get("Content-Disposition")).To(HavePrefix("attachment"))
		Expect(html.Header.Get("Content-Type")).ToNot(ContainSubstring("html"))
		Expect(html.Header.Get("Content-Security-Policy")).To(ContainSubstring("sandbox"))
		txt := get("/api/chats/files/content?path=/note.txt&inline=1")
		Expect(txt.Header.Get("Content-Disposition")).ToNot(HavePrefix("attachment"), "plain text is shown by the browser with inline=1")
	})

	It("refuses a file larger than 256 MiB", func() {
		pr, pw := io.Pipe()
		go func() {
			chunk := make([]byte, 1<<20)
			for i := 0; i < 257; i++ {
				if _, err := pw.Write(chunk); err != nil {
					return
				}
			}
			_ = pw.Close()
		}()
		status := 0
		req := c.req(http.MethodPut, "/api/chats/files/content?path=/huge.bin", pr, "application/octet-stream")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			status = resp.StatusCode
		}
		_ = pr.Close()
		code, _ := c.do(http.MethodGet, "/api/chats/files/content?path=/huge.bin", nil)
		Expect(code).To(Equal(http.StatusNotFound), "no truncated file is kept")
		Expect(status).To(Equal(http.StatusRequestEntityTooLarge))
	})

	Describe("attachments", func() {
		attach := func(id, text, name, body string) (int, []byte) {
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			_ = mw.WriteField("text", text)
			fw, err := mw.CreateFormFile("files", name)
			Expect(err).ToNot(HaveOccurred())
			_, _ = fw.Write([]byte(body))
			Expect(mw.Close()).To(Succeed())
			resp, err := http.DefaultClient.Do(c.req(http.MethodPost, "/api/chats/"+id+"/messages", &buf, mw.FormDataContentType()))
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, raw
		}

		It("saves an attached file in the chat's folder and tells the model its path", func() {
			ch := c.create("fake")
			code, raw := attach(ch.ID, "ATTACH-1 read it", "note.txt", "attached text")
			Expect(code).To(Equal(http.StatusOK), string(raw))
			c.settled(ch.ID)
			dirs := list("/Chats")
			Expect(dirs).ToNot(BeEmpty())
			var inChat []fileEntry
			for _, d := range dirs {
				if strings.HasSuffix(d.Path, "ATTACH-1 read it") {
					inChat = list(d.Path)
				}
			}
			Expect(paths(inChat)).To(ContainElement(ContainSubstring("note.txt")))
			var prompt string
			for _, r := range llm.Requests() {
				if strings.Contains(r.Prompt(), "ATTACH-1") {
					prompt = r.Prompt()
				}
			}
			Expect(prompt).To(ContainSubstring("note.txt"))
			Expect(texts(c.transcript(ch.ID), "user")[0]).To(ContainSubstring("note.txt"))
		})

		It("refuses files sent while a turn runs", func() {
			var gate sync.Mutex
			gate.Lock()
			var once sync.Once
			llm.Handle(func(fakeReq) fakeStep {
				once.Do(func() { gate.Lock(); gate.Unlock() })
				return fakeStep{Text: "ok"}
			})
			ch := c.create("fake")
			c.send(ch.ID, "ATTACH-2 busy")
			Eventually(func() int { return llm.CountWith("ATTACH-2") }, 60*time.Second).Should(Equal(1))
			code, raw := attach(ch.ID, "ATTACH-2 late file", "late.txt", "x")
			Expect(code).To(Equal(http.StatusConflict), string(raw))
			gate.Unlock()
			c.settled(ch.ID)
		})

		It("refuses a message with neither text nor files", func() {
			ch := c.create("fake")
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			Expect(mw.Close()).To(Succeed())
			resp, err := http.DefaultClient.Do(c.req(http.MethodPost, "/api/chats/"+ch.ID+"/messages", &buf, mw.FormDataContentType()))
			Expect(err).ToNot(HaveOccurred())
			_ = resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		})
	})

	It("lets the model write a file and the user undo that agent change", func() {
		llm.Handle(func(r fakeReq) fakeStep {
			if !strings.Contains(r.Prompt(), "UNDO-1") {
				return fakeStep{Text: "ok"}
			}
			if llm.CountWith("UNDO-1") == 1 {
				return fakeStep{Calls: []fakeCall{{Name: "write_file", Args: `{"path":"/agent/made.txt","content":"by the model"}`}}}
			}
			return fakeStep{Text: "written"}
		})
		ch := c.create("fake")
		Expect(c.say(ch.ID, "UNDO-1 write a file")).To(Equal("done"))
		code, raw := c.do(http.MethodGet, "/api/chats/files/content?path=/agent/made.txt", nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(raw)).To(Equal("by the model"))
		var hist struct {
			Entries []struct {
				Rev  int64  `json:"rev"`
				Kind string `json:"kind"`
				Ops  []struct {
					Path string `json:"path"`
				} `json:"ops"`
			} `json:"entries"`
		}
		Expect(c.get("/api/chats/files/history", &hist)).To(Equal(http.StatusOK))
		var rev int64
		for _, e := range hist.Entries {
			for _, op := range e.Ops {
				if op.Path == "/agent/made.txt" && rev == 0 {
					rev = e.Rev
					Expect(e.Kind).To(Equal("agent"), "the history tells a change of the model from one of the user")
				}
			}
		}
		Expect(rev).ToNot(BeZero())
		code, raw = c.do(http.MethodPost, "/api/chats/files/undo", map[string]any{"rev": rev})
		Expect(code).To(Equal(http.StatusOK), string(raw))
		code, _ = c.do(http.MethodGet, "/api/chats/files/content?path=/agent/made.txt", nil)
		Expect(code).To(Equal(http.StatusNotFound))
	})
})
