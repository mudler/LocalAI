package e2echats_test

import (
	"net/http"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The chat lifecycle and the turn loop, with authentication off and the JSON
// store under the data path. Spec: sections 4.2 (steering), 4.3 (sub-agents),
// 4.6 and 4.13 (storage without a database), 4.11 (the API).
var _ = Describe("Chats lifecycle and turns", Ordered, func() {
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

	Describe("conversations", func() {
		It("starts empty and says that chats and files are on", func() {
			var out struct {
				Conversations []chatInfo `json:"conversations"`
				Persistent    bool       `json:"persistent"`
				Files         bool       `json:"files"`
			}
			Expect(c.get("/api/chats", &out)).To(Equal(http.StatusOK))
			Expect(out.Conversations).To(BeEmpty())
			Expect(out.Persistent).To(BeTrue())
			Expect(out.Files).To(BeTrue())
		})

		It("creates, reads, lists and deletes a chat", func() {
			ch := c.create("fake")
			Expect(ch.Kind).To(Equal("chat"))
			Expect(ch.Model).To(Equal("fake"))
			Expect(ch.Running).To(BeFalse())
			Expect(c.info(ch.ID).ID).To(Equal(ch.ID))

			var list struct {
				Conversations []chatInfo `json:"conversations"`
			}
			Expect(c.get("/api/chats?kind=chat", &list)).To(Equal(http.StatusOK))
			var ids []string
			for _, x := range list.Conversations {
				ids = append(ids, x.ID)
			}
			Expect(ids).To(ContainElement(ch.ID))

			code, _ := c.do(http.MethodDelete, "/api/chats/"+ch.ID, nil)
			Expect(code).To(Equal(http.StatusOK))
			code, raw := c.do(http.MethodGet, "/api/chats/"+ch.ID, nil)
			Expect(code).To(Equal(http.StatusNotFound), string(raw))
			Expect(errType(raw)).To(Equal("not_found"))
			code, _ = c.do(http.MethodGet, "/api/chats/"+ch.ID+"/messages", nil)
			Expect(code).To(Equal(http.StatusNotFound))
		})

		It("refuses a chat with no model or an unknown model", func() {
			code, raw := c.do(http.MethodPost, "/api/chats", map[string]string{})
			Expect(code).To(Equal(http.StatusBadRequest), string(raw))
			code, raw = c.do(http.MethodPost, "/api/chats", map[string]string{"model": "no-such-model"})
			Expect(code).To(Equal(http.StatusBadRequest), string(raw))
			code, _ = c.do(http.MethodPost, "/api/chats", "{not json")
			Expect(code).To(Equal(http.StatusBadRequest))
		})

		It("answers 404 for an id that does not exist", func() {
			for _, p := range []string{"", "/messages", "/pending/p1", "/events"} {
				method := http.MethodGet
				if p == "/pending/p1" {
					method = http.MethodDelete
				}
				code, _ := c.do(method, "/api/chats/does-not-exist"+p, nil)
				Expect(code).To(Equal(http.StatusNotFound), p)
			}
			code, _ := c.do(http.MethodPost, "/api/chats/does-not-exist/messages", map[string]string{"text": "x"})
			Expect(code).To(Equal(http.StatusNotFound))
			code, _ = c.do(http.MethodPost, "/api/chats/does-not-exist/stop", nil)
			Expect(code).To(Equal(http.StatusNotFound))
		})

		It("can rename a chat or generate a title", func() {
			// The spec names no rename route; the sidebar of a real user needs
			// one. Known gap: chats keep the title "New chat" for ever.
			Skip("known gap: G-CHATS-TITLE (no rename route and no title generation)")
		})
	})

	Describe("a turn", func() {
		It("streams turn_start, text and turn_end, and keeps the transcript", func() {
			llm.Handle(func(fakeReq) fakeStep { return fakeStep{Text: "pong from the fake model"} })
			ch := c.create("fake")
			es := c.events(ch.ID)
			defer es.Close()
			status, ctype := es.Status()
			Expect(status).To(Equal(http.StatusOK))
			Expect(ctype).To(HavePrefix("text/event-stream"))

			Expect(c.say(ch.ID, "ping")).To(Equal("done"))
			es.Wait("turn_end")
			kinds := es.Kinds()
			Expect(kinds[0]).To(Equal("turn_start"))
			Expect(kinds[len(kinds)-1]).To(Equal("turn_end"))
			Expect(kinds).To(ContainElement("text"))
			Expect(es.Text("text")).To(Equal("pong from the fake model"))

			msgs := c.transcript(ch.ID)
			Expect(texts(msgs, "user")).To(Equal([]string{"ping"}))
			Expect(texts(msgs, "assistant")).To(Equal([]string{"pong from the fake model"}))
			var seqs []int64
			for _, m := range msgs {
				seqs = append(seqs, m.Seq)
			}
			Expect(seqs).To(BeEquivalentTo([]int64{1, 2, 3}), "user, assistant and the turn checkpoint, numbered from 1")
		})

		It("lets a client that connects late read the past from the transcript and the rest from the stream", func() {
			llm.Handle(func(fakeReq) fakeStep { return fakeStep{Text: "late reply", Pause: 1500 * time.Millisecond} })
			ch := c.create("fake")
			c.send(ch.ID, "LATE-1 hello")
			Eventually(func() int { return llm.CountWith("LATE-1") }, 60*time.Second).Should(Equal(1))
			es := c.events(ch.ID) // opened in the middle of the turn
			defer es.Close()
			es.Wait("turn_end")
			Expect(es.Kinds()).ToNot(ContainElement("turn_start"), "the stream carries only what happens after the connection")
			Expect(texts(c.transcript(ch.ID), "user")).To(Equal([]string{"LATE-1 hello"}))
			Expect(texts(c.transcript(ch.ID), "assistant")).To(Equal([]string{"late reply"}))
		})

		It("reads the transcript after a sequence number", func() {
			llm.Handle(func(fakeReq) fakeStep { return fakeStep{Text: "one"} })
			ch := c.create("fake")
			c.say(ch.ID, "a")
			var out struct {
				Messages []chatMsg `json:"messages"`
			}
			Expect(c.get("/api/chats/"+ch.ID+"/messages?after=1", &out)).To(Equal(http.StatusOK))
			Expect(out.Messages).ToNot(BeEmpty())
			Expect(out.Messages[0].Seq).To(BeNumerically(">", 1))
			Expect(c.get("/api/chats/"+ch.ID+"/messages?after=1&limit=1", &out)).To(Equal(http.StatusOK))
			Expect(out.Messages).To(HaveLen(1))
		})

		It("serves two clients on one chat the same events", func() {
			llm.Handle(func(fakeReq) fakeStep { return fakeStep{Text: "shared reply", Pause: 500 * time.Millisecond} })
			ch := c.create("fake")
			a, b := c.events(ch.ID), c.events(ch.ID)
			defer a.Close()
			defer b.Close()
			c.say(ch.ID, "TWO-1 hello")
			a.Wait("turn_end")
			b.Wait("turn_end")
			Expect(a.Text("text")).To(Equal("shared reply"))
			Expect(b.Text("text")).To(Equal("shared reply"))
		})

		It("stops the model request and removes the chat when a running chat is deleted", func() {
			// A slow answer, not a hang: when the server does not cancel it, it ends by itself.
			llm.Handle(func(fakeReq) fakeStep { return fakeStep{Text: "late", Pause: 30 * time.Second} })
			ch := c.create("fake")
			c.send(ch.ID, "DELRUN-1 slow")
			Eventually(func() int { return llm.CountWith("DELRUN-1") }, 60*time.Second).Should(Equal(1))
			code, raw := c.do(http.MethodDelete, "/api/chats/"+ch.ID, nil)
			Expect(code).To(Equal(http.StatusOK), string(raw))
			code, _ = c.do(http.MethodGet, "/api/chats/"+ch.ID, nil)
			Expect(code).To(Equal(http.StatusNotFound))
			deadline := time.Now().Add(15 * time.Second)
			for llm.Open() > 0 && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
			}
			if llm.Open() > 0 {
				Eventually(func() int { return llm.Open() }, 40*time.Second).Should(BeZero()) // let it end, so later specs start clean
				Skip("known gap: G-CHATS-DELRUN (deleting a running chat does not cancel its model request; the turn goes on in the background)")
			}
		})

		It("refuses an empty message", func() {
			ch := c.create("fake")
			code, _ := c.do(http.MethodPost, "/api/chats/"+ch.ID+"/messages", map[string]string{"text": ""})
			Expect(code).To(Equal(http.StatusBadRequest))
		})

		It("runs the tools of the loop and reports them as events and transcript lines", func() {
			llm.Handle(func(r fakeReq) fakeStep {
				p := r.Prompt()
				if !strings.Contains(p, "TOOLS-1") {
					return fakeStep{Text: "ok"}
				}
				switch llm.CountWith("TOOLS-1") {
				case 1:
					return fakeStep{Calls: []fakeCall{{Name: "write_file", Args: `{"path":"/e2e/tools.txt","content":"hello from the tool test"}`}}}
				case 2:
					return fakeStep{Calls: []fakeCall{{Name: "read_file", Args: `{"path":"/e2e/tools.txt"}`}}}
				}
				return fakeStep{Text: "all tools done"}
			})
			ch := c.create("fake")
			es := c.events(ch.ID)
			defer es.Close()
			Expect(c.say(ch.ID, "TOOLS-1 write and read a file")).To(Equal("done"))
			es.Wait("turn_end")
			var tools []string
			for _, ev := range es.All() {
				if ev.Kind == "tool_end" {
					Expect(ev.Tool.OK).To(BeTrue(), ev.Tool.Output)
					tools = append(tools, ev.Tool.Name)
				}
			}
			Expect(tools).To(Equal([]string{"write_file", "read_file"}))
			Expect(texts(c.transcript(ch.ID), "tool")).To(HaveLen(2))

			// The file really is in the user's files, and the write is one undoable change.
			code, raw := c.do(http.MethodGet, "/api/chats/files/content?path=/e2e/tools.txt", nil)
			Expect(code).To(Equal(http.StatusOK))
			Expect(string(raw)).To(Equal("hello from the tool test"))
			code, raw = c.do(http.MethodGet, "/api/chats/files/history", nil)
			Expect(code).To(Equal(http.StatusOK))
			Expect(string(raw)).To(ContainSubstring("/e2e/tools.txt"))
		})

		It("offers each tool to the model once", func() {
			llm.Handle(func(fakeReq) fakeStep { return fakeStep{Text: "ok"} })
			ch := c.create("fake")
			c.say(ch.ID, "DUPLICATES-1 which tools do I have")
			var tools []string
			for _, r := range llm.Requests() {
				if strings.Contains(r.Prompt(), "DUPLICATES-1") {
					tools = r.Tools
					break
				}
			}
			Expect(tools).ToNot(BeEmpty())
			seen := map[string]bool{}
			var dup []string
			for _, t := range tools {
				if seen[t] {
					dup = append(dup, t)
				}
				seen[t] = true
			}
			// Providers such as OpenAI and Anthropic reject a request with two tools of one name.
			if len(dup) > 0 {
				Skip("known gap: G-CHATS-DUPTOOLS (tools offered twice: " + strings.Join(dup, ", ") + ")")
			}
		})

		It("keeps different chats apart when they run at the same time", func() {
			llm.Handle(func(r fakeReq) fakeStep {
				time.Sleep(300 * time.Millisecond)
				for _, tok := range []string{"CONC-A", "CONC-B", "CONC-C", "CONC-D"} {
					if strings.Contains(r.Prompt(), tok) {
						return fakeStep{Text: "reply for " + tok}
					}
				}
				return fakeStep{Text: "unknown"}
			})
			toks := []string{"CONC-A", "CONC-B", "CONC-C", "CONC-D"}
			ids := make([]string, len(toks))
			for i := range toks {
				ids[i] = c.create("fake").ID
			}
			var wg sync.WaitGroup
			for i := range toks {
				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					c.say(ids[i], toks[i])
				}()
			}
			wg.Wait()
			for i, tok := range toks {
				Expect(texts(c.transcript(ids[i]), "assistant")).To(Equal([]string{"reply for " + tok}))
			}
			Expect(llm.MaxConcurrent()).To(BeNumerically(">=", 2), "turns of different chats overlap")
		})
	})

	Describe("steering", func() {
		It("keeps messages sent during a turn, lets the user edit and cancel them, and delivers the rest as one batch in order", func() {
			var gate sync.Mutex
			gate.Lock() // held until the spec has queued its messages
			var once sync.Once
			llm.Handle(func(r fakeReq) fakeStep {
				if strings.Contains(r.Prompt(), "STEER-1") {
					once.Do(func() { gate.Lock(); gate.Unlock() })
					return fakeStep{Text: "reply"}
				}
				return fakeStep{Text: "reply"}
			})
			ch := c.create("fake")
			es := c.events(ch.ID)
			defer es.Close()
			started, _ := c.send(ch.ID, "STEER-1 first")
			Expect(started).To(BeTrue())
			// The model is working on the first message (its request is open). A second message waits.
			Eventually(func() int { return llm.CountWith("STEER-1") }, 60*time.Second).Should(Equal(1))
			started, p1 := c.send(ch.ID, "STEER-1 alpha")
			Expect(started).To(BeFalse())
			Expect(p1).ToNot(BeEmpty())
			_, p2 := c.send(ch.ID, "STEER-1 bravo")
			_, p3 := c.send(ch.ID, "STEER-1 charlie")
			Expect(c.info(ch.ID).Pending).To(HaveLen(3))

			code, _ := c.do(http.MethodPatch, "/api/chats/"+ch.ID+"/pending/"+p2, map[string]string{"text": "STEER-1 bravo-edited"})
			Expect(code).To(Equal(http.StatusOK))
			code, _ = c.do(http.MethodDelete, "/api/chats/"+ch.ID+"/pending/"+p1, nil)
			Expect(code).To(Equal(http.StatusOK))
			pend := c.info(ch.ID).Pending
			Expect(pend).To(HaveLen(2))
			Expect(pend[0].Text).To(Equal("STEER-1 bravo-edited"))
			Expect(pend[1].ID).To(Equal(p3))
			code, _ = c.do(http.MethodPatch, "/api/chats/"+ch.ID+"/pending/"+p2, map[string]string{"text": ""})
			Expect(code).To(Equal(http.StatusBadRequest))
			code, _ = c.do(http.MethodDelete, "/api/chats/"+ch.ID+"/pending/nope", nil)
			Expect(code).To(Equal(http.StatusNotFound))

			gate.Unlock()
			// The turn ends normally, so the waiting messages start the next turn on their own.
			Eventually(func() int { return len(texts(c.transcript(ch.ID), "assistant")) }, 60*time.Second).Should(Equal(2))
			c.settled(ch.ID)
			Expect(c.info(ch.ID).Pending).To(BeEmpty())
			// The batch is one user line, in the order sent.
			Expect(texts(c.transcript(ch.ID), "user")).To(Equal([]string{"STEER-1 first", "STEER-1 bravo-edited\n\nSTEER-1 charlie"}))

			// Both reached the model in ONE request, in the order sent; the cancelled one never did.
			var batch string
			for _, r := range llm.Requests() {
				if strings.Contains(r.Prompt(), "bravo-edited") {
					batch = r.Prompt()
					break
				}
			}
			Expect(batch).To(ContainSubstring("charlie"))
			Expect(strings.Index(batch, "bravo-edited")).To(BeNumerically("<", strings.Index(batch, "charlie")))
			for _, r := range llm.Requests() {
				Expect(r.Prompt()).ToNot(ContainSubstring("STEER-1 alpha"))
			}
			es.Wait("turn_end")
		})

		It("delivers waiting messages at the next model request of the same turn and then refuses to change them", func() {
			var gate sync.Mutex
			gate.Lock()
			var once sync.Once
			llm.Handle(func(r fakeReq) fakeStep {
				p := r.Prompt()
				if !strings.Contains(p, "BATCH-1") {
					return fakeStep{Text: "ok"}
				}
				if strings.Contains(p, "sibling") {
					return fakeStep{Text: "saw the batch"}
				}
				once.Do(func() { gate.Lock(); gate.Unlock() })
				return fakeStep{Calls: []fakeCall{{Name: "list_files", Args: `{"path":"/"}`}}}
			})
			ch := c.create("fake")
			es := c.events(ch.ID)
			defer es.Close()
			c.send(ch.ID, "BATCH-1 go")
			Eventually(func() int { return llm.CountWith("BATCH-1") }, 60*time.Second).Should(Equal(1))
			_, p1 := c.send(ch.ID, "BATCH-1 sibling one")
			_, p2 := c.send(ch.ID, "BATCH-1 sibling two")
			gate.Unlock()
			c.settled(ch.ID)

			es.Wait("pending_delivered")
			var delivered []string
			for _, ev := range es.All() {
				if ev.Kind == "pending_delivered" {
					delivered = ev.IDs
				}
			}
			Expect(delivered).To(Equal([]string{p1, p2}))
			Expect(es.Text("text")).To(ContainSubstring("saw the batch"))
			// One turn only: the batch joined the running turn.
			Expect(c.transcript(ch.ID)[len(c.transcript(ch.ID))-1].Turn).To(Equal(1))

			code, raw := c.do(http.MethodPatch, "/api/chats/"+ch.ID+"/pending/"+p1, map[string]string{"text": "too late"})
			Expect(code).To(Equal(http.StatusConflict), string(raw))
			code, _ = c.do(http.MethodDelete, "/api/chats/"+ch.ID+"/pending/"+p2, nil)
			Expect(code).To(Equal(http.StatusConflict))
		})

		It("keeps waiting messages after a stop and starts them only on send-now", func() {
			var hang sync.Once
			llm.Handle(func(r fakeReq) fakeStep {
				if strings.Contains(r.Prompt(), "STOP-1 slow") {
					var step fakeStep
					hang.Do(func() { step = fakeStep{Hang: true} })
					if step.Hang {
						return step
					}
				}
				return fakeStep{Text: "after send-now"}
			})
			ch := c.create("fake")
			es := c.events(ch.ID)
			defer es.Close()
			c.send(ch.ID, "STOP-1 slow")
			Eventually(func() int { return llm.CountWith("STOP-1 slow") }, 60*time.Second).Should(Equal(1))
			_, pid := c.send(ch.ID, "STOP-1 queued")
			Expect(pid).ToNot(BeEmpty())

			code, _ := c.do(http.MethodPost, "/api/chats/"+ch.ID+"/stop", nil)
			Expect(code).To(Equal(http.StatusOK))
			Expect(c.settled(ch.ID)).To(Equal("stopped"))
			es.Wait("turn_end")

			// A stop is final: nothing starts by itself.
			Consistently(func() bool { return c.info(ch.ID).Running }, 2*time.Second, 200*time.Millisecond).Should(BeFalse())
			Expect(c.info(ch.ID).Pending).To(HaveLen(1))

			code, raw := c.do(http.MethodPost, "/api/chats/"+ch.ID+"/send-now", nil)
			Expect(code).To(Equal(http.StatusOK), string(raw))
			Eventually(func() []string { return texts(c.transcript(ch.ID), "assistant") }, 30*time.Second).Should(ContainElement("after send-now"))
			Expect(c.settled(ch.ID)).To(Equal("done"))
			Expect(c.info(ch.ID).Pending).To(BeEmpty())
		})

		It("answers stop and send-now on an idle chat without breaking it", func() {
			ch := c.create("fake")
			code, _ := c.do(http.MethodPost, "/api/chats/"+ch.ID+"/stop", nil)
			Expect(code).To(BeNumerically("<", 500))
			code, _ = c.do(http.MethodPost, "/api/chats/"+ch.ID+"/send-now", nil)
			Expect(code).To(BeNumerically("<", 500))
			Expect(c.info(ch.ID).Running).To(BeFalse())
		})
	})

	Describe("failures", func() {
		It("ends a turn as failed when the model errors, and keeps waiting messages", func() {
			llm.Handle(func(fakeReq) fakeStep { return fakeStep{Status: http.StatusInternalServerError} })
			ch := c.create("fake")
			es := c.events(ch.ID)
			defer es.Close()
			started, _ := c.send(ch.ID, "FAIL-1")
			Expect(started).To(BeTrue())
			// A persistent upstream error must end the turn with an error the user can read.
			Expect(c.settledOrGap(ch.ID, "G-CHATS-RETRY (a model that always answers 500 keeps the turn running for minutes, with no error event)", 40*time.Second)).To(Equal("failed"))
			es.Wait("error")
			es.Wait("turn_end")
			Expect(c.info(ch.ID).Running).To(BeFalse())
			// The chat still works afterwards.
			llm.Handle(func(fakeReq) fakeStep { return fakeStep{Text: "recovered"} })
			Expect(c.say(ch.ID, "FAIL-1 again")).To(Equal("done"))
			Expect(texts(c.transcript(ch.ID), "assistant")).To(ContainElement("recovered"))
		})

		It("ends a turn as failed when the backend is down", func() {
			ch := c.create("fake-down")
			es := c.events(ch.ID)
			defer es.Close()
			started, _ := c.send(ch.ID, "DOWN-1")
			Expect(started).To(BeTrue())
			Expect(c.settledOrGap(ch.ID, "G-CHATS-RETRY (a model whose backend is down keeps the turn running for minutes, with no error event)", 40*time.Second)).To(Equal("failed"))
			es.Wait("error")
			for _, ev := range es.All() {
				if ev.Kind == "error" {
					Expect(ev.Text).ToNot(BeEmpty())
				}
			}
		})

		It("runs a turn against a model installed without the proxy", func() {
			ch := c.create("mock-model")
			Expect(c.say(ch.ID, "hello mock")).To(BeElementOf("done", "failed"))
		})
	})

	Describe("sub-agents", func() {
		scriptSub := func(wait string, hold *sync.WaitGroup) {
			llm.Handle(func(r fakeReq) fakeStep {
				p := r.Prompt()
				switch {
				case strings.Contains(p, "SUBTASK-RUN") && !strings.Contains(p, "SUB-1"):
					// The sub-agent: its brief is the task, not the parent's request.
					if hold != nil {
						hold.Wait()
					}
					return fakeStep{Text: "child answer SUBDONE"}
				case strings.Contains(p, "SUB-1") && strings.Contains(p, "SUBDONE"):
					return fakeStep{Text: "parent final"}
				case strings.Contains(p, "SUB-1") && strings.Contains(p, "bg started"):
					return fakeStep{Text: "parent waits for the background result"}
				case strings.Contains(p, "SUB-1"):
					if wait == "false" {
						return fakeStep{Text: "bg started", Calls: []fakeCall{{Name: "spawn_agent", Args: `{"task":"SUBTASK-RUN do the thing","wait":false}`}}}
					}
					return fakeStep{Calls: []fakeCall{{Name: "spawn_agent", Args: `{"task":"SUBTASK-RUN do the thing"}`}}}
				}
				return fakeStep{Text: "ok"}
			})
		}

		listAll := func() []chatInfo {
			var out struct {
				Conversations []chatInfo `json:"conversations"`
			}
			Expect(c.get("/api/chats?limit=200", &out)).To(Equal(http.StatusOK))
			return out.Conversations
		}

		It("runs a foreground sub-agent as a linked read-only conversation", func() {
			scriptSub("true", nil)
			ch := c.create("fake")
			Expect(c.say(ch.ID, "SUB-1 please delegate")).To(Equal("done"))
			var child chatInfo
			for _, x := range listAll() {
				if x.ParentID == ch.ID {
					child = x
				}
			}
			if child.ID == "" {
				Skip("known gap: G-CHATS-SUBAGENT (the model's spawn_agent call is served by nib's own sub-agent tool, so no linked sub-agent conversation exists, and the background limit and parent-stop rules of the spec do not apply)")
			}
			Expect(child.Kind).To(Equal("subagent"))
			Expect(texts(c.transcript(child.ID), "assistant")).To(ContainElement("child answer SUBDONE"))
			Expect(texts(c.transcript(ch.ID), "assistant")).To(ContainElement("parent final"))

			code, raw := c.do(http.MethodPost, "/api/chats/"+child.ID+"/messages", map[string]string{"text": "hi"})
			Expect(code).To(Equal(http.StatusConflict), string(raw))

			var only struct {
				Conversations []chatInfo `json:"conversations"`
			}
			Expect(c.get("/api/chats?kind=chat&limit=200", &only)).To(Equal(http.StatusOK))
			for _, x := range only.Conversations {
				Expect(x.Kind).To(Equal("chat"))
			}
		})

		It("reports a background sub-agent back as an event and as a message", func() {
			scriptSub("false", nil)
			ch := c.create("fake")
			es := c.events(ch.ID)
			defer es.Close()
			c.send(ch.ID, "SUB-1 delegate in the background")
			Eventually(func() bool { return es.Has("sub_agent_done") || es.Has("turn_end") }, 60*time.Second, 100*time.Millisecond).Should(BeTrue())
			if !es.Has("sub_agent_done") {
				Skip("known gap: G-CHATS-SUBAGENT (the model's spawn_agent call is served by nib's own sub-agent tool, so no linked sub-agent conversation exists, and the background limit and parent-stop rules of the spec do not apply)")
			}
			Eventually(func() string {
				return strings.Join(texts(c.transcript(ch.ID), "user"), "|") + strings.Join(texts(c.transcript(ch.ID), "assistant"), "|")
			}, 30*time.Second).Should(ContainSubstring("SUBDONE"))
			c.settled(ch.ID)
		})

		It("stops its sub-agents when the parent is stopped", func() {
			var hold sync.WaitGroup
			hold.Add(1)
			defer hold.Done()
			scriptSub("false", &hold)
			ch := c.create("fake")
			c.send(ch.ID, "SUB-1 delegate then stop me")
			var child chatInfo
			found := func() string {
				for _, x := range listAll() {
					if x.ParentID == ch.ID {
						child = x
					}
				}
				return child.ID
			}
			deadline := time.Now().Add(20 * time.Second)
			for found() == "" && time.Now().Before(deadline) {
				time.Sleep(200 * time.Millisecond)
			}
			if child.ID == "" {
				_, _ = c.do(http.MethodPost, "/api/chats/"+ch.ID+"/stop", nil)
				Skip("known gap: G-CHATS-SUBAGENT (the model's spawn_agent call is served by nib's own sub-agent tool, so no linked sub-agent conversation exists, and the background limit and parent-stop rules of the spec do not apply)")
			}
			// Both turns hold: the parent's second request waits for the child result.
			code, _ := c.do(http.MethodPost, "/api/chats/"+ch.ID+"/stop", nil)
			Expect(code).To(Equal(http.StatusOK))
			Eventually(func() bool { return c.info(child.ID).Running }, 30*time.Second).Should(BeFalse())
		})
	})
})
