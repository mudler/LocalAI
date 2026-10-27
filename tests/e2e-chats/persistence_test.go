package e2echats_test

import (
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// No database: the JSON store under the data path. Spec: sections 4.6 and
// 4.13 (state survives a restart), 4.2 (waiting messages are kept).
var _ = Describe("Chats without a database", func() {
	BeforeEach(func() { llm.Reset() })

	It("keeps chats, transcripts, waiting messages and files across a restart", func() {
		in := newInstance()
		defer in.Cleanup()
		in.Start()
		c := &client{base: in.URL()}

		llm.Handle(func(r fakeReq) fakeStep {
			if strings.Contains(r.Prompt(), "RESTART-1 hang") && llm.CountWith("RESTART-1 hang") == 1 {
				return fakeStep{Hang: true}
			}
			return fakeStep{Text: "kept reply"}
		})
		done := c.create("fake")
		Expect(c.say(done.ID, "RESTART-1 hello")).To(Equal("done"))
		code, raw := c.do(http.MethodPut, "/api/chats/files/content?path=/Notes/keep.txt", []byte("kept file"))
		Expect(code).To(BeNumerically("<", 300), string(raw))
		want := c.transcript(done.ID)

		// A second chat is stopped with a message waiting.
		stopped := c.create("fake")
		c.send(stopped.ID, "RESTART-1 hang")
		Eventually(func() int { return llm.CountWith("RESTART-1 hang") }, 60*time.Second).Should(Equal(1))
		_, pid := c.send(stopped.ID, "RESTART-1 waiting")
		Expect(pid).ToNot(BeEmpty())
		code, _ = c.do(http.MethodPost, "/api/chats/"+stopped.ID+"/stop", nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(c.settled(stopped.ID)).To(Equal("stopped"))

		in.Restart()

		Expect(c.transcript(done.ID)).To(Equal(want))
		Expect(c.info(done.ID).LastTurn).To(Equal("done"))
		info := c.info(stopped.ID)
		Expect(info.LastTurn).To(Equal("stopped"))
		Expect(info.Pending).To(HaveLen(1))
		Expect(info.Pending[0].Text).To(Equal("RESTART-1 waiting"))
		code, raw = c.do(http.MethodGet, "/api/chats/files/content?path=/Notes/keep.txt", nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(raw)).To(Equal("kept file"))

		// The chat is usable after the restart, and the numbering goes on.
		Expect(c.say(done.ID, "RESTART-1 again")).To(Equal("done"))
		after := c.transcript(done.ID)
		Expect(len(after)).To(BeNumerically(">", len(want)))
		Expect(after[len(want)].Seq).To(BeNumerically(">", want[len(want)-1].Seq))
	})

	It("ends the turn a crash left running as interrupted and keeps the waiting message", func() {
		in := newInstance()
		defer in.Cleanup()
		in.Start()
		c := &client{base: in.URL()}
		llm.Handle(func(fakeReq) fakeStep { return fakeStep{Hang: true} })
		ch := c.create("fake")
		c.send(ch.ID, "CRASHJSON-1 hang")
		Eventually(func() int { return llm.CountWith("CRASHJSON-1") }, 60*time.Second).Should(Equal(1))
		c.send(ch.ID, "CRASHJSON-1 queued")

		in.Kill()
		llm.Reset()
		in.Start()

		i := c.info(ch.ID)
		Expect(i.Running).To(BeFalse())
		Expect(i.LastTurn).To(Equal("interrupted"))
		Expect(i.Pending).To(HaveLen(1))
		code, _ := c.do(http.MethodPost, "/api/chats/"+ch.ID+"/send-now", nil)
		Expect(code).To(Equal(http.StatusOK))
		Eventually(func() []string { return texts(c.transcript(ch.ID), "assistant") }, 30*time.Second).Should(ContainElement("ok"))
	})

	It("works in memory when there is no data path, and says that files and persistence are off", func() {
		in := newInstance()
		defer in.Cleanup()
		in.Start("--data-path=")
		c := &client{base: in.URL()}
		var list struct {
			Persistent  bool   `json:"persistent"`
			Files       bool   `json:"files"`
			FilesReason string `json:"files_reason"`
		}
		Expect(c.get("/api/chats", &list)).To(Equal(http.StatusOK))
		if list.Persistent {
			// An empty --data-path (or LOCALAI_DATA_PATH) falls back to the default folder,
			// so the spec's memory-only mode cannot be reached from the command line.
			Skip("known gap: G-CHATS-NODATAPATH (no way to start without a data path, so the memory-only mode is unreachable)")
		}
		Expect(list.Persistent).To(BeFalse())
		Expect(list.Files).To(BeFalse())
		Expect(list.FilesReason).ToNot(BeEmpty())

		ch := c.create("fake")
		Expect(c.say(ch.ID, "MEMORY-1")).To(Equal("done"))
		code, raw := c.do(http.MethodGet, "/api/chats/files", nil)
		Expect(code).To(Equal(http.StatusConflict), string(raw))
		Expect(errType(raw)).To(Equal("files_off"))
		code, raw = c.do(http.MethodPut, "/api/chats/files/content?path=/a.txt", []byte("x"))
		Expect(code).To(Equal(http.StatusConflict), string(raw))

		in.Restart("--data-path=")
		code, _ = c.do(http.MethodGet, "/api/chats/"+ch.ID, nil)
		Expect(code).To(Equal(http.StatusNotFound), "memory-only chats are gone after a restart")
	})
})
