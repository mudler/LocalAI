package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
)

// fakeKnowledge is a Collections value that can also add and remove a text,
// as the real service does. It keeps the entries by user, collection and name.
type fakeKnowledge struct {
	*fakeCollections
	mu      sync.Mutex
	noEmbed bool
	addErr  error
	entries map[string]string
}

func newFakeKnowledge(user string, names ...string) *fakeKnowledge {
	f := &fakeKnowledge{fakeCollections: &fakeCollections{mine: map[string]map[string][]studio.CollectionHit{user: {}}}, entries: map[string]string{}}
	for _, n := range names {
		f.mine[user][n] = nil
	}
	return f
}

func (f *fakeKnowledge) EmbeddingReady(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.noEmbed {
		return errors.New("no embedding model")
	}
	return nil
}

func (f *fakeKnowledge) Add(_ context.Context, user, collection, name, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.addErr != nil {
		return f.addErr
	}
	f.entries[user+"|"+collection+"|"+name] = text
	return nil
}

func (f *fakeKnowledge) Remove(_ context.Context, user, collection, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.entries, user+"|"+collection+"|"+name)
	return nil
}

func (f *fakeKnowledge) held() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.entries {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func knowledgeArgs(collection, content, summary string) string {
	return proposeArgs("knowledge", "user", collection, content, summary)
}

// knowledgeShared is a rig whose service can add to the collections of fk.
func knowledgeShared(fk *fakeKnowledge, st *saveSetting) *shared {
	sh := changeShared(grantAll)
	inner := sh.deps
	sh.deps = func(d *studio.Deps) {
		inner(d)
		d.Collections = fk
		if st != nil {
			d.UserSetting = st.get
		}
	}
	return sh
}

var _ = Describe("knowledge changes", func() {
	ctx := context.Background()
	var (
		fk *fakeKnowledge
		r  *trigRig
		ag agents.Agent
	)
	BeforeEach(func() {
		fk = newFakeKnowledge(alice, "manuals", "wiki")
		r = knowledgeShared(fk, nil).rig(func(workspace.Actor, string) []studiotest.ToolStep { return nil }, studio.TriggerOptions{})
		var err error
		ag, err = r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Librarian", Model: "m", Collections: []string{"manuals"},
			Builtins: []string{contract.CapKnowledgeWrite}})
		Expect(err).NotTo(HaveOccurred())
	})
	propose := func(in studio.ProposeInput) (contract.ChangeProposal, error) {
		if in.User == "" {
			in.User = alice
		}
		if in.Agent == "" && in.Kind == "" {
			in.Agent = ag.ID
		}
		if in.Kind == "" {
			in.Kind = "knowledge"
		}
		if in.Scope == "" {
			in.Scope = "user"
		}
		if in.Summary == "" {
			in.Summary = "Router reset steps"
		}
		return r.svc.Changes().Propose(ctx, in)
	}

	It("is stored as a proposal that adds nothing, then added as one entry by a confirmation and removed by an undo", func() {
		p, err := propose(studio.ProposeInput{Target: "manuals", Content: "Hold the reset button for ten seconds."})
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Kind).To(Equal(contract.ChangeKnowledge))
		Expect(p.Target).To(Equal("manuals"))
		Expect(p.Status).To(Equal(contract.ProposalPending))
		Expect(fk.held()).To(BeEmpty(), "a proposal indexes nothing")

		done, err := r.svc.Changes().Confirm(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(done.Status).To(Equal(contract.ProposalApplied))
		Expect(done.Auto).To(BeFalse())
		Expect(done.Rev).To(BeZero(), "a collection has no change set")
		Expect(fk.held()).To(HaveLen(1))
		key := fk.held()[0]
		Expect(key).To(HavePrefix(alice + "|manuals|router-reset-steps-"))
		Expect(key).To(HaveSuffix(".md"))
		Expect(fk.entries[key]).To(Equal("# Router reset steps\n\nHold the reset button for ten seconds.\n"))
		_, err = r.svc.Changes().Confirm(ctx, alice, p.ID)
		Expect(err).To(MatchError(studio.ErrChangeAnswered), "a second confirmation adds nothing")
		Expect(fk.held()).To(HaveLen(1))

		back, err := r.svc.Changes().Revert(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(back.Status).To(Equal(contract.ProposalReverted))
		Expect(fk.held()).To(BeEmpty())
		_, err = r.svc.Changes().Revert(ctx, alice, p.ID)
		Expect(err).To(MatchError(studio.ErrNotApplied))

		events, err := r.svc.Changes().Events(ctx, alice, 10)
		Expect(err).NotTo(HaveOccurred())
		var actions []string
		for _, e := range events {
			actions = append(actions, e.Action)
		}
		Expect(actions).To(ContainElements(proposals.ActionPropose, proposals.ActionConfirm, proposals.ActionRevert))
	})

	It("adds the text the owner edited, and holds the same cap", func() {
		p, err := propose(studio.ProposeInput{Target: "manuals", Content: "draft"})
		Expect(err).NotTo(HaveOccurred())
		_, err = r.svc.Changes().ConfirmEdited(ctx, alice, p.ID, strings.Repeat("x", contract.MaxKnowledgeBytes+1))
		Expect(err).To(MatchError(studio.ErrBadChange))
		Expect(fk.held()).To(BeEmpty())
		_, err = r.svc.Changes().ConfirmEdited(ctx, alice, p.ID, "final text")
		Expect(err).NotTo(HaveOccurred())
		Expect(fk.entries[fk.held()[0]]).To(ContainSubstring("final text"))
	})

	It("undoes the last change of the agent by RevertLast and ignores the knowledge ones when asked", func() {
		p, err := propose(studio.ProposeInput{Target: "manuals", Content: "a"})
		Expect(err).NotTo(HaveOccurred())
		_, err = r.svc.Changes().Confirm(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.svc.Changes().RevertLast(ctx, alice, ag.ID, contract.ChangeKnowledge)
		Expect(err).To(MatchError(studio.ErrNothingToRevert))
		got, err := r.svc.Changes().RevertLast(ctx, alice, ag.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID).To(Equal(p.ID))
		Expect(fk.held()).To(BeEmpty())
	})

	It("stays pending when the collection fails, and can be tried again", func() {
		p, err := propose(studio.ProposeInput{Target: "manuals", Content: "a"})
		Expect(err).NotTo(HaveOccurred())
		fk.addErr = errors.New("disk full")
		_, err = r.svc.Changes().Confirm(ctx, alice, p.ID)
		Expect(err).To(MatchError(ContainSubstring("disk full")))
		got, _ := r.svc.Changes().Get(ctx, alice, p.ID)
		Expect(got.Status).To(Equal(contract.ProposalPending))
		fk.addErr = nil
		_, err = r.svc.Changes().Confirm(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(fk.held()).To(HaveLen(1))
	})

	It("asks the destination again at the confirmation: a model lost since the proposal, or a collection that was removed", func() {
		p, err := propose(studio.ProposeInput{Target: "manuals", Content: "a"})
		Expect(err).NotTo(HaveOccurred())
		fk.noEmbed = true
		_, err = r.svc.Changes().Confirm(ctx, alice, p.ID)
		Expect(err).To(MatchError(ContainSubstring("embedding model")))
		fk.noEmbed = false
		delete(fk.mine[alice], "manuals")
		_, err = r.svc.Changes().Confirm(ctx, alice, p.ID)
		Expect(err).To(MatchError(ContainSubstring(`no collection "manuals"`)))
		got, _ := r.svc.Changes().Get(ctx, alice, p.ID)
		Expect(got.Status).To(Equal(contract.ProposalPending))
		Expect(fk.held()).To(BeEmpty())
	})

	DescribeTable("the access chain of a proposal",
		func(in studio.ProposeInput, mutate func(), want error, text string) {
			if mutate != nil {
				mutate()
			}
			_, err := propose(in)
			if want == nil {
				Expect(err).NotTo(HaveOccurred())
				return
			}
			Expect(err).To(MatchError(want))
			if text != "" {
				Expect(err).To(MatchError(ContainSubstring(text)))
			}
			list, _ := r.svc.Changes().List(ctx, alice)
			Expect(list).To(BeEmpty(), "a refused proposal is not stored")
		},
		Entry("a collection of the agent list", studio.ProposeInput{Target: "manuals", Content: "t"}, nil, nil, ""),
		Entry("a collection that exists but is not on the agent list", studio.ProposeInput{Target: "wiki", Content: "t"}, nil, studio.ErrBadChange, "not on this agent's list"),
		Entry("a collection that does not exist", studio.ProposeInput{Target: "nope", Content: "t"}, nil, studio.ErrBadChange, "no collection"),
		Entry("no target", studio.ProposeInput{Target: "", Content: "t"}, nil, studio.ErrBadChange, "name of a collection"),
		Entry("a control character in the name", studio.ProposeInput{Target: "man\nuals", Content: "t"}, nil, studio.ErrBadChange, "name of a collection"),
		Entry("a name that is too long", studio.ProposeInput{Target: strings.Repeat("a", 129), Content: "t"}, nil, studio.ErrBadChange, "name of a collection"),
		Entry("an empty text", studio.ProposeInput{Target: "manuals", Content: "  \n"}, nil, studio.ErrBadChange, "empty"),
		Entry("scope agent", studio.ProposeInput{Target: "manuals", Content: "t", Scope: "agent"}, nil, studio.ErrBadChange, "scope user"),
		Entry("scope channel", studio.ProposeInput{Target: "manuals", Content: "t", Scope: "channel"}, nil, studio.ErrBadChange, "scope user"),
		Entry("exactly the cap", studio.ProposeInput{Target: "manuals", Content: strings.Repeat("a", contract.MaxKnowledgeBytes)}, nil, nil, ""),
		Entry("one byte over the cap", studio.ProposeInput{Target: "manuals", Content: strings.Repeat("a", contract.MaxKnowledgeBytes+1)}, nil, studio.ErrBadChange, "may have 65536"),
		Entry("binary text", studio.ProposeInput{Target: "manuals", Content: "a\x00b"}, nil, studio.ErrBadChange, "must be text"),
		Entry("the embedding model is not installed: the grant is not usable", studio.ProposeInput{Target: "manuals", Content: "t"}, func() { fk.noEmbed = true }, proposals.ErrNotGranted, ""),
		Entry("the account may not use collections", studio.ProposeInput{Target: "manuals", Content: "t"}, func() { fk.blocked = map[string]bool{alice: true} }, proposals.ErrNotGranted, ""),
		Entry("an agent without the grant", studio.ProposeInput{Target: "manuals", Content: "t"}, func() {
			var err error
			ag, err = r.svc.SaveAgent(ctx, alice, agents.Agent{ID: ag.ID, Name: "Librarian", Model: "m", Collections: []string{"manuals"}, Builtins: []string{contract.CapMemoryWrite}})
			Expect(err).NotTo(HaveOccurred())
		}, proposals.ErrNotGranted, ""),
		Entry("an agent with an empty collection list", studio.ProposeInput{Target: "manuals", Content: "t"}, func() {
			var err error
			ag, err = r.svc.SaveAgent(ctx, alice, agents.Agent{ID: ag.ID, Name: "Librarian", Model: "m", Builtins: []string{contract.CapKnowledgeWrite}})
			Expect(err).NotTo(HaveOccurred())
		}, studio.ErrBadChange, "no collection is on this agent's list"),
		Entry("a run that started by itself", studio.ProposeInput{Target: "manuals", Content: "t", Source: contract.ChangeSource{Kind: "trigger"}}, nil, proposals.ErrNotGranted, ""),
	)

	It("lets a plain chat add to any collection of the person", func() {
		_, err := r.svc.Changes().Propose(ctx, studio.ProposeInput{User: alice, Kind: "knowledge", Scope: "user", Target: "wiki", Content: "t", Summary: "s"})
		Expect(err).NotTo(HaveOccurred())
	})

	It("never lets the host apply it unasked, whatever the caller asks", func() {
		_, err := propose(studio.ProposeInput{Target: "manuals", Content: "t", Auto: true})
		Expect(err).To(MatchError(studio.ErrBadChange))
		Expect(fk.held()).To(BeEmpty())
	})

	Describe("the threat scan", func() {
		const leaky = "Wi-Fi of the office\npassword: correct-horse-battery\n"

		It("flags a text that looks like it holds a secret, keeps showing it, and lets the owner confirm it", func() {
			p, err := propose(studio.ProposeInput{Target: "manuals", Content: leaky})
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Warning).To(Equal(contract.WarningSecret))
			Expect(p.Content).To(Equal(leaky), "the owner sees the text as it is")
			got, err := r.svc.Changes().Get(ctx, alice, p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Warning).To(Equal(contract.WarningSecret))
			list, err := r.svc.Changes().List(ctx, alice)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			Expect(list[0].Warning).To(Equal(contract.WarningSecret))

			done, err := r.svc.Changes().Confirm(ctx, alice, p.ID)
			Expect(err).NotTo(HaveOccurred(), "the web interface confirms it")
			Expect(done.Warning).To(Equal(contract.WarningSecret))
			Expect(fk.held()).To(HaveLen(1))
		})

		It("scans the summary too, and flags a key block", func() {
			p, err := propose(studio.ProposeInput{Target: "manuals", Content: "plain", Summary: "token = abcdef123456"})
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Warning).To(Equal(contract.WarningSecret))
			p, err = propose(studio.ProposeInput{Target: "manuals", Content: "-----BEGIN RSA PRIVATE KEY-----\nabc\n"})
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Warning).To(Equal(contract.WarningSecret))
		})

		It("does not flag an ordinary text", func() {
			p, err := propose(studio.ProposeInput{Target: "manuals", Content: "Hold the reset button for ten seconds. The password is set again by the reset."})
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Warning).To(BeEmpty())
		})
	})

	It("keeps the limits of the other kinds: 10 waiting, 5 an hour", func() {
		for i := range contract.MaxChangesPerHour {
			_, err := propose(studio.ProposeInput{Target: "manuals", Content: strings.Repeat("a", i+1)})
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := propose(studio.ProposeInput{Target: "manuals", Content: "more"})
		Expect(err).To(MatchError(proposals.ErrRateLimited))
	})

	It("cannot be proposed on a server whose collections cannot add", func() {
		plain := &fakeCollections{mine: map[string]map[string][]studio.CollectionHit{alice: {"manuals": nil}}}
		sh := changeShared(grantAll)
		inner := sh.deps
		sh.deps = func(d *studio.Deps) { inner(d); d.Collections = plain }
		rr := sh.rig(func(workspace.Actor, string) []studiotest.ToolStep { return nil }, studio.TriggerOptions{})
		_, err := rr.svc.Changes().Propose(ctx, studio.ProposeInput{User: alice, Kind: "knowledge", Scope: "user", Target: "manuals", Content: "t", Summary: "s"})
		Expect(err).To(MatchError(studio.ErrBadChange))
		Expect(rr.svc.BuiltinGrants()).NotTo(BeEmpty())
	})
})

