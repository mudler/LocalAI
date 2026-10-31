// Package storetest is the conformance suite for store.Store. Every
// implementation passes it, so the JSON and GORM stores behave the same. Run
// registers Ginkgo specs, so call it inside a Describe of the implementation's
// test package.
package storetest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// New returns an empty store for one spec. Clean up with DeferCleanup.
type New func() store.Store

var t0 = time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)

// ctx returns a context that is cancelled when the spec ends.
func ctx() context.Context {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	DeferCleanup(cancel)
	return c
}

func chat(s store.Store, id, principal string) {
	Expect(s.CreateChat(ctx(), session.ChatRecord{ID: id, Principal: principal, Title: "New chat", Created: t0, Updated: t0})).To(Succeed())
}

func texts(ps []store.Pending) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Text)
	}
	return out
}

// Run registers the specs that check stores from newStore.
func Run(newStore New) {
	// The loop's own transcript contract.
	sessiontest.RunStoreSuite(func() session.Store { return newStore() })

	It("skill switches round-trip, survive a chat update and belong to the owner", func() {
		s := newStore()
		chat(s, "c1", "alice")
		Expect(s.SetSkillsOff(ctx(), "alice", "c1", []string{"review", "notes"})).To(Succeed())
		rec, err := s.GetChat(ctx(), "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.SkillsOff).To(Equal([]string{"review", "notes"}))
		// The loop saves its own copy of the record at the end of a turn. That
		// copy has no switches in it and must not wipe them.
		stale := rec
		stale.SkillsOff = nil
		stale.Title, stale.Updated = "Renamed", t0.Add(time.Minute)
		Expect(s.UpdateChat(ctx(), stale)).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.Title).To(Equal("Renamed"))
		Expect(rec.SkillsOff).To(HaveLen(2), "want the switches kept")
		Expect(s.SetSkillsOff(ctx(), "bob", "c1", []string{"x"})).To(MatchError(session.ErrNotFound), "another principal's SetSkillsOff")
		Expect(s.SetSkillsOff(ctx(), "alice", "c1", nil)).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.SkillsOff).To(BeEmpty(), "after clearing")
	})

	It("children of a chat belong to the owner and the parent", func() {
		s := newStore()
		chat(s, "p1", "alice")
		chat(s, "p2", "alice")
		for i, spec := range []struct{ id, owner, parent string }{{"k1", "alice", "p1"}, {"k2", "alice", "p1"}, {"k3", "alice", "p2"}, {"k4", "bob", "p1"}} {
			rec := session.ChatRecord{ID: spec.id, Principal: spec.owner, Title: "child", Kind: session.KindSubAgent, ParentID: spec.parent,
				Created: t0.Add(time.Duration(i) * time.Second), Updated: t0}
			Expect(s.CreateChat(ctx(), rec)).To(Succeed())
		}
		kids, err := s.ListChildren(ctx(), "alice", "p1")
		Expect(err).NotTo(HaveOccurred())
		Expect(kids).To(HaveLen(2), "want k1 and k2: %+v", kids)
		Expect(kids[0].ID).To(Equal("k1"))
		Expect(kids[1].ID).To(Equal("k2"))
		kids, _ = s.ListChildren(ctx(), "alice", "nothing")
		Expect(kids).To(BeEmpty(), "children of an unknown parent")
	})

	It("chat meta and truncation", func() {
		s := newStore()
		chat(s, "c1", "alice")
		for _, txt := range []string{"a", "b", "c"} {
			Expect(s.AppendEntry(ctx(), "c1", session.Entry{Role: session.RoleUser, Text: txt})).Error().NotTo(HaveOccurred())
		}
		title, model := "Renamed", "m2"
		set := session.ChatSettings{SystemPrompt: "be brief", ReasoningEffort: "low", ContextSize: 4096}
		temp := 0.3
		set.Temperature = &temp
		Expect(s.SetChatMeta(ctx(), "alice", "c1", session.ChatMeta{Title: &title, Model: &model, Settings: &set})).To(Succeed())
		rec, _ := s.GetChat(ctx(), "alice", "c1")
		Expect(rec.Title).To(Equal("Renamed"), "after SetChatMeta: %+v", rec)
		Expect(rec.Model).To(Equal("m2"))
		Expect(rec.Settings.SystemPrompt).To(Equal("be brief"))
		Expect(rec.Settings.Temperature).NotTo(BeNil())
		Expect(*rec.Settings.Temperature).To(Equal(0.3))
		Expect(rec.Settings.ContextSize).To(BeEquivalentTo(4096))
		// The loop's stale copy must not wipe the settings.
		stale := rec
		stale.Settings = session.ChatSettings{}
		stale.Updated = t0.Add(time.Hour)
		Expect(s.UpdateChat(ctx(), stale)).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.Settings.SystemPrompt).To(Equal("be brief"), "UpdateChat wiped the settings or the model: %+v", rec)
		Expect(rec.Model).To(Equal("m2"), "UpdateChat wiped the settings or the model: %+v", rec)
		Expect(s.SetChatMeta(ctx(), "bob", "c1", session.ChatMeta{Title: &title})).To(MatchError(session.ErrNotFound), "another principal's SetChatMeta")
		empty := session.ChatSettings{}
		Expect(s.SetChatMeta(ctx(), "alice", "c1", session.ChatMeta{Settings: &empty})).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.Settings == (session.ChatSettings{})).To(BeTrue(), "settings not cleared: %+v", rec.Settings)
		Expect(s.TruncateEntries(ctx(), "bob", "c1", 1)).To(MatchError(session.ErrNotFound), "another principal's TruncateEntries")
		Expect(s.TruncateEntries(ctx(), "alice", "c1", 1)).To(Succeed())
		es, _ := s.Entries(ctx(), "c1", 0, 0)
		Expect(es).To(HaveLen(1), "entries after truncate = %+v", es)
		Expect(es[0].Text).To(Equal("a"))
		seq, err := s.AppendEntry(ctx(), "c1", session.Entry{Role: session.RoleUser, Text: "d"})
		Expect(err).NotTo(HaveOccurred())
		Expect(seq).To(BeEquivalentTo(2), "next seq after truncate")
	})

	It("MCP switches round-trip, survive a chat update and belong to the owner", func() {
		s := newStore()
		chat(s, "c1", "alice")
		Expect(s.SetMcpOff(ctx(), "alice", "c1", []string{"search", "connector:abc"})).To(Succeed())
		rec, err := s.GetChat(ctx(), "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.McpOff).To(Equal([]string{"search", "connector:abc"}))
		stale := rec
		stale.McpOff = nil
		stale.Title, stale.Updated = "Renamed", t0.Add(time.Minute)
		Expect(s.UpdateChat(ctx(), stale)).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.Title).To(Equal("Renamed"))
		Expect(rec.McpOff).To(HaveLen(2), "want the switches kept")
		Expect(s.SetMcpOff(ctx(), "bob", "c1", []string{"x"})).To(MatchError(session.ErrNotFound), "another principal's SetMcpOff")
		Expect(s.SetMcpOff(ctx(), "alice", "c1", nil)).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.McpOff).To(BeEmpty(), "after clearing")
	})

	It("built-in tool switches round-trip, survive a chat update and belong to the owner", func() {
		s := newStore()
		chat(s, "c1", "alice")
		Expect(s.SetBuiltinsOff(ctx(), "alice", "c1", []string{"web"})).To(Succeed())
		rec, err := s.GetChat(ctx(), "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.BuiltinsOff).To(Equal([]string{"web"}))
		stale := rec
		stale.BuiltinsOff = nil
		stale.Title, stale.Updated = "Renamed", t0.Add(time.Minute)
		Expect(s.UpdateChat(ctx(), stale)).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.BuiltinsOff).To(Equal([]string{"web"}), "want the switches kept")
		Expect(s.SetBuiltinsOff(ctx(), "bob", "c1", []string{"web"})).To(MatchError(session.ErrNotFound), "another principal's SetBuiltinsOff")
		Expect(s.SetBuiltinsOff(ctx(), "alice", "c1", nil)).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.BuiltinsOff).To(BeEmpty(), "after clearing")
	})

	It("opt-in built-in tool switches round-trip, survive a chat update and belong to the owner", func() {
		s := newStore()
		chat(s, "c1", "alice")
		Expect(s.SetBuiltinsOn(ctx(), "alice", "c1", []string{"saved_agents"})).To(Succeed())
		rec, err := s.GetChat(ctx(), "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.BuiltinsOn).To(Equal([]string{"saved_agents"}))
		stale := rec
		stale.BuiltinsOn = nil
		stale.Title, stale.Updated = "Renamed", t0.Add(time.Minute)
		Expect(s.UpdateChat(ctx(), stale)).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.BuiltinsOn).To(Equal([]string{"saved_agents"}), "want the switches kept")
		Expect(s.SetBuiltinsOn(ctx(), "bob", "c1", []string{"saved_agents"})).To(MatchError(session.ErrNotFound), "another principal's SetBuiltinsOn")
		Expect(s.SetBuiltinsOn(ctx(), "alice", "c1", nil)).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.BuiltinsOn).To(BeEmpty(), "after clearing")
	})

	It("keeps the consent card that waits, survives a chat update and belongs to the owner", func() {
		s := newStore()
		chat(s, "c1", "alice")
		Expect(s.SetHostConsent(ctx(), "alice", "c1", &session.HostConsent{ID: "k1", Command: "ls -la"})).To(Succeed())
		rec, err := s.GetChat(ctx(), "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.HostConsent).To(Equal(&session.HostConsent{ID: "k1", Command: "ls -la"}))
		stale := rec
		stale.HostConsent = nil
		stale.Title, stale.Updated = "Renamed", t0.Add(time.Minute)
		Expect(s.UpdateChat(ctx(), stale)).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.HostConsent).NotTo(BeNil(), "want the card kept")
		Expect(s.SetHostConsent(ctx(), "bob", "c1", nil)).To(MatchError(session.ErrNotFound), "another principal's SetHostConsent")
		Expect(s.SetHostConsent(ctx(), "alice", "c1", nil)).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.HostConsent).To(BeNil(), "after clearing")
	})

	It("keeps the network state, survives a chat update, belongs to the owner and loses no concurrent change", func() {
		s := newStore()
		Expect(s.CreateChat(ctx(), session.ChatRecord{ID: "c1", Principal: "alice", Title: "New chat", Created: t0, Updated: t0,
			Egress: egress.ChatState{Preset: egress.PresetRegistries}})).To(Succeed())
		rec, err := s.GetChat(ctx(), "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Egress.Preset).To(Equal(egress.PresetRegistries), "CreateChat keeps the preset")
		Expect(s.UpdateEgress(ctx(), "alice", "c1", func(st *egress.ChatState) error {
			st.PutRule(egress.Rule{Host: "example.com", Port: 443, Allow: true, At: t0})
			st.PutCard(egress.Card{ID: "k1", Host: "example.com", Port: 443, State: egress.CardPending, ToolSeq: 7, AskedAt: t0})
			return nil
		})).To(Succeed())
		stale := rec
		stale.Egress = egress.ChatState{}
		stale.Title, stale.Updated = "Renamed", t0.Add(time.Minute)
		Expect(s.UpdateChat(ctx(), stale)).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.Egress.Rules).To(HaveLen(1), "UpdateChat keeps the rules")
		card, ok := rec.Egress.Card("k1")
		Expect(ok).To(BeTrue(), "the card is kept with the chat, so a reload shows it")
		Expect(card.ToolSeq).To(Equal(int64(7)))
		Expect(s.UpdateEgress(ctx(), "bob", "c1", func(*egress.ChatState) error { return nil })).To(MatchError(session.ErrNotFound))
		boom := errors.New("refused")
		Expect(s.UpdateEgress(ctx(), "alice", "c1", func(st *egress.ChatState) error {
			st.Preset = egress.PresetAll
			return boom
		})).To(MatchError(boom))
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.Egress.Preset).To(Equal(egress.PresetRegistries), "a failed change leaves the state")

		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				defer GinkgoRecover()
				Expect(s.UpdateEgress(ctx(), "alice", "c1", func(st *egress.ChatState) error {
					st.PutRule(egress.Rule{Host: "h.example", Port: 1000 + i, Allow: true})
					return nil
				})).To(Succeed())
			}(i)
		}
		wg.Wait()
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.Egress.Rules).To(HaveLen(9), "no change was lost")
	})

	It("add-on switches round-trip, survive a chat update and belong to the owner", func() {
		s := newStore()
		chat(s, "c1", "alice")
		Expect(s.SetAddonsOff(ctx(), "alice", "c1", []string{"office", "sandbox"})).To(Succeed())
		rec, err := s.GetChat(ctx(), "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.AddonsOff).To(Equal([]string{"office", "sandbox"}))
		stale := rec
		stale.AddonsOff = nil
		stale.Title, stale.Updated = "Renamed", t0.Add(time.Minute)
		Expect(s.UpdateChat(ctx(), stale)).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.Title).To(Equal("Renamed"))
		Expect(rec.AddonsOff).To(HaveLen(2), "want the switches kept")
		Expect(s.SetAddonsOff(ctx(), "bob", "c1", []string{"x"})).To(MatchError(session.ErrNotFound), "another principal's SetAddonsOff")
		Expect(s.SetAddonsOff(ctx(), "alice", "c1", nil)).To(Succeed())
		rec, _ = s.GetChat(ctx(), "alice", "c1")
		Expect(rec.AddonsOff).To(BeEmpty(), "after clearing")
	})

	It("a chat created with its switches keeps them", func() {
		s := newStore()
		rec := session.ChatRecord{ID: "c1", Principal: "alice", Title: "New", Created: t0, Updated: t0, Kind: session.KindChat, Model: "qwen3",
			AddonsOff: []string{"office"}, McpOff: []string{"connector:k1"}, BuiltinsOff: []string{"web"},
			BuiltinsOn: []string{"saved_agents"}, SkillsOff: []string{"builtin:pdf"}}
		Expect(s.CreateChat(ctx(), rec)).To(Succeed())
		got, err := s.GetChat(ctx(), "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.AddonsOff).To(Equal([]string{"office"}))
		Expect(got.McpOff).To(Equal([]string{"connector:k1"}))
		Expect(got.BuiltinsOff).To(Equal([]string{"web"}))
		Expect(got.BuiltinsOn).To(Equal([]string{"saved_agents"}))
		Expect(got.SkillsOff).To(Equal([]string{"builtin:pdf"}))
	})

	It("kind, parent, model, status and summary round-trip", func() {
		s := newStore()
		chat(s, "parent", "alice")
		rec := session.ChatRecord{ID: "child", Principal: "alice", Title: "Sub", Created: t0, Updated: t0, Kind: session.KindSubAgent, ParentID: "parent", Model: "qwen3"}
		Expect(s.CreateChat(ctx(), rec)).To(Succeed())
		rec.Status, rec.Updated = "done", t0.Add(time.Minute)
		Expect(s.UpdateChat(ctx(), rec)).To(Succeed())
		got, err := s.GetChat(ctx(), "alice", "child")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Kind).To(Equal(session.KindSubAgent), "got %+v", got)
		Expect(got.ParentID).To(Equal("parent"))
		Expect(got.Model).To(Equal("qwen3"))
		Expect(got.Status).To(Equal("done"))
	})

	It("agent and origin round-trip and survive UpdateChat", func() {
		s := newStore()
		rec := session.ChatRecord{ID: "run1", Principal: "alice", Title: "Run", Created: t0, Updated: t0, Kind: session.KindChat, Model: "qwen3",
			AgentID: "ag1", Origin: session.OriginTrigger, OriginID: "trg1"}
		Expect(s.CreateChat(ctx(), rec)).To(Succeed())
		stale := rec
		stale.AgentID, stale.Origin, stale.OriginID, stale.Title, stale.Updated = "other", "", "", "Renamed", t0.Add(time.Minute)
		Expect(s.UpdateChat(ctx(), stale)).To(Succeed())
		got, err := s.GetChat(ctx(), "alice", "run1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.AgentID).To(Equal("ag1"), "got %+v", got)
		Expect(got.Origin).To(Equal(session.OriginTrigger))
		Expect(got.OriginID).To(Equal("trg1"))
		Expect(got.Title).To(Equal("Renamed"))
	})

	It("the assistant flag round-trips and survives UpdateChat", func() {
		s := newStore()
		rec := session.ChatRecord{ID: "as1", Principal: "alice", Title: "Assistant", Created: t0, Updated: t0, Kind: session.KindChat, Model: "qwen3", Assistant: true}
		Expect(s.CreateChat(ctx(), rec)).To(Succeed())
		stale := rec
		stale.Assistant, stale.Title, stale.Updated = false, "Renamed", t0.Add(time.Minute)
		Expect(s.UpdateChat(ctx(), stale)).To(Succeed())
		got, err := s.GetChat(ctx(), "alice", "as1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Assistant).To(BeTrue(), "got %+v", got)
		Expect(got.Title).To(Equal("Renamed"))
	})

	It("pending messages are taken once, in send order", func() {
		s := newStore()
		chat(s, "c1", "alice")
		for _, x := range []string{"one", "two", "three"} {
			Expect(s.AddPending(ctx(), "alice", "c1", x)).Error().NotTo(HaveOccurred())
		}
		got, err := s.TakePending(ctx(), "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(texts(got)).To(Equal([]string{"one", "two", "three"}), "first take")
		again, err := s.TakePending(ctx(), "alice", "c1")
		Expect(err).NotTo(HaveOccurred(), "second take")
		Expect(again).To(BeEmpty(), "second take = %v; want nothing", texts(again))
		left, err := s.Pending(ctx(), "alice", "c1")
		Expect(err).NotTo(HaveOccurred(), "Pending after take")
		Expect(left).To(BeEmpty(), "Pending after take = %v; want nothing", texts(left))
	})

	It("edit and cancel work until the message is taken", func() {
		s := newStore()
		chat(s, "c1", "alice")
		a, _ := s.AddPending(ctx(), "alice", "c1", "keep")
		b, _ := s.AddPending(ctx(), "alice", "c1", "drop")
		c, _ := s.AddPending(ctx(), "alice", "c1", "typo")
		Expect(s.EditPending(ctx(), "alice", "c1", c.ID, "fixed")).To(Succeed())
		Expect(s.CancelPending(ctx(), "alice", "c1", b.ID)).To(Succeed())
		got, _ := s.TakePending(ctx(), "alice", "c1")
		Expect(texts(got)).To(Equal([]string{"keep", "fixed"}), "taken")
		Expect(s.EditPending(ctx(), "alice", "c1", a.ID, "late")).To(MatchError(store.ErrDelivered), "edit after take")
		Expect(s.CancelPending(ctx(), "alice", "c1", a.ID)).To(MatchError(store.ErrDelivered), "cancel after take")
	})

	It("restore puts taken messages back in their original order", func() {
		s := newStore()
		chat(s, "c1", "alice")
		_, _ = s.AddPending(ctx(), "alice", "c1", "one")
		_, _ = s.AddPending(ctx(), "alice", "c1", "two")
		taken, _ := s.TakePending(ctx(), "alice", "c1")
		_, _ = s.AddPending(ctx(), "alice", "c1", "three")
		var ids []string
		for _, p := range taken {
			ids = append(ids, p.ID)
		}
		Expect(s.RestorePending(ctx(), "alice", "c1", ids)).To(Succeed())
		got, _ := s.TakePending(ctx(), "alice", "c1")
		Expect(texts(got)).To(Equal([]string{"one", "two", "three"}), "after restore")
	})

	It("another principal sees ErrNotFound for every call", func() {
		s := newStore()
		chat(s, "c1", "alice")
		p, _ := s.AddPending(ctx(), "alice", "c1", "secret")
		calls := map[string]error{}
		_, calls["AddPending"] = s.AddPending(ctx(), "bob", "c1", "x")
		_, calls["Pending"] = s.Pending(ctx(), "bob", "c1")
		calls["EditPending"] = s.EditPending(ctx(), "bob", "c1", p.ID, "x")
		calls["CancelPending"] = s.CancelPending(ctx(), "bob", "c1", p.ID)
		_, calls["TakePending"] = s.TakePending(ctx(), "bob", "c1")
		calls["RestorePending"] = s.RestorePending(ctx(), "bob", "c1", []string{p.ID})
		calls["ClaimTurn"] = s.ClaimTurn(ctx(), "bob", "c1", "r1")
		calls["ReleaseTurn"] = s.ReleaseTurn(ctx(), "bob", "c1", "r1", store.TurnDone)
		_, calls["Turn"] = s.Turn(ctx(), "bob", "c1")
		for name, err := range calls {
			Expect(err).To(MatchError(session.ErrNotFound), "%s as another principal", name)
		}
	})

	It("exactly one claim wins", func() {
		s := newStore()
		chat(s, "c1", "alice")
		c := ctx()
		var wins, busy int32
		var other []error
		var mu sync.Mutex
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				defer GinkgoRecover()
				err := s.ClaimTurn(c, "alice", "c1", string(rune('a'+i))) // #nosec G115 -- i counts a few test turns
				switch {
				case err == nil:
					atomic.AddInt32(&wins, 1)
				case isTurnRunning(err):
					atomic.AddInt32(&busy, 1)
				default:
					mu.Lock()
					other = append(other, err)
					mu.Unlock()
				}
			}(i)
		}
		wg.Wait()
		Expect(other).To(BeEmpty(), "ClaimTurn failed unexpectedly")
		Expect(wins).To(BeEquivalentTo(1))
		Expect(busy).To(BeEquivalentTo(7))
	})

	It("release frees the chat and records the outcome", func() {
		s := newStore()
		chat(s, "c1", "alice")
		Expect(s.ClaimTurn(ctx(), "alice", "c1", "r1")).To(Succeed())
		info, _ := s.Turn(ctx(), "alice", "c1")
		Expect(info.Running).To(BeTrue(), "running turn = %+v", info)
		Expect(info.RunID).To(Equal("r1"))
		Expect(s.ReleaseTurn(ctx(), "alice", "c1", "other", store.TurnDone)).To(MatchError(store.ErrNotClaimed), "release by another run")
		Expect(s.ReleaseTurn(ctx(), "alice", "c1", "r1", store.TurnStopped)).To(Succeed())
		info, _ = s.Turn(ctx(), "alice", "c1")
		Expect(info.Running).To(BeFalse(), "after release = %+v", info)
		Expect(info.Last).To(Equal(store.TurnStopped))
		Expect(s.ClaimTurn(ctx(), "alice", "c1", "r2")).To(Succeed(), "claim after release")
	})

	It("a turn that stops beating is reaped, one that keeps beating is not", func() {
		s := newStore()
		chat(s, "stale", "alice")
		chat(s, "alive", "alice")
		Expect(s.ClaimTurn(ctx(), "alice", "stale", "r-stale")).To(Succeed())
		Expect(s.ClaimTurn(ctx(), "alice", "alive", "r-alive")).To(Succeed())
		time.Sleep(40 * time.Millisecond)
		cutoff := time.Now()
		time.Sleep(40 * time.Millisecond)
		Expect(s.TouchTurn(ctx(), "alice", "alive", "r-alive")).To(Succeed())

		got, err := s.ReapTurns(ctx(), cutoff)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(1), "reaped = %+v, want only the chat that stopped beating", got)
		Expect(got[0].ChatID).To(Equal("stale"))
		Expect(got[0].Principal).To(Equal("alice"))
		Expect(got[0].RunID).To(Equal("r-stale"))
		info, _ := s.Turn(ctx(), "alice", "stale")
		Expect(info.Running).To(BeFalse(), "reaped turn = %+v, want not running and interrupted", info)
		Expect(info.Last).To(Equal(store.TurnInterrupted))
		info, _ = s.Turn(ctx(), "alice", "alive")
		Expect(info.Running).To(BeTrue(), "a turn that kept beating was reaped: %+v", info)
		// The run that was reaped can no longer beat or release, and the chat is free.
		Expect(s.TouchTurn(ctx(), "alice", "stale", "r-stale")).To(MatchError(store.ErrNotClaimed), "TouchTurn on a reaped run")
		Expect(s.ClaimTurn(ctx(), "alice", "stale", "r-next")).To(Succeed(), "claim after reap")
		again, _ := s.ReapTurns(ctx(), cutoff)
		Expect(again).To(BeEmpty(), "a second reap found %+v", again)
	})

	It("a reaped turn gives back the messages its model request took but never confirmed", func() {
		s := newStore()
		chat(s, "c1", "alice")
		Expect(s.ClaimTurn(ctx(), "alice", "c1", "r1")).To(Succeed())
		lost, _ := s.AddPending(ctx(), "alice", "c1", "taken, then the frontend died")
		kept, _ := s.AddPending(ctx(), "alice", "c1", "taken and recorded")
		Expect(s.TakePending(ctx(), "alice", "c1")).Error().NotTo(HaveOccurred())
		Expect(s.ConfirmPending(ctx(), "alice", "c1", []string{kept.ID})).To(Succeed())
		got, _ := s.ReapTurns(ctx(), time.Now().Add(time.Hour))
		Expect(got).To(HaveLen(1), "reaped = %+v, want the one turn", got)
		pend, _ := s.Pending(ctx(), "alice", "c1")
		Expect(pend).To(HaveLen(1), "pending after the reap = %+v, want only the unconfirmed message, undelivered", pend)
		Expect(pend[0].ID).To(Equal(lost.ID))
		Expect(pend[0].Delivered).To(BeFalse())
	})

	It("touch is refused for another principal and for a run that does not hold the chat", func() {
		s := newStore()
		chat(s, "c1", "alice")
		_ = s.ClaimTurn(ctx(), "alice", "c1", "r1")
		Expect(s.TouchTurn(ctx(), "bob", "c1", "r1")).To(MatchError(session.ErrNotFound), "TouchTurn as bob")
		Expect(s.TouchTurn(ctx(), "alice", "c1", "other")).To(MatchError(store.ErrNotClaimed), "TouchTurn by another run")
	})

	It("racing takes never deliver a message twice", func() {
		s := newStore()
		chat(s, "c1", "alice")
		for i := 0; i < 20; i++ {
			_, _ = s.AddPending(ctx(), "alice", "c1", string(rune('a'+i)))
		}
		c := ctx()
		var mu sync.Mutex
		seen := map[string]int{}
		var errs []error
		var wg sync.WaitGroup
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				got, err := s.TakePending(c, "alice", "c1")
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errs = append(errs, err)
					return
				}
				for _, p := range got {
					seen[p.ID]++
				}
			}()
		}
		wg.Wait()
		Expect(errs).To(BeEmpty(), "TakePending failed")
		Expect(seen).To(HaveLen(20), "delivered %d distinct messages, want 20", len(seen))
		for id, n := range seen {
			Expect(n).To(Equal(1), "message %s delivered %d times", id, n)
		}
	})

	It("deleting a chat removes its pending messages and claim", func() {
		s := newStore()
		chat(s, "c1", "alice")
		_, _ = s.AddPending(ctx(), "alice", "c1", "x")
		_ = s.ClaimTurn(ctx(), "alice", "c1", "r1")
		Expect(s.DeleteChat(ctx(), "alice", "c1")).To(Succeed())
		_, err := s.Pending(ctx(), "alice", "c1")
		Expect(err).To(MatchError(session.ErrNotFound), "Pending after delete")
		chat(s, "c1", "alice") // the id can be reused, with a clean slate
		got, _ := s.Pending(ctx(), "alice", "c1")
		Expect(got).To(BeEmpty(), "a recreated chat inherited %v", texts(got))
		Expect(s.ClaimTurn(ctx(), "alice", "c1", "r2")).To(Succeed(), "a recreated chat inherited a claim")
	})
}

