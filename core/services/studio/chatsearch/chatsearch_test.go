package chatsearch_test

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/chatsearch"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func rec(id, title string) session.ChatRecord {
	return session.ChatRecord{ID: id, Principal: "alice", Title: title, Updated: now}
}

func cand(c session.ChatRecord, seq int64, role, text string, age time.Duration) chatsearch.Candidate {
	return chatsearch.Candidate{Chat: c, Seq: seq, Role: role, Text: text, At: now.Add(-age)}
}

var _ = Describe("Terms", func() {
	It("folds, splits and keeps a quoted phrase as one word", func() {
		Expect(chatsearch.Terms(`  Qdrant "Port Number" qdrant `)).To(Equal([]string{"qdrant", "port number"}))
	})
	It("caps the number and the length of the words", func() {
		Expect(chatsearch.Terms("a b c d e f g h")).To(HaveLen(chatsearch.MaxTerms))
		Expect([]rune(chatsearch.Terms(strings.Repeat("x", 500))[0])).To(HaveLen(chatsearch.MaxTermRunes))
	})
	It("returns nothing for blank text", func() {
		Expect(chatsearch.Terms("   ")).To(BeEmpty())
	})
})

var _ = Describe("IndexText", func() {
	It("skips lines that are not messages", func() {
		_, ok := chatsearch.IndexText(session.RoleCompacted, "x", false)
		Expect(ok).To(BeFalse())
	})
	It("leaves out the list of files of an old message", func() {
		txt, ok := chatsearch.IndexText(session.RoleUser, "see this\n\nThe user attached these files:\n- /workspace/home/a", false)
		Expect(ok).To(BeTrue())
		Expect(txt).To(Equal("see this"))
	})
})