var _ = Describe("knowledge changes from a chat of the person", func() {
	ctx := context.Background()
	// run sends "add" to a plain chat under a save level and returns what the
	// proposal tool said and what was stored.
	run := func(level string) (*trigRig, *fakeKnowledge) {
		fk := newFakeKnowledge(alice, "manuals")
		st := &saveSetting{}
		if level != "" {
			st.putHost(studio.MemorySaveKey, level)
		}
		r := knowledgeShared(fk, st).rig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "add" {
				return []studiotest.ToolStep{{Tool: "propose_change", Args: knowledgeArgs("manuals", "Hold reset for ten seconds.", "Reset steps")}, {Say: "ok"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, studio.TriggerOptions{})
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "add")
		idle(r.svc, alice, id)
		return r, fk
	}

	DescribeTable("is a proposal the owner confirms at every save level",
		func(level string) {
			r, fk := run(level)
			p := onlyProposal(r)
			Expect(p.Kind).To(Equal(contract.ChangeKnowledge))
			Expect(p.Status).To(Equal(contract.ProposalPending))
			Expect(p.Auto).To(BeFalse())
			Expect(fk.held()).To(BeEmpty())
			res := r.h.ResultsFor("propose_change")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).To(BeNil())
			Expect(res[0].Out).To(ContainSubstring("Nothing is saved yet"))
			_, err := r.svc.Changes().Confirm(ctx, alice, p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(fk.held()).To(HaveLen(1))
		},
		Entry("ask", studio.SaveAsk),
		Entry("auto: a memory note would be saved at once, a text for Knowledge never", studio.SaveAuto),
		Entry("always confirm", studio.SaveAlways),
		Entry("nothing chosen", ""),
	)

	It("is not offered when the person chose Never: the kind leaves the schema and a call is refused", func() {
		r, fk := run(studio.SaveNever)
		list, _ := r.svc.Changes().List(ctx, alice)
		Expect(list).To(BeEmpty())
		Expect(fk.held()).To(BeEmpty())
		res := r.h.ResultsFor("propose_change")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(MatchError(ContainSubstring("turned off saving")))
		events, _ := r.svc.Changes().Events(ctx, alice, 5)
		Expect(events).NotTo(BeEmpty())
		Expect(events[0].Action).To(Equal(proposals.ActionRefuse))
	})

	It("is not offered when the Knowledge tools are switched off for the chat", func() {
		fk := newFakeKnowledge(alice, "manuals")
		r := knowledgeShared(fk, nil).rig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "propose_change", Args: knowledgeArgs("manuals", "t", "s")}, {Say: "ok"}}
		}, studio.TriggerOptions{})
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.svc.SetBuiltinsOff(ctx, alice, rec.ID, []string{"collections"})).To(Succeed())
		sent(r.svc, alice, rec.ID, "add")
		idle(r.svc, alice, rec.ID)
		res := r.h.ResultsFor("propose_change")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(BeNil())
		list, _ := r.svc.Changes().List(ctx, alice)
		Expect(list).To(BeEmpty())
	})

	It("lists knowledge among the kinds the model can propose", func() {
		fk := newFakeKnowledge(alice, "manuals")
		r := knowledgeShared(fk, nil).rig(func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }, studio.TriggerOptions{})
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "hi")
		idle(r.svc, alice, id)
		var doc struct {
			Properties struct {
				Kind struct {
					Enum []string `json:"enum"`
				} `json:"kind"`
			} `json:"properties"`
		}
		Expect(json.Unmarshal([]byte(r.h.ToolSchema(id, "propose_change")), &doc)).To(Succeed())
		Expect(doc.Properties.Kind.Enum).To(ContainElement("knowledge"))
	})
})