// Seed writes a small, known state. CheckSeed reads it back from another
// handle on the same storage (a reopened store), which proves persistence.
func Seed(s store.Store) {
	Expect(s.CreateChat(ctx(), session.ChatRecord{ID: "seed", Principal: "alice", Title: "Kept", Created: t0, Updated: t0})).To(Succeed())
	Expect(s.AppendEntry(ctx(), "seed", session.Entry{Role: "user", Text: "hello", Note: "\n\n[Text of a.pdf:\nx]",
		Attachments: []session.Attachment{{Path: "/Chats/c/a.pdf", Name: "a.pdf", Size: 9, Mime: "application/pdf", Version: 1}}})).Error().NotTo(HaveOccurred())
	Expect(s.AddPending(ctx(), "alice", "seed", "waiting")).Error().NotTo(HaveOccurred())
	Expect(s.ClaimTurn(ctx(), "alice", "seed", "r1")).To(Succeed())
	Expect(s.ReleaseTurn(ctx(), "alice", "seed", "r1", store.TurnStopped)).To(Succeed())
}

// CheckSeed fails the spec unless s holds the state Seed wrote.
func CheckSeed(s store.Store) {
	rec, err := s.GetChat(ctx(), "alice", "seed")
	Expect(err).NotTo(HaveOccurred())
	Expect(rec.Title).To(Equal("Kept"))
	es, err := s.Entries(ctx(), "seed", 0, 10)
	Expect(err).NotTo(HaveOccurred())
	Expect(es).To(HaveLen(1), "Entries = %+v", es)
	Expect(es[0].Text).To(Equal("hello"))
	Expect(es[0].Note).To(Equal("\n\n[Text of a.pdf:\nx]"))
	Expect(es[0].Attachments).To(Equal([]session.Attachment{{Path: "/Chats/c/a.pdf", Name: "a.pdf", Size: 9, Mime: "application/pdf", Version: 1}}))
	ps, err := s.Pending(ctx(), "alice", "seed")
	Expect(err).NotTo(HaveOccurred())
	Expect(texts(ps)).To(Equal([]string{"waiting"}), "Pending")
	info, err := s.Turn(ctx(), "alice", "seed")
	Expect(err).NotTo(HaveOccurred())
	Expect(info.Running).To(BeFalse(), "Turn = %+v", info)
	Expect(info.Last).To(Equal(store.TurnStopped))
}

func isTurnRunning(err error) bool { return errors.Is(err, store.ErrTurnRunning) }