var _ = Describe("Build", func() {
	a, b := rec("a", "Qdrant setup"), rec("b", "Other")
	q := chatsearch.Query{Principal: "alice", Text: "qdrant port", Now: now, Titles: true}

	It("needs every word in the message", func() {
		hits := chatsearch.Build(q, []chatsearch.Candidate{
			cand(a, 1, "user", "the qdrant port is 6333", time.Hour),
			cand(a, 2, "user", "only qdrant here", time.Hour),
		}, nil)
		Expect(hits).To(HaveLen(1))
		Expect(hits[0].Seq).To(Equal(int64(1)))
	})

	It("ranks a whole-word phrase above a newer partial match", func() {
		hits := chatsearch.Build(q, []chatsearch.Candidate{
			cand(b, 1, "user", "xqdrantx and reporting", time.Hour),
			cand(a, 1, "assistant", "the Qdrant port is 6333", 20*24*time.Hour),
		}, nil)
		Expect(hits).To(HaveLen(2))
		Expect(hits[0].Chat.ID).To(Equal("a"))
	})

	It("prefers the newer message when quality is equal", func() {
		hits := chatsearch.Build(q, []chatsearch.Candidate{
			cand(a, 1, "user", "qdrant port", 40*24*time.Hour),
			cand(b, 1, "user", "qdrant port", time.Hour),
		}, nil)
		Expect(hits[0].Chat.ID).To(Equal("b"))
	})

	It("keeps one hit per chat when asked and counts the matches", func() {
		pq := q
		pq.PerChat = true
		hits := chatsearch.Build(pq, []chatsearch.Candidate{
			cand(a, 1, "user", "qdrant port", time.Hour),
			cand(a, 3, "user", "qdrant port again", time.Hour),
		}, nil)
		Expect(hits).To(HaveLen(1))
		Expect(hits[0].Matches).To(Equal(2))
	})

	It("returns a chat whose title matches and has no matching message", func() {
		tq := q
		tq.Text = "qdrant"
		hits := chatsearch.Build(tq, nil, []session.ChatRecord{a})
		Expect(hits).To(HaveLen(1))
		Expect(hits[0].InTitle).To(BeTrue())
		Expect(hits[0].Seq).To(BeZero())
	})

	It("filters by role, time, agent and origin", func() {
		x := rec("x", "t")
		x.AgentID, x.Origin = "ag", "trigger"
		cs := []chatsearch.Candidate{
			cand(x, 1, "user", "qdrant port", 2*time.Hour),
			cand(x, 2, "assistant", "qdrant port", 48*time.Hour),
		}
		with := func(f func(*chatsearch.Query)) []chatsearch.Hit {
			qq := q
			f(&qq)
			return chatsearch.Build(qq, cs, nil)
		}
		Expect(with(func(qq *chatsearch.Query) { qq.Role = "assistant" })).To(HaveLen(1))
		Expect(with(func(qq *chatsearch.Query) { qq.Since = now.Add(-24 * time.Hour) })).To(HaveLen(1))
		Expect(with(func(qq *chatsearch.Query) { qq.Until = now.Add(-24 * time.Hour) })).To(HaveLen(1))
		Expect(with(func(qq *chatsearch.Query) { qq.AgentID = "other" })).To(BeEmpty())
		Expect(with(func(qq *chatsearch.Query) { qq.Origin = chatsearch.OriginPerson })).To(BeEmpty())
		Expect(with(func(qq *chatsearch.Query) { qq.Origin = chatsearch.OriginAutomated })).To(HaveLen(2))
		Expect(with(func(qq *chatsearch.Query) { qq.Origin = "trigger" })).To(HaveLen(2))
		Expect(with(func(qq *chatsearch.Query) { qq.Origin = "connector" })).To(BeEmpty())
	})

	It("never returns another principal's chat, a sub-agent chat or an excluded chat", func() {
		other := rec("o", "t")
		other.Principal = "bob"
		sub := rec("s", "t")
		sub.Kind = session.KindSubAgent
		priv := rec("p", "t")
		qq := q
		qq.Exclude = func(r session.ChatRecord) bool { return r.ID == "p" }
		Expect(chatsearch.Build(qq, []chatsearch.Candidate{
			cand(other, 1, "user", "qdrant port", time.Hour),
			cand(sub, 1, "user", "qdrant port", time.Hour),
			cand(priv, 1, "user", "qdrant port", time.Hour),
		}, nil)).To(BeEmpty())
	})

	It("cuts a snippet with highlight offsets in UTF-16 units", func() {
		text := "😀 prefix " + strings.Repeat("filler ", 40) + "the Qdrant port is 6333 " + strings.Repeat("tail ", 40)
		hits := chatsearch.Build(q, []chatsearch.Candidate{cand(a, 1, "user", text, time.Hour)}, nil)
		Expect(hits).To(HaveLen(1))
		h := hits[0]
		Expect(h.Snippet).To(HavePrefix("…"))
		Expect(h.Snippet).To(HaveSuffix("…"))
		Expect(len([]rune(h.Snippet))).To(BeNumerically("<=", chatsearch.DefaultSnippetRunes+2))
		Expect(h.Highlights).To(HaveLen(2))
		units := []rune(h.Snippet) // the snippet has no astral rune here
		got := []string{}
		for _, m := range h.Highlights {
			got = append(got, string(units[m.Start:m.End]))
		}
		Expect(got).To(Equal([]string{"Qdrant", "port"}))
	})

	It("counts an astral rune as two units in the offsets", func() {
		hits := chatsearch.Build(chatsearch.Query{Principal: "alice", Text: "needle", Now: now},
			[]chatsearch.Candidate{cand(a, 1, "user", "😀 needle", time.Hour)}, nil)
		Expect(hits[0].Highlights).To(Equal([]chatsearch.Range{{Start: 3, End: 9}}))
	})

	It("limits the hits", func() {
		var cs []chatsearch.Candidate
		for i := range 5 {
			cs = append(cs, cand(a, int64(i+1), "user", "qdrant port", time.Duration(i)*time.Hour))
		}
		qq := q
		qq.Limit = 2
		Expect(chatsearch.Build(qq, cs, nil)).To(HaveLen(2))
	})
})
