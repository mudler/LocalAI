package storetest

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/chatsearch"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// runSearch registers the specs of the text search that every store passes.
func runSearch(newStore New) {
	Describe("text search", func() {
		var (
			s   store.Store
			idx chatsearch.Index
		)
		BeforeEach(func() {
			s = newStore()
			var ok bool
			idx, ok = s.(chatsearch.Index)
			Expect(ok).To(BeTrue(), "the store must implement chatsearch.Index")
		})

		say := func(id, role, text string, at time.Time) int64 {
			seq, err := s.AppendEntry(ctx(), id, session.Entry{Role: role, Text: text, At: at})
			Expect(err).NotTo(HaveOccurred())
			return seq
		}
		search := func(principal, text string, f ...func(*chatsearch.Query)) chatsearch.Result {
			q := chatsearch.Query{Principal: principal, Text: text, Titles: true, Now: t0.Add(24 * time.Hour)}
			for _, fn := range f {
				fn(&q)
			}
			// A backfill in the background may still run: wait for it.
			var res chatsearch.Result
			Eventually(func() bool {
				var err error
				res, err = idx.SearchText(ctx(), q)
				Expect(err).NotTo(HaveOccurred())
				return !res.Progress.Indexing
			}, "20s", "20ms").Should(BeTrue())
			return res
		}
		seqs := func(r chatsearch.Result) []string {
			var out []string
			for _, h := range r.Hits {
				out = append(out, fmt.Sprintf("%s/%d", h.Chat.ID, h.Seq))
			}
			return out
		}

		It("finds the words of user and assistant messages, ignoring case and accents of the script", func() {
			chat(s, "c1", "alice")
			say("c1", session.RoleUser, "What is the Qdrant port?", t0)
			a := say("c1", session.RoleAssistant, "The default port is 6333. Città di Roma", t0.Add(time.Minute))
			say("c1", session.RoleCompacted, "qdrant hidden", t0.Add(2*time.Minute))
			r := search("alice", "QDRANT port")
			Expect(seqs(r)).To(Equal([]string{"c1/1"}))
			Expect(r.Hits[0].Role).To(Equal(session.RoleUser))
			Expect(r.Hits[0].Snippet).To(ContainSubstring("Qdrant"))
			Expect(r.Hits[0].Highlights).NotTo(BeEmpty())
			Expect(r.Hits[0].At.Equal(t0)).To(BeTrue())
			Expect(seqs(search("alice", "città"))).To(Equal([]string{fmt.Sprintf("c1/%d", a)}))
		})

		It("matches every word in one message and a quoted phrase as written", func() {
			chat(s, "c1", "alice")
			say("c1", session.RoleUser, "the port of qdrant", t0)
			say("c1", session.RoleUser, "qdrant only", t0.Add(time.Minute))
			Expect(seqs(search("alice", "qdrant port"))).To(Equal([]string{"c1/1"}))
			Expect(search("alice", `"qdrant port"`).Hits).To(BeEmpty())
			Expect(seqs(search("alice", `"port of qdrant"`))).To(Equal([]string{"c1/1"}))
		})

		It("finds words shorter than the trigram size", func() {
			chat(s, "c1", "alice")
			say("c1", session.RoleUser, "go is fun", t0)
			say("c1", session.RoleUser, "nothing", t0.Add(time.Minute))
			Expect(seqs(search("alice", "go"))).To(Equal([]string{"c1/1"}))
			Expect(seqs(search("alice", "go fun"))).To(Equal([]string{"c1/1"}))
		})

		It("treats SQL and index syntax in the query as text", func() {
			chat(s, "c1", "alice")
			say("c1", session.RoleUser, `a 100% sure, a_b, he said ab"c and back\slash`, t0)
			say("c1", session.RoleUser, "a 1000 sure aXb abc", t0.Add(time.Minute))
			Expect(seqs(search("alice", "100%"))).To(Equal([]string{"c1/1"}))
			Expect(seqs(search("alice", "a_b"))).To(Equal([]string{"c1/1"}))
			Expect(seqs(search("alice", `ab"c`))).To(Equal([]string{"c1/1"}))
			Expect(seqs(search("alice", `back\slash`))).To(Equal([]string{"c1/1"}))
			Expect(search("alice", "NEAR(a b)").Hits).To(BeEmpty())
			Expect(search("alice", "a OR zzz").Hits).To(BeEmpty())
		})

		It("never shows another user's chats, nor reveals them", func() {
			chat(s, "ca", "alice")
			chat(s, "cb", "bob")
			say("ca", session.RoleUser, "shared word zebra", t0)
			say("cb", session.RoleUser, "shared word zebra secret", t0)
			Expect(seqs(search("alice", "zebra"))).To(Equal([]string{"ca/1"}))
			Expect(seqs(search("bob", "zebra"))).To(Equal([]string{"cb/1"}))
			Expect(search("alice", "secret").Hits).To(BeEmpty())
			Expect(search("carol", "zebra").Hits).To(BeEmpty())
			Expect(search("alice", "zebra", func(q *chatsearch.Query) { q.ChatID = "cb" }).Hits).To(BeEmpty())
		})

		It("leaves out sub-agent chats", func() {
			chat(s, "parent", "alice")
			Expect(s.CreateChat(ctx(), session.ChatRecord{ID: "sub", Principal: "alice", Title: "sub", Kind: session.KindSubAgent,
				ParentID: "parent", Created: t0, Updated: t0})).To(Succeed())
			say("sub", session.RoleAssistant, "findings about zebra", t0)
			Expect(search("alice", "zebra").Hits).To(BeEmpty())
		})

		It("drops a deleted chat from the index at once", func() {
			chat(s, "c1", "alice")
			say("c1", session.RoleUser, "unique-giraffe-word", t0)
			Expect(search("alice", "unique-giraffe-word").Hits).To(HaveLen(1))
			Expect(s.DeleteChat(ctx(), "alice", "c1")).To(Succeed())
			Expect(search("alice", "unique-giraffe-word").Hits).To(BeEmpty())
			// A new chat with the same ID starts clean.
			chat(s, "c1", "alice")
			say("c1", session.RoleUser, "other words", t0)
			Expect(search("alice", "unique-giraffe-word").Hits).To(BeEmpty())
		})

		It("drops truncated text and indexes the text written in its place", func() {
			chat(s, "c1", "alice")
			say("c1", session.RoleUser, "question one", t0)
			say("c1", session.RoleAssistant, "old answer about pelicans", t0.Add(time.Minute))
			Expect(s.TruncateEntries(ctx(), "alice", "c1", 1)).To(Succeed())
			Expect(search("alice", "pelicans").Hits).To(BeEmpty())
			say("c1", session.RoleAssistant, "new answer about herons", t0.Add(2*time.Minute))
			Expect(seqs(search("alice", "herons"))).To(Equal([]string{"c1/2"}))
			Expect(search("alice", "pelicans").Hits).To(BeEmpty())
		})

		It("indexes a batch of entries", func() {
			chat(s, "c1", "alice")
			Expect(s.AppendEntries(ctx(), "c1", []session.Entry{
				{Role: session.RoleUser, Text: "first lemur", At: t0},
				{Role: session.RoleAssistant, Text: "second lemur"},
			})).To(Succeed())
			Expect(seqs(search("alice", "lemur"))).To(ConsistOf("c1/1", "c1/2"))
		})

		It("leaves out the list of files of an older message", func() {
			chat(s, "c1", "alice")
			say("c1", session.RoleUser, "see this\n\nThe user attached these files:\n- /workspace/home/Chats/x/a.txt", t0)
			Expect(search("alice", "attached these files").Hits).To(BeEmpty())
			Expect(search("alice", "see this").Hits).To(HaveLen(1))
		})

		It("matches titles, and returns a title-only chat without a line", func() {
			Expect(s.CreateChat(ctx(), session.ChatRecord{ID: "c1", Principal: "alice", Title: "Kubernetes upgrade", Created: t0, Updated: t0})).To(Succeed())
			r := search("alice", "kubernetes")
			Expect(r.Hits).To(HaveLen(1))
			Expect(r.Hits[0].InTitle).To(BeTrue())
			Expect(r.Hits[0].Seq).To(BeZero())
			Expect(search("alice", "kubernetes", func(q *chatsearch.Query) { q.Titles = false }).Hits).To(BeEmpty())
		})

		It("filters by agent, origin, role and time", func() {
			Expect(s.CreateChat(ctx(), session.ChatRecord{ID: "web", Principal: "alice", Title: "w", Created: t0, Updated: t0})).To(Succeed())
			Expect(s.CreateChat(ctx(), session.ChatRecord{ID: "ag", Principal: "alice", Title: "a", AgentID: "helper", Created: t0, Updated: t0})).To(Succeed())
			Expect(s.CreateChat(ctx(), session.ChatRecord{ID: "tr", Principal: "alice", Title: "t", Origin: "trigger", OriginID: "t1", Created: t0, Updated: t0})).To(Succeed())
			Expect(s.CreateChat(ctx(), session.ChatRecord{ID: "cn", Principal: "alice", Title: "c", Origin: "connector", OriginID: "b1", Created: t0, Updated: t0})).To(Succeed())
			for _, id := range []string{"web", "ag", "tr", "cn"} {
				say(id, session.RoleUser, "walrus question", t0)
				say(id, session.RoleAssistant, "walrus answer", t0.Add(48*time.Hour))
			}
			with := func(f func(*chatsearch.Query)) []string {
				r := search("alice", "walrus", f, func(q *chatsearch.Query) { q.Titles = false })
				var ids []string
				for _, h := range r.Hits {
					ids = append(ids, fmt.Sprintf("%s/%d", h.Chat.ID, h.Seq))
				}
				return ids
			}
			Expect(with(func(q *chatsearch.Query) { q.AgentID = "helper" })).To(ConsistOf("ag/1", "ag/2"))
			Expect(with(func(q *chatsearch.Query) { q.Origin = chatsearch.OriginPerson })).To(ConsistOf("web/1", "web/2", "ag/1", "ag/2"))
			Expect(with(func(q *chatsearch.Query) { q.Origin = chatsearch.OriginAutomated })).To(ConsistOf("tr/1", "tr/2", "cn/1", "cn/2"))
			Expect(with(func(q *chatsearch.Query) { q.Origin = "connector" })).To(ConsistOf("cn/1", "cn/2"))
			Expect(with(func(q *chatsearch.Query) { q.Role = session.RoleAssistant; q.ChatID = "web" })).To(ConsistOf("web/2"))
			Expect(with(func(q *chatsearch.Query) { q.Since = t0.Add(24 * time.Hour); q.ChatID = "web" })).To(ConsistOf("web/2"))
			Expect(with(func(q *chatsearch.Query) { q.Until = t0.Add(24 * time.Hour); q.ChatID = "web" })).To(ConsistOf("web/1"))
		})

		It("keeps one hit per chat when asked, and ranks the newer chat first on equal text", func() {
			chat(s, "old", "alice")
			chat(s, "new", "alice")
			say("old", session.RoleUser, "ocelot", t0)
			say("new", session.RoleUser, "ocelot", t0.Add(10*24*time.Hour))
			say("new", session.RoleAssistant, "another ocelot", t0.Add(10*24*time.Hour))
			r := search("alice", "ocelot", func(q *chatsearch.Query) { q.PerChat = true })
			Expect(r.Hits).To(HaveLen(2))
			Expect(r.Hits[0].Chat.ID).To(Equal("new"))
			Expect(r.Hits[0].Matches).To(Equal(2))
		})

		It("searches every chat, not only the newest few hundred", func() {
			chat(s, "oldest", "alice")
			say("oldest", session.RoleUser, "the one platypus", t0)
			for i := range 520 {
				id := fmt.Sprintf("n%03d", i)
				Expect(s.CreateChat(ctx(), session.ChatRecord{ID: id, Principal: "alice", Title: id, Created: t0, Updated: t0.Add(time.Duration(i+1) * time.Minute)})).To(Succeed())
				say(id, session.RoleUser, "filler "+strings.Repeat("x", 5), t0.Add(time.Duration(i+1)*time.Minute))
			}
			Expect(seqs(search("alice", "platypus"))).To(Equal([]string{"oldest/1"}))
		})

		It("bounds the number of hits and cuts the snippet", func() {
			chat(s, "c1", "alice")
			long := strings.Repeat("blah ", 100) + "needle" + strings.Repeat(" blah", 100)
			for i := range 8 {
				say("c1", session.RoleUser, long, t0.Add(time.Duration(i)*time.Minute))
			}
			r := search("alice", "needle", func(q *chatsearch.Query) { q.Limit = 3; q.SnippetRunes = 100 })
			Expect(r.Hits).To(HaveLen(3))
			Expect(len([]rune(r.Hits[0].Snippet))).To(BeNumerically("<=", 102))
		})
	})
}
