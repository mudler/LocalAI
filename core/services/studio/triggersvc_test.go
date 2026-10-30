package studio_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/testutil"
)

type tclock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *tclock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *tclock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// shared is what two frontends of one cluster share: every store.
type shared struct {
	chats   store.Store
	files   *files.Service
	agents  agents.Store
	notices notify.Store
	trg     triggers.Store
	clock   *tclock
	users   func(string) error
	allowed func(*auth.User, string) bool
	addons  *fakeAddons
	net     *testutil.FakeBus  // when set, notifications travel over it, as between frontends
	deps    func(*studio.Deps) // when set, it changes the service's dependencies last
}

func newShared() *shared {
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	ag, _ := agents.NewJSON("")
	ns, _ := notify.NewJSON("")
	ts, _ := triggers.NewJSON("")
	return &shared{chats: st, files: newFiles(), agents: ag, notices: ns, trg: ts,
		clock:  &tclock{t: time.Date(2026, 10, 27, 7, 0, 0, 0, time.UTC)},
		addons: &fakeAddons{tools: map[string][]studio.AddonTool{alice: {addonTool("office", "read-doc"), {Name: "addon__sandbox__bash", AddonID: "sandbox", Action: true, Description: "bash"}}}},
	}
}

type trigRig struct {
	sh    *shared
	svc   *studio.Service
	h     *studiotest.ToolHarness
	host  *studio.Triggers
	inbox *notify.Service
}

func (sh *shared) rig(script func(workspace.Actor, string) []studiotest.ToolStep, o studio.TriggerOptions) *trigRig {
	var fan notify.Fanout
	if sh.net != nil {
		fan = notify.NewBroadcastFanout(sh.net, nil)
	}
	inbox := notify.NewService(notify.Options{Store: sh.notices, Clock: sh.clock.Now, Fanout: fan})
	svc, h := newToolService(script, func(d *studio.Deps) {
		d.Store, d.Files, d.Agents, d.Notify, d.AddonTools = sh.chats, sh.files, sh.agents, inbox, sh.addons
		d.Allowed = sh.allowed
		if sh.users != nil {
			d.Users = func(_ context.Context, id string) (*auth.User, error) { return &auth.User{ID: id}, sh.users(id) }
		}
		if sh.deps != nil {
			sh.deps(d)
		}
	})
	o.Store, o.Clock = sh.trg, sh.clock.Now
	if o.Tick == 0 {
		o.Tick = time.Hour // tests call Pass
	}
	host, err := studio.NewTriggers(svc, o)
	Expect(err).NotTo(HaveOccurred())
	svc.SetTriggers(host)
	DeferCleanup(host.Close)
	return &trigRig{sh: sh, svc: svc, h: h, host: host, inbox: inbox}
}

func newTrigRig(script func(workspace.Actor, string) []studiotest.ToolStep) *trigRig {
	return newShared().rig(script, studio.TriggerOptions{})
}

func (r *trigRig) schedule(in studio.TriggerInput) triggers.Trigger {
	if in.Kind == "" {
		in.Kind, in.Schedule, in.TZ = triggers.KindSchedule, "daily 08:00", "UTC"
	}
	if in.Name == "" {
		in.Name = "Nightly review"
	}
	if in.Prompt == "" {
		in.Prompt = "Review the open items."
	}
	if in.Model == "" {
		in.Model = "m"
	}
	in.Enabled = true
	tr, err := r.host.CreateTrigger(context.Background(), alice, in)
	Expect(err).NotTo(HaveOccurred())
	return tr
}

func (r *trigRig) pass() {
	Expect(r.host.Pass(context.Background())).To(Succeed())
}

// due moves the clock past 08:00 and makes a pass.
func (r *trigRig) due() {
	r.sh.clock.Advance(61 * time.Minute)
	r.pass()
}

func (r *trigRig) lastRun(id string) triggers.Run {
	runs, err := r.host.TriggerRuns(context.Background(), alice, id, 1)
	Expect(err).NotTo(HaveOccurred(), "no run")
	Expect(runs).NotTo(BeEmpty(), "no run: %v", err)
	return runs[0]
}

