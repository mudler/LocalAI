package studio_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
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
	chats store.Store
	files *files.Service
	// transfers is the table of file handles over the blob store of files: the
	// connector rigs give it to the add-ons.
	transfers *hostops.Transfers
	agents    agents.Store
	notices   notify.Store
	trg       triggers.Store
	clock     *tclock
	users     func(string) error
	allowed   func(*auth.User, string) bool
	addons    *fakeAddons
	net       *testutil.FakeBus  // when set, notifications travel over it, as between frontends
	deps      func(*studio.Deps) // when set, it changes the service's dependencies last
}

func newShared() *shared {
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	ag, _ := agents.NewJSON("")
	ns, _ := notify.NewJSON("")
	ts, _ := triggers.NewJSON("")
	fs, blobs := newFilesWithBlobs()
	return &shared{chats: st, files: fs, transfers: hostops.NewTransfers(blobs), agents: ag, notices: ns, trg: ts,
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

	It("a run ends its first message with the closing instruction, once", func() {
		r := newTrigRig(saySummary)
		tr := r.schedule(studio.TriggerInput{Prompt: "Refresh data/news.json. Finish with one sentence."})
		r.due()
		run := r.waitEnded(tr.ID)
		sent := r.h.Sent(r.chatOf(run).ID)
		Expect(sent).To(HaveLen(1), "first message = %q", sent)
		const closing = "Do not print JSON or code unless the task asks the user for it."
		Expect(sent[0]).To(HaveSuffix(closing), "first message = %q", sent)
		Expect(strings.Count(sent[0], closing)).To(Equal(1), "first message = %q", sent)
		Expect(sent[0]).To(HavePrefix("Refresh data/news.json."), "first message = %q", sent)
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
		// The run's tools are refused for want of a grant, so it ends as blocked; this spec is about something else.
		Expect(run.Status).To(BeElementOf(triggers.StatusDone, triggers.StatusBlocked), "run = %+v", run)
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

	It("reads the run limit of the operator when a run starts", func() {
		block := make(chan struct{})
		DeferCleanup(func() { close(block) })
		var limit atomic.Int64
		limit.Store(int64(time.Hour))
		sh := newShared()
		r := sh.rig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Block: block}}
		}, studio.TriggerOptions{Limits: func() studio.TriggerLimits {
			return studio.TriggerLimits{RunLimit: time.Duration(limit.Load())}
		}})
		Expect(r.host.Limits().RunLimit).To(Equal(time.Hour))
		limit.Store(int64(150 * time.Millisecond)) // changed after the host started
		tr := r.schedule(studio.TriggerInput{})
		r.due()
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusFailed), "run = %+v", run)
		Expect(run.Reason).To(Equal("took longer than 150ms"), "run = %+v", run)
	})

	It("holds a run to the least of its own time limit and the operator's", func() {
		for _, c := range []struct {
			name          string
			own, ceiling  time.Duration
			want          string
			wantEffective time.Duration
		}{
			{"own below the ceiling", 150 * time.Millisecond, time.Hour, "took longer than 150ms", 150 * time.Millisecond},
			{"own above the ceiling", time.Hour, 150 * time.Millisecond, "took longer than 150ms", 150 * time.Millisecond},
		} {
			block := make(chan struct{})
			var ceiling atomic.Int64
			ceiling.Store(int64(c.ceiling))
			sh := newShared()
			r := sh.rig(func(workspace.Actor, string) []studiotest.ToolStep {
				return []studiotest.ToolStep{{Block: block}}
			}, studio.TriggerOptions{Limits: func() studio.TriggerLimits {
				return studio.TriggerLimits{RunLimit: time.Duration(ceiling.Load())}
			}})
			tr := r.schedule(studio.TriggerInput{})
			// The API refuses less than a minute; the store holds what it is given,
			// which keeps the spec short.
			tr.RunLimit = c.own
			Expect(sh.trg.PutTrigger(context.Background(), tr)).To(Succeed())
			Expect(r.host.RunLimitOf(tr)).To(Equal(c.wantEffective), c.name)
			r.due()
			run := r.waitEnded(tr.ID)
			close(block)
			Expect(run.Status).To(Equal(triggers.StatusFailed), "%s: run = %+v", c.name, run)
			Expect(run.Reason).To(Equal(c.want), c.name)
		}
	})

	It("follows a lowered ceiling at once for a trigger with a longer limit of its own", func() {
		var ceiling atomic.Int64
		ceiling.Store(int64(time.Hour))
		r := newShared().rig(saySummary, studio.TriggerOptions{Limits: func() studio.TriggerLimits {
			return studio.TriggerLimits{RunLimit: time.Duration(ceiling.Load())}
		}})
		tr := r.schedule(studio.TriggerInput{RunLimit: 30 * time.Minute})
		Expect(r.host.RunLimitOf(tr)).To(Equal(30 * time.Minute))
		ceiling.Store(int64(10 * time.Minute))
		Expect(r.host.RunLimitOf(tr)).To(Equal(10 * time.Minute))
		Expect(tr.RunLimit).To(Equal(30*time.Minute), "stored as set")
	})

	It("refuses a time limit outside 1m to 24h and keeps one above the ceiling", func() {
		r := newTrigRig(saySummary)
		in := studio.TriggerInput{Name: "T", Prompt: "p", Kind: triggers.KindSchedule, Schedule: "daily 08:00", TZ: "UTC", Model: "m", Enabled: true}
		for _, d := range []time.Duration{time.Second, -time.Minute, 25 * time.Hour} {
			in.RunLimit = d
			_, err := r.host.CreateTrigger(context.Background(), alice, in)
			Expect(err).To(MatchError(studio.ErrBadTrigger), "%s", d)
			Expect(err.Error()).To(ContainSubstring("time limit must be empty or"))
		}
		in.RunLimit = 20 * time.Hour // above the default ceiling of 15m
		tr, err := r.host.CreateTrigger(context.Background(), alice, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(tr.RunLimit).To(Equal(20 * time.Hour))
		Expect(r.host.RunLimitOf(tr)).To(Equal(studio.DefaultRunLimit))
	})

	It("takes the default limits when the operator sets none", func() {
		r := newTrigRig(saySummary)
		Expect(r.host.Limits()).To(Equal(studio.TriggerLimits{
			RunLimit: studio.DefaultRunLimit, MaxPerUser: studio.DefaultMaxTriggersPerUser, MaxDailyCap: triggers.MaxDailyCap,
		}))
	})

	It("reads the most triggers of a user at each use and names it in the refusal", func() {
		var most atomic.Int64
		most.Store(2)
		r := newShared().rig(saySummary, studio.TriggerOptions{Limits: func() studio.TriggerLimits {
			return studio.TriggerLimits{MaxPerUser: int(most.Load())}
		}})
		in := studio.TriggerInput{Name: "T", Prompt: "p", Kind: triggers.KindSchedule, Schedule: "daily 08:00", TZ: "UTC", Model: "m", Enabled: true}
		for range 2 {
			_, err := r.host.CreateTrigger(context.Background(), alice, in)
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := r.host.CreateTrigger(context.Background(), alice, in)
		Expect(err).To(MatchError(studio.ErrTooManyTriggers))
		Expect(err.Error()).To(ContainSubstring("at most 2 triggers"))
		most.Store(3)
		_, err = r.host.CreateTrigger(context.Background(), alice, in)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.CreateTrigger(context.Background(), alice, in)
		Expect(err.Error()).To(ContainSubstring("at most 3 triggers"))
	})

	It("refuses a daily limit above the highest one of the operator, and holds an older trigger to it", func() {
		var top atomic.Int64
		top.Store(1000)
		r := newShared().rig(saySummary, studio.TriggerOptions{Limits: func() studio.TriggerLimits {
			return studio.TriggerLimits{MaxDailyCap: int(top.Load())}
		}})
		in := studio.TriggerInput{Name: "T", Prompt: "p", Kind: triggers.KindSchedule, Schedule: "daily 08:00", TZ: "UTC", Model: "m", Enabled: true, DailyCap: 500}
		tr, err := r.host.CreateTrigger(context.Background(), alice, in)
		Expect(err).NotTo(HaveOccurred())
		top.Store(10)
		_, err = r.host.CreateTrigger(context.Background(), alice, in)
		Expect(err).To(MatchError(ContainSubstring("daily limit must be 0 to 10")))
		in.DailyCap = 10
		_, err = r.host.CreateTrigger(context.Background(), alice, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(tr.DailyCap).To(Equal(500)) // stored as set; the runner holds it to 10
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
			wg.Go(func() { ; _ = r.host.Pass(context.Background()) })
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
		reaped, err := sh.chats.ReapTurns(ctx, -time.Hour)
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

var _ = Describe("trigger runs of a scoped agent", func() {
	// arrive writes a file and lets its quiet period end.
	arrive := func(s *scopedTrig, rel string) {
		GinkgoHelper()
		s.put(rel, "hello")
		s.pass()
		s.sh.clock.Advance(2 * time.Second)
		s.pass()
	}
	allRuns := func(s *scopedTrig, id string) []triggers.Run {
		GinkgoHelper()
		runs, err := s.host.TriggerRuns(s.ctx, alice, id, 0)
		Expect(err).NotTo(HaveOccurred())
		return runs
	}

	It("watches only inside the agent's folder", func() {
		s := newScopedTrig(saySummary)
		tr, err := s.fileTrigger("/Projects/Acme")
		Expect(err).NotTo(HaveOccurred())
		s.pass() // reads the feed: nothing yet
		arrive(s, "/Projects/Other/x.txt")
		arrive(s, "/Projects/AcmeX/x.txt")
		arrive(s, "/top.txt")
		Expect(allRuns(s, tr.ID)).To(BeEmpty(), "a file outside the folder started a run")
		arrive(s, "/Projects/Acme/In/a.txt")
		run := s.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusDone), "run = %+v", run)
		Expect(run.File).To(Equal(s.home + "/Projects/Acme/In/a.txt"))
		sent := s.h.Sent(run.ChatID)
		Expect(sent).To(HaveLen(1))
		// The run names the file as its chat sees it: the agent's folder is
		// /workspace/home.
		Expect(sent[0]).To(ContainSubstring("A new file arrived: /workspace/home/In/a.txt"), "first message = %q", sent[0])
		Expect(sent[0]).NotTo(ContainSubstring("/Projects"), "first message = %q", sent[0])
		Expect(allRuns(s, tr.ID)).To(HaveLen(1))
	})

	It("skips and switches off a run outside the folder", func() {
		s := newScopedTrig(saySummary)
		// The trigger was made while the agent had no folder; the folder came later.
		plain, err := s.svc.SaveAgent(s.ctx, alice, agents.Agent{Name: "Later", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		tr, err := s.host.CreateTrigger(s.ctx, alice, studio.TriggerInput{Name: "Other", Prompt: "Sort it.", Kind: triggers.KindFile,
			Folder: "/Projects/Other", Quiet: time.Second, AgentID: plain.ID, Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		_, err = s.svc.SaveAgentWithFolder(s.ctx, alice, plain, &studio.AgentFolderWrite{Path: "/Projects/Acme", Mode: agents.FolderWrite})
		Expect(err).NotTo(HaveOccurred())
		s.pass()
		arrive(s, "/Projects/Other/x.txt")
		run := s.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusSkipped), "run = %+v", run)
		Expect(run.Reason).To(ContainSubstring("not inside the agent's folder"), "run = %+v", run)
		Expect(run.ChatID).To(BeEmpty(), "a run outside the folder made a chat")
		got, _ := s.host.GetTrigger(s.ctx, alice, tr.ID)
		Expect(got.Enabled).To(BeFalse(), "the trigger stayed on")
		chats, _ := s.svc.List(s.ctx, alice, 100)
		Expect(chats).To(BeEmpty(), "chats = %+v", chats)
		var bodies []string
		for _, n := range s.notes() {
			bodies = append(bodies, n.Title+": "+n.Body)
		}
		Expect(strings.Join(bodies, "\n")).To(ContainSubstring("not inside the agent's folder"), "notifications = %q", bodies)
	})

	It("skips and switches off a run whose agent folder is missing, without reading the home", func() {
		var reads []string
		var mu sync.Mutex
		s := newScopedTrig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			mu.Lock()
			reads = append(reads, text)
			mu.Unlock()
			return []studiotest.ToolStep{{Say: "ran"}}
		})
		s.put("/AGENTS.md", "HOME-RULES-MARKER")
		tr := s.schedule(studio.TriggerInput{AgentID: s.agent.ID, Model: ""})
		s.remove("/Projects/Acme")
		s.due()
		run := s.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusSkipped), "run = %+v", run)
		Expect(run.Reason).To(Equal("the agent's folder is missing"), "run = %+v", run)
		Expect(run.ChatID).To(BeEmpty())
		got, _ := s.host.GetTrigger(s.ctx, alice, tr.ID)
		Expect(got.Enabled).To(BeFalse(), "the trigger stayed on")
		chats, _ := s.svc.List(s.ctx, alice, 100)
		Expect(chats).To(BeEmpty(), "a run with a missing folder made a chat: %+v", chats)
		mu.Lock()
		Expect(reads).To(BeEmpty(), "a turn ran: %q", reads)
		mu.Unlock()
		var bodies []string
		for _, n := range s.notes() {
			bodies = append(bodies, n.Title+": "+n.Body)
		}
		Expect(strings.Join(bodies, "\n")).To(ContainSubstring("the agent's folder is missing"), "notifications = %q", bodies)
		Expect(strings.Join(bodies, "\n")).NotTo(ContainSubstring("HOME-RULES-MARKER"))
	})

	It("skips a run whose agent's folder was never chosen", func() {
		s := newScopedTrig(saySummary)
		tr := s.schedule(studio.TriggerInput{AgentID: s.agent.ID})
		// An imported agent whose folder does not exist here: limited, not chosen.
		unset := s.agent
		unset.Folders, unset.FolderID = []agents.AgentFolder{{Mode: agents.FolderRead}}, 0
		Expect(s.sh.agents.Put(s.ctx, unset)).To(Succeed())
		s.due()
		run := s.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusSkipped), "run = %+v", run)
		Expect(run.Reason).To(Equal("the agent's folder is missing"), "run = %+v", run)
		chats, _ := s.svc.List(s.ctx, alice, 100)
		Expect(chats).To(BeEmpty())
	})

	It("reads the rules from the agent's folder for a non-file trigger", func() {
		s := newScopedTrig(saySummary)
		s.put("/AGENTS.md", "HOME-RULES-MARKER")
		s.put("/Projects/AGENTS.md", "PROJECTS-RULES-MARKER")
		s.put("/Projects/Acme/AGENTS.md", "ACME-RULES-MARKER")
		s.put("/Projects/Other/AGENTS.md", "OTHER-RULES-MARKER")
		tr := s.schedule(studio.TriggerInput{AgentID: s.agent.ID})
		s.due()
		run := s.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusDone), "run = %+v", run)
		sent := s.h.Sent(run.ChatID)
		Expect(sent).To(HaveLen(1))
		Expect(sent[0]).To(ContainSubstring("ACME-RULES-MARKER"))
		Expect(sent[0]).To(ContainSubstring("# Folder rules: /workspace/home"), "first message = %q", sent[0])
		for _, m := range []string{"HOME-RULES-MARKER", "PROJECTS-RULES-MARKER", "OTHER-RULES-MARKER", "/Projects"} {
			Expect(sent[0]).NotTo(ContainSubstring(m), "first message = %q", sent[0])
		}
	})

	It("reads the rules of a file trigger from its folder up to the agent's folder only", func() {
		s := newScopedTrig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "read_file", Args: `{"path":"/In/a.txt"}`}, {Say: "read it"}}
		})
		s.put("/Projects/AGENTS.md", "PROJECTS-RULES-MARKER")
		s.put("/Projects/Acme/AGENTS.md", "ACME-RULES-MARKER")
		s.put("/Projects/Acme/In/AGENTS.md", "IN-RULES-MARKER")
		tr, err := s.fileTrigger("/Projects/Acme/In")
		Expect(err).NotTo(HaveOccurred())
		s.pass()
		arrive(s, "/Projects/Acme/In/a.txt")
		run := s.waitEnded(tr.ID)
		sent := s.h.Sent(run.ChatID)
		Expect(sent).To(HaveLen(1))
		Expect(sent[0]).To(ContainSubstring("ACME-RULES-MARKER"))
		Expect(sent[0]).To(ContainSubstring("IN-RULES-MARKER"))
		Expect(sent[0]).To(ContainSubstring("# Folder rules: /workspace/home/In"), "first message = %q", sent[0])
		Expect(sent[0]).NotTo(ContainSubstring("PROJECTS-RULES-MARKER"))
		// The rules came in the first message: the turn note does not point
		// at them again.
		Expect(s.h.TurnPrompt(run.ChatID)).NotTo(ContainSubstring("AGENTS.md"), "turn prompt = %q", s.h.TurnPrompt(run.ChatID))
		reads := s.h.ResultsFor("read_file")
		Expect(reads).To(HaveLen(1))
		Expect(reads[0].Out).To(ContainSubstring("hello"))
		Expect(reads[0].Out).NotTo(ContainSubstring("AGENTS.md"), "the rules of the run's folder were pointed at again: %q", reads[0].Out)
	})

	It("a scheduled run of a scoped agent reaches only its folder", func() {
		var env workspace.Actor
		var mu sync.Mutex
		s := newScopedTrig(func(a workspace.Actor, _ string) []studiotest.ToolStep {
			mu.Lock()
			env = a
			mu.Unlock()
			return []studiotest.ToolStep{
				{Tool: "read_file", Args: `{"path":"/Projects/Other/secret.txt"}`},
				{Tool: "read_file", Args: `{"path":"/workspace/home/../Other/secret.txt"}`},
				{Tool: "list_files", Args: `{"path":"/"}`},
				{Tool: "write_file", Args: `{"path":"/note.txt","content":"inside"}`},
				{Say: "done"},
			}
		})
		s.put("/Projects/Other/secret.txt", "OUTSIDE-SECRET-MARKER")
		// A run that nobody watches writes only with act.files (the action policy).
		tr := s.schedule(studio.TriggerInput{AgentID: s.agent.ID, Acts: []string{"act.files"}})
		s.due()
		run := s.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusDone), "run = %+v", run)
		mu.Lock()
		Expect(workspace.HasFence(env)).To(BeTrue(), "the run's actor has no fence")
		mu.Unlock()
		for _, res := range s.h.Results() {
			Expect(res.Out).NotTo(ContainSubstring("OUTSIDE-SECRET-MARKER"), "%+v", res)
			Expect(res.Out).NotTo(ContainSubstring("Other"), "%+v", res)
		}
		n, err := s.sh.files.Store().Stat(s.ctx, s.act, s.home+"/Projects/Acme/note.txt")
		Expect(err).NotTo(HaveOccurred(), "the run did not write inside its folder")
		Expect(n.Kind).To(Equal(workspace.KindFile))
		_, err = s.sh.files.Store().Stat(s.ctx, s.act, s.home+"/note.txt")
		Expect(err).To(MatchError(workspace.ErrNotFound), "the run wrote at the home root")
	})
})