var _ = Describe("knowledge changes from a messaging app", func() {
	ctx := context.Background()
	const (
		listed = "11"
		paired = "12"
	)
	var (
		fk  *fakeKnowledge
		bot *scriptedBot
		r   *connRig
		b   connectors.Binding
	)
	caps := []string{contract.CapMemoryWrite, contract.CapKnowledgeWrite, contract.CapProposalsConfirm}
	setup := func(embed bool, collections []string) {
		fk, bot = newFakeKnowledge(alice, "manuals"), newScriptedBot()
		fk.noEmbed = !embed
		addon := studiobot.Telegram(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
		addon.Kind.Permissions = caps
		script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch text {
			case "add":
				return []studiotest.ToolStep{{Tool: "propose_change", Args: knowledgeArgs("manuals", "Hold reset for ten seconds.", "Reset steps")}, {Say: "ok"}}
			case "leak":
				return []studiotest.ToolStep{{Tool: "propose_change", Args: knowledgeArgs("manuals", "Wi-Fi\npassword: correct-horse-battery", "Wi-Fi of the office")}, {Say: "ok"}}
			case "forget":
				return []studiotest.ToolStep{{Tool: "revert_change", Args: `{}`}, {Say: "ok"}}
			}
			return []studiotest.ToolStep{{Say: "model"}}
		}
		sh := knowledgeShared(fk, nil)
		r = newConnRigWith(sh, connStore(), studiobot.NewTransport(addon), script, nil)
		r.bot, r.untrusted = bot, true
		r.svc.SetCapabilityGate(r.conns.CapabilityGate())
		ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Support", Model: "m", Addons: []string{"office"}, Collections: collections,
			Builtins: []string{contract.CapMemoryWrite, contract.CapKnowledgeWrite}})
		Expect(err).NotTo(HaveOccurred())
		r.agent = ag
		b = r.bind(studio.ConnectorInput{Allow: []string{listed, paired}, Caps: caps})
		got, err := r.store.GetBinding(ctx, b.ID)
		Expect(err).NotTo(HaveOccurred())
		got.Paired = append(got.Paired, connectors.PairedSender{SenderID: paired, At: sh.clock.Now()})
		Expect(r.store.PutBinding(ctx, got)).To(Succeed())
		b = got
	}
	say := func(sender, text string, n int) {
		before := len(bot.answers())
		m := contract.ConnectorMessage{Chat: sender, Direct: true, SenderID: sender, SenderName: "S", Text: text}
		Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: m})).To(Succeed())
		waitFor("the answers", func() bool { return len(bot.answers()) >= before+n })
	}
	list := func() []contract.ChangeProposal {
		l, err := r.svc.Changes().List(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		return l
	}
	AfterEach(func() {
		if CurrentSpecReport().Failed() && bot != nil {
			GinkgoWriter.Printf("ANSWERS %q\nRESULTS %+v\n", bot.answers(), r.h.ResultsFor("propose_change"))
		}
	})

	It("is written in the chat for a paired sender, confirmed by yes and undone by undo", func() {
		setup(true, []string{"manuals"})
		say(paired, "add", 2)
		Expect(list()).To(HaveLen(1))
		Expect(list()[0].Status).To(Equal(contract.ProposalPending))
		asked := strings.Join(bot.answers(), "\n")
		Expect(asked).To(ContainSubstring(`"manuals"`))
		Expect(asked).To(ContainSubstring("Reset steps"))
		Expect(asked).To(ContainSubstring("Hold reset for ten seconds."))
		Expect(asked).To(ContainSubstring(`Reply "yes"`))
		Expect(fk.held()).To(BeEmpty())

		say(paired, "yes", 1)
		Expect(list()[0].Status).To(Equal(contract.ProposalApplied))
		Expect(fk.held()).To(HaveLen(1))
		Expect(bot.answers()[len(bot.answers())-1]).To(ContainSubstring("Added to manuals"))

		say(paired, "undo", 1)
		Expect(list()[0].Status).To(Equal(contract.ProposalReverted))
		Expect(fk.held()).To(BeEmpty())
	})

	It("is dropped by no, and a code answers when several wait", func() {
		setup(true, []string{"manuals"})
		say(paired, "add", 2)
		say(paired, "no", 1)
		Expect(list()[0].Status).To(Equal(contract.ProposalDismissed))
		Expect(fk.held()).To(BeEmpty())
	})

	It("does not write a flagged text in the chat, and a yes does not confirm it, but a no still drops it", func() {
		setup(true, []string{"manuals"})
		say(paired, "leak", 1)
		Expect(list()).To(HaveLen(1))
		Expect(list()[0].Warning).To(Equal(contract.WarningSecret))
		Expect(strings.Join(bot.answers(), "\n")).NotTo(ContainSubstring("correct-horse"), "the secret is not repeated in the chat")
		Expect(r.h.ResultsFor("propose_change")[0].Out).To(ContainSubstring("cannot be confirmed from a chat"))

		say(paired, "yes", 1)
		Expect(bot.answers()[len(bot.answers())-1]).To(ContainSubstring("cannot be confirmed from a chat"))
		Expect(list()[0].Status).To(Equal(contract.ProposalPending))
		Expect(fk.held()).To(BeEmpty())

		say(paired, "no", 1)
		Expect(list()[0].Status).To(Equal(contract.ProposalDismissed))
	})

	It("is refused for a listed sender who is not paired: the capability is sensitive", func() {
		setup(true, []string{"manuals"})
		say(listed, "add", 1)
		Expect(list()).To(BeEmpty())
		res := r.h.ResultsFor("propose_change")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(MatchError(ContainSubstring("not permitted")))
		Expect(fk.held()).To(BeEmpty())
	})

	Describe("the connection page", func() {
		decision := func(v studio.AccessView, subject string) studio.Decision {
			i := slices.IndexFunc(v.Subjects, func(s studio.AccessSubjectView) bool { return s.ID == subject })
			Expect(i).To(BeNumerically(">=", 0), subject)
			j := slices.IndexFunc(v.Subjects[i].Decisions, func(d studio.Decision) bool { return d.Capability == contract.CapKnowledgeWrite })
			Expect(j).To(BeNumerically(">=", 0))
			return v.Subjects[i].Decisions[j]
		}
		saved := func() studio.AccessView {
			v, err := r.conns.Access(ctx, alice, b.ID)
			Expect(err).NotTo(HaveOccurred())
			return v
		}

		It("says a paired sender may and a listed one or a channel member needs pairing", func() {
			setup(true, []string{"manuals"})
			v := saved()
			Expect(decision(v, "paired").Allowed).To(BeTrue())
			for _, who := range []string{"listed", "anyone"} {
				d := decision(v, who)
				Expect(d.Allowed).To(BeFalse(), who)
				Expect(d.Code).To(Equal(studio.CodeSenderNeedsPairing), who)
				Expect(d.BlockedBy).To(Equal(studio.BySender))
				Expect(d.Fix).To(Equal(&studio.Fix{Kind: studio.FixPairSender, Target: "pairing"}))
			}
		})

		It("names the server when the embedding model is missing", func() {
			setup(false, []string{"manuals"})
			d := decision(saved(), "paired")
			Expect(d.Allowed).To(BeFalse())
			Expect(d.Code).To(Equal(studio.CodeKnowledgeNoEmbedding))
			Expect(d.BlockedBy).To(Equal(studio.ByServer))
			Expect(d.Message).To(Equal("The embedding model for Knowledge is not installed, so the bot cannot write to knowledge. Install it in Models."))
			Expect(studio.BlockSay(d.Code, d.Capability)).To(Equal("The server cannot index new texts right now, so the bot cannot write to knowledge."))
		})

		It("names the agent when it has no collection on its list", func() {
			setup(true, nil)
			d := decision(saved(), "paired")
			Expect(d.Code).To(Equal(studio.CodeKnowledgeNone))
			Expect(d.BlockedBy).To(Equal(studio.ByAgent))
			Expect(d.Fix).To(Equal(&studio.Fix{Kind: studio.FixOpenAgent, Target: r.agent.ID, Field: "collections"}))
			Expect(d.Message).To(ContainSubstring("No collection is on the list of the agent"))
		})

		It("names the Tools switch of Knowledge, which the grant nests under", func() {
			setup(true, []string{"manuals"})
			ag := r.agent
			ag.BuiltinsOff = []string{"collections"}
			saved2, err := r.svc.SaveAgent(ctx, alice, ag)
			Expect(err).NotTo(HaveOccurred())
			r.agent = saved2
			d := decision(saved(), "paired")
			Expect(d.Code).To(Equal(studio.CodeToolsSwitchOff))
			Expect(d.Fix).To(Equal(&studio.Fix{Kind: studio.FixOpenAgent, Target: r.agent.ID, Field: "tools:collections"}))
		})

		It("names the connection when the owner did not tick it, and the preview answers for a draft", func() {
			setup(true, []string{"manuals"})
			v, err := r.conns.AccessPreview(ctx, alice, b.ID, studio.AccessDraft{Caps: []string{contract.CapMemoryWrite}, Sender: studio.SenderPaired})
			Expect(err).NotTo(HaveOccurred())
			Expect(v.Preview).To(BeTrue())
			d := decision(v, "paired")
			Expect(d.Code).To(Equal(studio.CodeConnectionPermissionOff))
			Expect(d.Fix).To(Equal(&studio.Fix{Kind: studio.FixOpenConnection, Target: "caps", Field: contract.CapKnowledgeWrite}))
			v, err = r.conns.AccessPreview(ctx, alice, b.ID, studio.AccessDraft{Caps: caps, Sender: studio.SenderPaired})
			Expect(err).NotTo(HaveOccurred())
			Expect(decision(v, "paired").Allowed).To(BeTrue())
		})

		It("is never usual: the preset does not tick it", func() {
			Expect(contract.SensitiveCapability(contract.CapKnowledgeWrite)).To(BeTrue())
			Expect(contract.UsualConnectorCapabilities()).NotTo(ContainElement(contract.CapKnowledgeWrite))
		})
	})
})