func (r *trigRig) waitEnded(id string) triggers.Run {
	var run triggers.Run
	until("the run to end", func() bool {
		runs, _ := r.host.TriggerRuns(context.Background(), alice, id, 1)
		if len(runs) == 0 {
			return false
		}
		run = runs[0]
		return run.Status.Final()
	})
	return run
}

func (r *trigRig) chatOf(run triggers.Run) session.ChatRecord {
	rec, err := r.svc.Get(context.Background(), alice, run.ChatID)
	Expect(err).NotTo(HaveOccurred(), "chat of the run")
	return rec
}

func (r *trigRig) notes() []notify.Notification {
	ns, err := r.inbox.List(context.Background(), alice, false, 0)
	Expect(err).NotTo(HaveOccurred())
	return ns
}

var saySummary = func(workspace.Actor, string) []studiotest.ToolStep {
	return []studiotest.ToolStep{{Say: "All caught up."}}
}

var _ = Describe("triggersvc", func() {
	It("a schedule run makes one chat of its owner and tells them", func() {
		r := newTrigRig(saySummary)
		tr := r.schedule(studio.TriggerInput{})
		r.pass() // before it is due: nothing
		runs, _ := r.host.TriggerRuns(context.Background(), alice, tr.ID, 0)
		Expect(runs).To(BeEmpty(), "a run before the due time: %+v", runs)
		r.due()
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusDone), "run = %+v", run)
		Expect(run.ChatID).NotTo(Equal(""), "run = %+v", run)
		rec := r.chatOf(run)
		Expect(rec.Origin).To(Equal(session.OriginTrigger), "chat = %+v", rec)
		Expect(rec.OriginID).To(Equal(tr.ID), "chat = %+v", rec)
		Expect(rec.Model).To(Equal("m"), "chat = %+v", rec)
		Expect(rec.Title).To(HavePrefix("Nightly review · 2026-10-27 08:"), "chat = %+v", rec)
		sent := r.h.Sent(rec.ID)
		Expect(sent).To(HaveLen(1), "first message = %q", sent)
		Expect(sent[0]).To(ContainSubstring("Review the open items."), "first message = %q", sent)
		ns := r.notes()
		Expect(ns).To(HaveLen(1), "notifications = %+v", ns)
		Expect(ns[0].Kind).To(Equal(notify.KindRunEnded), "notifications = %+v", ns)
		Expect(ns[0].Link).To(Equal(notify.ChatLink(rec.ID)), "notifications = %+v", ns)
		Expect(ns[0].Body).To(Equal("All caught up."), "notifications = %+v", ns)
		r.pass() // the same due time does not run again
		runs, _ = r.host.TriggerRuns(context.Background(), alice, tr.ID, 0)
		Expect(runs).To(HaveLen(1), "a due time ran %d times", len(runs))
		// Another user sees nothing of it.
		_, err := r.host.GetTrigger(context.Background(), "bob", tr.ID)
		Expect(err).To(MatchError(triggers.ErrNotFound), "bob read alice's trigger: %v", err)
	})

	It("a file arrival starts one run that names the file and its own writes start none", func() {
		sh := newShared()
		ctx := context.Background()
		Expect(sh.files.EnsureHome(ctx, alice)).To(Succeed())
		act := sh.files.Actor(alice)
		folder, _ := sh.files.Abs(alice, "/Contracts")
		_, err := sh.files.Store().Mkdir(ctx, act, folder)
		Expect(err).NotTo(HaveOccurred())
		r := sh.rig(func(a workspace.Actor, text string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "write_file", Args: `{"path":"/Contracts/summary.txt","content":"done"}`}, {Say: "wrote it"}}
		}, studio.TriggerOptions{})
		tr := r.schedule(studio.TriggerInput{Kind: triggers.KindFile, Folder: "/Contracts", Quiet: time.Second, Name: "Contracts"})
		r.pass() // reads the feed: the folder is empty

		_, err = sh.files.Store().Put(ctx, act, folder+"/a.txt", strings.NewReader("hello"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		r.pass()
		sh.clock.Advance(2 * time.Second) // the quiet period ends
		r.pass()
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusDone), "run = %+v", run)
		Expect(run.File).To(Equal(folder+"/a.txt"), "run = %+v", run)
		rec := r.chatOf(run)
		sent := r.h.Sent(rec.ID)
		Expect(sent).To(HaveLen(1), "first message = %q", sent)
		Expect(sent[0]).To(ContainSubstring("A new file arrived: /Contracts/a.txt"), "first message = %q", sent)
		// The run wrote summary.txt in the watched folder: that must not start the trigger.
		for range 3 {
			sh.clock.Advance(2 * time.Second)
			r.pass()
		}
		runs, _ := r.host.TriggerRuns(ctx, alice, tr.ID, 0)
		Expect(runs).To(HaveLen(1), "the run's own write started %d runs", len(runs))
	})

	It("a run whose agent was deleted fails switches the trigger off and tells the owner", func() {
		r := newTrigRig(saySummary)
		ctx := context.Background()
		ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Reviewer"})
		Expect(err).NotTo(HaveOccurred())
		tr := r.schedule(studio.TriggerInput{AgentID: ag.ID})
		Expect(r.svc.DeleteAgent(ctx, alice, ag.ID)).To(Succeed())
		r.due()
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusFailed), "run = %+v", run)
		Expect(run.Reason).To(Equal("the agent was deleted"), "run = %+v", run)
		got, _ := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(got.Enabled).To(BeFalse(), "the trigger stayed on")
		kinds := []string{}
		for _, n := range r.notes() {
			kinds = append(kinds, n.Kind)
		}
		slices.Sort(kinds)
		Expect(kinds).To(Equal([]string{notify.KindNeedsAttention, notify.KindRunFailed}), "notifications = %v", kinds)
		chats, _ := r.svc.List(ctx, alice, 100)
		Expect(chats).To(BeEmpty(), "a failed start left %d chats", len(chats))
	})

	It("a disabled owner skips the run and leaves the trigger on", func() {
		sh := newShared()
		sh.users = func(string) error { return studio.ErrUserDisabled }
		r := sh.rig(saySummary, studio.TriggerOptions{})
		tr := r.schedule(studio.TriggerInput{})
		r.due()
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusSkipped), "run = %+v", run)
		Expect(run.Reason).To(ContainSubstring("disabled"), "run = %+v", run)
		got, _ := r.host.GetTrigger(context.Background(), alice, tr.ID)
		Expect(got.Enabled).To(BeTrue(), "the trigger was switched off for a disabled account")
		ns := r.notes()
		Expect(ns).To(HaveLen(1), "notifications = %+v", ns)
		Expect(ns[0].Kind).To(Equal(notify.KindRunFailed), "notifications = %+v", ns)
	})

	It("an owner who lost the chats feature skips the run and leaves the trigger on", func() {
		sh := newShared()
		sh.users = func(string) error { return studio.ErrChatsOff }
		r := sh.rig(saySummary, studio.TriggerOptions{})
		tr := r.schedule(studio.TriggerInput{})
		r.due()
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusSkipped), "run = %+v, want a skip that says chats are off for the owner", run)
		Expect(run.Reason).To(ContainSubstring("chats"), "run = %+v, want a skip that says chats are off for the owner", run)
		got, _ := r.host.GetTrigger(context.Background(), alice, tr.ID)
		Expect(got.Enabled).To(BeTrue(), "the trigger was switched off; an administrator can give the feature back")
	})

	It("an owner who lost the triggers feature skips the run and leaves the trigger on", func() {
		sh := newShared()
		sh.users = func(string) error { return nil }
		sh.allowed = func(_ *auth.User, feature string) bool { return feature != auth.FeatureTriggers }
		r := sh.rig(saySummary, studio.TriggerOptions{})
		tr := r.schedule(studio.TriggerInput{})
		r.due()
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusSkipped), "run = %+v", run)
		Expect(run.Reason).To(ContainSubstring("no longer use triggers"), "run = %+v", run)
		got, _ := r.host.GetTrigger(context.Background(), alice, tr.ID)
		Expect(got.Enabled).To(BeTrue(), "the trigger was switched off for a missing permission; the owner may get it back")
	})

	It("an owner who is gone skips the run and switches the trigger off", func() {
		sh := newShared()
		sh.users = func(string) error { return studio.ErrNoUser }
		r := sh.rig(saySummary, studio.TriggerOptions{})
		tr := r.schedule(studio.TriggerInput{})
		r.due()
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusSkipped), "run = %+v", run)
		got, _ := r.host.GetTrigger(context.Background(), alice, tr.ID)
		Expect(got.Enabled).To(BeFalse(), "the trigger stayed on")
	})

	It("a folder that is gone skips the run and switches the trigger off", func() {
		sh := newShared()
		ctx := context.Background()
		_ = sh.files.EnsureHome(ctx, alice)
		folder, _ := sh.files.Abs(alice, "/Inbox")
		_, err := sh.files.Store().Mkdir(ctx, sh.files.Actor(alice), folder)
		Expect(err).NotTo(HaveOccurred())
		r := sh.rig(saySummary, studio.TriggerOptions{})
		tr := r.schedule(studio.TriggerInput{Kind: triggers.KindFile, Folder: "/Inbox", Quiet: time.Second})
		_, err = r.host.RunTrigger(ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred(), "a run by hand needs the folder too")
		Expect(sh.files.Store().Delete(ctx, sh.files.Actor(alice), folder)).To(Succeed())
		r.pass()
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusSkipped), "run = %+v", run)
		Expect(run.Reason).To(Equal(triggers.ReasonNotReadable), "run = %+v", run)
		got, _ := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(got.Enabled).To(BeFalse(), "the trigger stayed on")
	})

	It("a refused model request fails the run with the reason", func() {
		r := newTrigRig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Err: errors.New("the model refused the request " + session.MarkQuota)}}
		})
		tr := r.schedule(studio.TriggerInput{})
		r.due()
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusFailed), "run = %+v", run)
		Expect(run.Reason).To(Equal(session.ReasonQuota), "run = %+v", run)
		ns := r.notes()
		Expect(ns).To(HaveLen(1), "notifications = %+v", ns)
		Expect(ns[0].Kind).To(Equal(notify.KindRunFailed), "notifications = %+v", ns)
		Expect(ns[0].Body).To(Equal(session.ReasonQuota), "notifications = %+v", ns)
	})

	It("a trigger without an agent copies the switches and model of its source chat and gets no action tools", func() {
		r := newTrigRig(saySummary)
		ctx := context.Background()
		src, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "src-model"})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.svc.SetSkillsOff(ctx, alice, src.ID, []string{"noisy"})).To(Succeed())
		Expect(r.svc.SetAddonsOff(ctx, alice, src.ID, []string{"office"})).To(Succeed())
		tr := r.schedule(studio.TriggerInput{Model: "none", SourceChatID: src.ID})
		Expect(tr.Model).To(Equal("none"), "an explicit model wins over the source chat's")
		tr, err = r.host.UpdateTrigger(ctx, alice, tr.ID, studio.TriggerInput{Name: "Nightly review", Prompt: "Go.", Kind: triggers.KindSchedule,
			Schedule: "daily 08:00", TZ: "UTC", Enabled: true, SourceChatID: src.ID, Model: ""})
		Expect(err).NotTo(HaveOccurred(), "model after update = %q, %v", tr.Model, err)
		Expect(tr.Model).To(Equal("src-model"), "model after update = %q, %v", tr.Model, err)
		r.due()
		run := r.waitEnded(tr.ID)
		rec := r.chatOf(run)
		Expect(rec.Model).To(Equal("src-model"), "chat = %+v", rec)
		Expect(rec.SkillsOff).To(Equal([]string{"noisy"}), "chat = %+v", rec)
		Expect(rec.AddonsOff).To(Equal([]string{"office"}), "chat = %+v", rec)
		for _, n := range r.h.ToolNames(rec.ID) {
			Expect(n).NotTo(HavePrefix("addon__"), "an unattended run with no agent got the add-on tool %s", n)
		}
	})

	It("an agent that names an addon gives an unattended run its action tools", func() {
		r := newTrigRig(saySummary)
		ag, err := r.svc.SaveAgent(context.Background(), alice, agents.Agent{Name: "Coder", Addons: []string{"sandbox"}})
		Expect(err).NotTo(HaveOccurred())
		tr := r.schedule(studio.TriggerInput{AgentID: ag.ID})
		r.due()
		run := r.waitEnded(tr.ID)
		got := r.h.ToolNames(run.ChatID)
		Expect(got).To(ContainElement("addon__sandbox__bash"), "tools = %v, want the sandbox tool only", got)
		Expect(got).NotTo(ContainElement("addon__office__read-doc"), "tools = %v, want the sandbox tool only", got)
	})

	It("a run that takes too long is stopped and failed", func() {
		block := make(chan struct{})
		DeferCleanup(func() { close(block) })
		sh := newShared()
		r := sh.rig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Block: block}}
		}, studio.TriggerOptions{RunLimit: 200 * time.Millisecond})
		tr := r.schedule(studio.TriggerInput{})
		r.due()
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusFailed), "run = %+v", run)
		Expect(run.Reason).To(Equal("took longer than 200ms"), "run = %+v", run)
		until("the chat to stop", func() bool { return !turn(r.svc, alice, run.ChatID).Running })
	})

	It("the owner can stop a run without a browser open", func() {
		block := make(chan struct{})
		DeferCleanup(func() { close(block) })
		r := newTrigRig(func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Block: block}} })
		tr := r.schedule(studio.TriggerInput{})
		r.due()
		var run triggers.Run
		until("the run to start", func() bool {
			run = r.lastRun(tr.ID)
			return run.Status == triggers.StatusRunning && run.ChatID != ""
		})
		until("the turn to run", func() bool { return turn(r.svc, alice, run.ChatID).Running })
		Expect(r.svc.Stop(context.Background(), alice, run.ChatID)).To(Succeed())
		run = r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusDone), "run = %+v", run)
		Expect(run.Reason).To(Equal("stopped by the user"), "run = %+v", run)
	})

	It("after close nothing starts and close does not wait forever", func() {
		r := newTrigRig(saySummary)
		r.schedule(studio.TriggerInput{})
		done := make(chan struct{})
		go func() { r.host.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			Fail("Close did not return")
		}
		Expect(r.host.Pass(context.Background())).To(MatchError(studio.ErrTriggersClosed))
	})

	It("a run left running by a frontend that died is failed by the next leader", func() {
		sh := newShared()
		r := sh.rig(saySummary, studio.TriggerOptions{})
		tr := r.schedule(studio.TriggerInput{})
		ctx := context.Background()
		// The dead frontend had claimed the run and started a chat that is not running now.
		run := triggers.Run{ID: "r-old", TriggerID: tr.ID, DueKey: "2026-10-27T08:00:00Z", Due: sh.clock.Now(), Status: triggers.StatusRunning, ChatID: "gone-chat", Started: sh.clock.Now()}
		ok, err := sh.trg.Claim(ctx, run)
		Expect(err).NotTo(HaveOccurred(), "claim: %v %v", ok, err)
		Expect(ok).To(BeTrue(), "claim: %v %v", ok, err)
		r.pass()
		got, err := sh.trg.GetRun(ctx, "r-old")
		Expect(err).NotTo(HaveOccurred(), "run = %+v, %v", got, err)
		Expect(got.Status).To(Equal(triggers.StatusFailed), "run = %+v, %v", got, err)
		Expect(got.Reason).To(Equal(triggers.ReasonInterrupted), "run = %+v, %v", got, err)
		// The owner is told: a run that a restart cut short is a failed run.
		list, err := r.inbox.List(ctx, alice, false, 10)
		Expect(err).NotTo(HaveOccurred(), "notifications = %+v, %v", list, err)
		Expect(list).To(HaveLen(1), "notifications = %+v, %v", list, err)
		Expect(list[0].Kind).To(Equal(notify.KindRunFailed), "notifications = %+v, %v", list, err)
		Expect(list[0].Body).To(ContainSubstring("restart"), "notifications = %+v, %v", list, err)
		// And only once.
		r.pass()
		list, _ = r.inbox.List(ctx, alice, false, 10)
		Expect(list).To(HaveLen(1), "notifications after another pass = %d", len(list))
	})

	It("two frontends on one store run a due time once and the next leader ends a dead run", func() {
		block := make(chan struct{})
		DeferCleanup(func() { close(block) })
		sh := newShared()
		script := func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Block: block}} }
		a := sh.rig(script, studio.TriggerOptions{})
		b := sh.rig(script, studio.TriggerOptions{})
		tr := a.schedule(studio.TriggerInput{})
		sh.clock.Advance(61 * time.Minute)
		// Both frontends pass at the same time (a split brain): the claim is unique.
		var wg sync.WaitGroup
		for _, r := range []*trigRig{a, b} {
			wg.Add(1)
			go func() { defer wg.Done(); _ = r.host.Pass(context.Background()) }()
		}
		wg.Wait()
		ctx := context.Background()
		runs, _ := sh.trg.ListRuns(ctx, tr.ID, 0)
		Expect(runs).To(HaveLen(1), "a due time made %d runs", len(runs))
		var run triggers.Run
		until("the run to start", func() bool {
			run, _ = sh.trg.GetRun(ctx, runs[0].ID)
			return run.Status == triggers.StatusRunning && run.ChatID != ""
		})
		// The frontend that runs it dies: its host stops, and the reaper frees its chat.
		Expect(a.sh.chats).NotTo(BeNil(), "no store")
		reaped, err := sh.chats.ReapTurns(ctx, sh.clock.Now().Add(24*time.Hour).Add(time.Now().Sub(sh.clock.Now())))
		Expect(err).NotTo(HaveOccurred())
		Expect(reaped).NotTo(BeEmpty(), "the reaper found no running turn")
		// Whichever host supervised the run, the next pass of the new leader ends it.
		a.host.Close()
		Expect(b.host.Pass(ctx)).To(Succeed())
		until("the dead run to be failed", func() bool {
			got, _ := sh.trg.GetRun(ctx, run.ID)
			return got.Status == triggers.StatusFailed
		})
		got, _ := sh.trg.GetRun(ctx, run.ID)
		Expect(got.Reason).To(Equal(triggers.ReasonInterrupted), "reason = %q", got.Reason)
		info, _ := sh.chats.Turn(ctx, alice, run.ChatID)
		Expect(info.Running).To(BeFalse(), "the chat is still held")
	})

	// The model sees paths of the user's home ("/Contracts"), never the internal
	// path of the workspace.
	It("a run names folders as the user sees them", func() {
		sh := newShared()
		ctx := context.Background()
		Expect(sh.files.EnsureHome(ctx, alice)).To(Succeed())
		act := sh.files.Actor(alice)
		folder, _ := sh.files.Abs(alice, "/Contracts")
		_, err := sh.files.Store().Mkdir(ctx, act, folder)
		Expect(err).NotTo(HaveOccurred())
		_, err = sh.files.Store().Put(ctx, act, folder+"/AGENTS.md", strings.NewReader("Be brief."), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		r := sh.rig(func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }, studio.TriggerOptions{})
		tr := r.schedule(studio.TriggerInput{Kind: triggers.KindFile, Folder: "/Contracts", Quiet: time.Second, Name: "Contracts"})
		r.pass()
		_, err = sh.files.Store().Put(ctx, act, folder+"/a.txt", strings.NewReader("hello"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		r.pass()
		sh.clock.Advance(2 * time.Second)
		r.pass()
		run := r.waitEnded(tr.ID)
		sent := r.h.Sent(r.chatOf(run).ID)
		Expect(sent).To(HaveLen(1), "sent = %q", sent)
		Expect(sent[0]).To(ContainSubstring("# Folder rules: /Contracts"), "the first message holds an internal path (%s):\n%s", folder, sent[0])
		Expect(sent[0]).NotTo(ContainSubstring(folder), "the first message holds an internal path (%s):\n%s", folder, sent[0])
		got := r.host.RelPath(alice, run.File)
		Expect(got).To(Equal("/Contracts/a.txt"), "RelPath = %q", got)
	})
})
