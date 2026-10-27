package studio_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
	addons  *fakeAddons
	net     *testutil.FakeBus // when set, notifications travel over it, as between frontends
}

func newShared(t *testing.T) *shared {
	t.Helper()
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	ag, _ := agents.NewJSON("")
	ns, _ := notify.NewJSON("")
	ts, _ := triggers.NewJSON("")
	return &shared{chats: st, files: newFiles(t), agents: ag, notices: ns, trg: ts,
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

func (sh *shared) rig(t *testing.T, script func(workspace.Actor, string) []studiotest.ToolStep, o studio.TriggerOptions) *trigRig {
	t.Helper()
	var fan notify.Fanout
	if sh.net != nil {
		fan = notify.NewBroadcastFanout(sh.net, nil)
	}
	inbox := notify.NewService(notify.Options{Store: sh.notices, Clock: sh.clock.Now, Fanout: fan})
	svc, h := newToolService(t, script, func(d *studio.Deps) {
		d.Store, d.Files, d.Agents, d.Notify, d.AddonTools = sh.chats, sh.files, sh.agents, inbox, sh.addons
		if sh.users != nil {
			d.Users = func(_ context.Context, id string) (*auth.User, error) { return &auth.User{ID: id}, sh.users(id) }
		}
	})
	o.Store, o.Clock = sh.trg, sh.clock.Now
	if o.Tick == 0 {
		o.Tick = time.Hour // tests call Pass
	}
	host, err := studio.NewTriggers(svc, o)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetTriggers(host)
	t.Cleanup(host.Close)
	return &trigRig{sh: sh, svc: svc, h: h, host: host, inbox: inbox}
}

func newTrigRig(t *testing.T, script func(workspace.Actor, string) []studiotest.ToolStep) *trigRig {
	t.Helper()
	return newShared(t).rig(t, script, studio.TriggerOptions{})
}

func (r *trigRig) schedule(t *testing.T, in studio.TriggerInput) triggers.Trigger {
	t.Helper()
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
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func (r *trigRig) pass(t *testing.T) {
	t.Helper()
	if err := r.host.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// due moves the clock past 08:00 and makes a pass.
func (r *trigRig) due(t *testing.T) {
	t.Helper()
	r.sh.clock.Advance(61 * time.Minute)
	r.pass(t)
}

func (r *trigRig) lastRun(t *testing.T, id string) triggers.Run {
	t.Helper()
	runs, err := r.host.TriggerRuns(context.Background(), alice, id, 1)
	if err != nil || len(runs) == 0 {
		t.Fatalf("no run: %v", err)
	}
	return runs[0]
}

func (r *trigRig) waitEnded(t *testing.T, id string) triggers.Run {
	t.Helper()
	var run triggers.Run
	until(t, "the run to end", func() bool {
		runs, _ := r.host.TriggerRuns(context.Background(), alice, id, 1)
		if len(runs) == 0 {
			return false
		}
		run = runs[0]
		return run.Status.Final()
	})
	return run
}

func (r *trigRig) chatOf(t *testing.T, run triggers.Run) session.ChatRecord {
	t.Helper()
	rec, err := r.svc.Get(context.Background(), alice, run.ChatID)
	if err != nil {
		t.Fatalf("chat of the run: %v", err)
	}
	return rec
}

func (r *trigRig) notes(t *testing.T) []notify.Notification {
	t.Helper()
	ns, err := r.inbox.List(context.Background(), alice, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	return ns
}

var saySummary = func(workspace.Actor, string) []studiotest.ToolStep {
	return []studiotest.ToolStep{{Say: "All caught up."}}
}

func TestAScheduleRunMakesOneChatOfItsOwnerAndTellsThem(t *testing.T) {
	r := newTrigRig(t, saySummary)
	tr := r.schedule(t, studio.TriggerInput{})
	r.pass(t) // before it is due: nothing
	if runs, _ := r.host.TriggerRuns(context.Background(), alice, tr.ID, 0); len(runs) != 0 {
		t.Fatalf("a run before the due time: %+v", runs)
	}
	r.due(t)
	run := r.waitEnded(t, tr.ID)
	if run.Status != triggers.StatusDone || run.ChatID == "" {
		t.Fatalf("run = %+v", run)
	}
	rec := r.chatOf(t, run)
	if rec.Origin != session.OriginTrigger || rec.OriginID != tr.ID || rec.Model != "m" || !strings.HasPrefix(rec.Title, "Nightly review · 2026-10-27 08:") {
		t.Fatalf("chat = %+v", rec)
	}
	if sent := r.h.Sent(rec.ID); len(sent) != 1 || !strings.Contains(sent[0], "Review the open items.") {
		t.Fatalf("first message = %q", sent)
	}
	ns := r.notes(t)
	if len(ns) != 1 || ns[0].Kind != notify.KindRunEnded || ns[0].Link != notify.ChatLink(rec.ID) || ns[0].Body != "All caught up." {
		t.Fatalf("notifications = %+v", ns)
	}
	r.pass(t) // the same due time does not run again
	if runs, _ := r.host.TriggerRuns(context.Background(), alice, tr.ID, 0); len(runs) != 1 {
		t.Fatalf("a due time ran %d times", len(runs))
	}
	// Another user sees nothing of it.
	if _, err := r.host.GetTrigger(context.Background(), "bob", tr.ID); !errors.Is(err, triggers.ErrNotFound) {
		t.Fatalf("bob read alice's trigger: %v", err)
	}
}

func TestAFileArrivalStartsOneRunThatNamesTheFileAndItsOwnWritesStartNone(t *testing.T) {
	sh := newShared(t)
	ctx := context.Background()
	if err := sh.files.EnsureHome(ctx, alice); err != nil {
		t.Fatal(err)
	}
	act := sh.files.Actor(alice)
	folder, _ := sh.files.Abs(alice, "/Contracts")
	if _, err := sh.files.Store().Mkdir(ctx, act, folder); err != nil {
		t.Fatal(err)
	}
	r := sh.rig(t, func(a workspace.Actor, text string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Tool: "write_file", Args: `{"path":"/Contracts/summary.txt","content":"done"}`}, {Say: "wrote it"}}
	}, studio.TriggerOptions{})
	tr := r.schedule(t, studio.TriggerInput{Kind: triggers.KindFile, Folder: "/Contracts", Quiet: time.Second, Name: "Contracts"})
	r.pass(t) // reads the feed: the folder is empty

	if _, err := sh.files.Store().Put(ctx, act, folder+"/a.txt", strings.NewReader("hello"), workspace.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	r.pass(t)
	sh.clock.Advance(2 * time.Second) // the quiet period ends
	r.pass(t)
	run := r.waitEnded(t, tr.ID)
	if run.Status != triggers.StatusDone || run.File != folder+"/a.txt" {
		t.Fatalf("run = %+v", run)
	}
	rec := r.chatOf(t, run)
	if sent := r.h.Sent(rec.ID); len(sent) != 1 || !strings.Contains(sent[0], "A new file arrived: /Contracts/a.txt") {
		t.Fatalf("first message = %q", sent)
	}
	// The run wrote summary.txt in the watched folder: that must not start the trigger.
	for i := 0; i < 3; i++ {
		sh.clock.Advance(2 * time.Second)
		r.pass(t)
	}
	if runs, _ := r.host.TriggerRuns(ctx, alice, tr.ID, 0); len(runs) != 1 {
		t.Fatalf("the run's own write started %d runs", len(runs))
	}
}

func TestARunWhoseAgentWasDeletedFailsSwitchesTheTriggerOffAndTellsTheOwner(t *testing.T) {
	r := newTrigRig(t, saySummary)
	ctx := context.Background()
	ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	tr := r.schedule(t, studio.TriggerInput{AgentID: ag.ID})
	if err := r.svc.DeleteAgent(ctx, alice, ag.ID); err != nil {
		t.Fatal(err)
	}
	r.due(t)
	run := r.waitEnded(t, tr.ID)
	if run.Status != triggers.StatusFailed || run.Reason != "the agent was deleted" {
		t.Fatalf("run = %+v", run)
	}
	if got, _ := r.host.GetTrigger(ctx, alice, tr.ID); got.Enabled {
		t.Fatal("the trigger stayed on")
	}
	kinds := []string{}
	for _, n := range r.notes(t) {
		kinds = append(kinds, n.Kind)
	}
	slices.Sort(kinds)
	if !slices.Equal(kinds, []string{notify.KindNeedsAttention, notify.KindRunFailed}) {
		t.Fatalf("notifications = %v", kinds)
	}
	if chats, _ := r.svc.List(ctx, alice, 100); len(chats) != 0 {
		t.Fatalf("a failed start left %d chats", len(chats))
	}
}

func TestADisabledOwnerSkipsTheRunAndLeavesTheTriggerOn(t *testing.T) {
	sh := newShared(t)
	sh.users = func(string) error { return studio.ErrUserDisabled }
	r := sh.rig(t, saySummary, studio.TriggerOptions{})
	tr := r.schedule(t, studio.TriggerInput{})
	r.due(t)
	run := r.waitEnded(t, tr.ID)
	if run.Status != triggers.StatusSkipped || !strings.Contains(run.Reason, "disabled") {
		t.Fatalf("run = %+v", run)
	}
	if got, _ := r.host.GetTrigger(context.Background(), alice, tr.ID); !got.Enabled {
		t.Fatal("the trigger was switched off for a disabled account")
	}
	if ns := r.notes(t); len(ns) != 1 || ns[0].Kind != notify.KindRunFailed {
		t.Fatalf("notifications = %+v", ns)
	}
}

func TestAnOwnerWhoIsGoneSkipsTheRunAndSwitchesTheTriggerOff(t *testing.T) {
	sh := newShared(t)
	sh.users = func(string) error { return studio.ErrNoUser }
	r := sh.rig(t, saySummary, studio.TriggerOptions{})
	tr := r.schedule(t, studio.TriggerInput{})
	r.due(t)
	if run := r.waitEnded(t, tr.ID); run.Status != triggers.StatusSkipped {
		t.Fatalf("run = %+v", run)
	}
	if got, _ := r.host.GetTrigger(context.Background(), alice, tr.ID); got.Enabled {
		t.Fatal("the trigger stayed on")
	}
}

func TestAFolderThatIsGoneSkipsTheRunAndSwitchesTheTriggerOff(t *testing.T) {
	sh := newShared(t)
	ctx := context.Background()
	_ = sh.files.EnsureHome(ctx, alice)
	folder, _ := sh.files.Abs(alice, "/Inbox")
	if _, err := sh.files.Store().Mkdir(ctx, sh.files.Actor(alice), folder); err != nil {
		t.Fatal(err)
	}
	r := sh.rig(t, saySummary, studio.TriggerOptions{})
	tr := r.schedule(t, studio.TriggerInput{Kind: triggers.KindFile, Folder: "/Inbox", Quiet: time.Second})
	if _, err := r.host.RunTrigger(ctx, alice, tr.ID); err != nil { // a run by hand needs the folder too
		t.Fatal(err)
	}
	if err := sh.files.Store().Delete(ctx, sh.files.Actor(alice), folder); err != nil {
		t.Fatal(err)
	}
	r.pass(t)
	run := r.waitEnded(t, tr.ID)
	if run.Status != triggers.StatusSkipped || run.Reason != triggers.ReasonNotReadable {
		t.Fatalf("run = %+v", run)
	}
	if got, _ := r.host.GetTrigger(ctx, alice, tr.ID); got.Enabled {
		t.Fatal("the trigger stayed on")
	}
}

func TestARefusedModelRequestFailsTheRunWithTheReason(t *testing.T) {
	r := newTrigRig(t, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Err: errors.New("the model refused the request " + session.MarkQuota)}}
	})
	tr := r.schedule(t, studio.TriggerInput{})
	r.due(t)
	run := r.waitEnded(t, tr.ID)
	if run.Status != triggers.StatusFailed || run.Reason != session.ReasonQuota {
		t.Fatalf("run = %+v", run)
	}
	if ns := r.notes(t); len(ns) != 1 || ns[0].Kind != notify.KindRunFailed || ns[0].Body != session.ReasonQuota {
		t.Fatalf("notifications = %+v", ns)
	}
}

func TestATriggerWithoutAnAgentCopiesTheSwitchesAndModelOfItsSourceChatAndGetsNoActionTools(t *testing.T) {
	r := newTrigRig(t, saySummary)
	ctx := context.Background()
	src, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "src-model"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SetSkillsOff(ctx, alice, src.ID, []string{"noisy"}); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SetAddonsOff(ctx, alice, src.ID, []string{"office"}); err != nil {
		t.Fatal(err)
	}
	tr := r.schedule(t, studio.TriggerInput{Model: "none", SourceChatID: src.ID})
	if tr.Model != "none" { // an explicit model wins over the source chat's
		t.Fatalf("model = %q", tr.Model)
	}
	tr, err = r.host.UpdateTrigger(ctx, alice, tr.ID, studio.TriggerInput{Name: "Nightly review", Prompt: "Go.", Kind: triggers.KindSchedule,
		Schedule: "daily 08:00", TZ: "UTC", Enabled: true, SourceChatID: src.ID, Model: ""})
	if err != nil || tr.Model != "src-model" {
		t.Fatalf("model after update = %q, %v", tr.Model, err)
	}
	r.due(t)
	run := r.waitEnded(t, tr.ID)
	rec := r.chatOf(t, run)
	if rec.Model != "src-model" || !slices.Equal(rec.SkillsOff, []string{"noisy"}) || !slices.Equal(rec.AddonsOff, []string{"office"}) {
		t.Fatalf("chat = %+v", rec)
	}
	for _, n := range r.h.ToolNames(rec.ID) {
		if strings.HasPrefix(n, "addon__") {
			t.Errorf("an unattended run with no agent got the add-on tool %s", n)
		}
	}
}

func TestAnAgentThatNamesAnAddonGivesAnUnattendedRunItsActionTools(t *testing.T) {
	r := newTrigRig(t, saySummary)
	ag, err := r.svc.SaveAgent(context.Background(), alice, agents.Agent{Name: "Coder", Addons: []string{"sandbox"}})
	if err != nil {
		t.Fatal(err)
	}
	tr := r.schedule(t, studio.TriggerInput{AgentID: ag.ID})
	r.due(t)
	run := r.waitEnded(t, tr.ID)
	if got := r.h.ToolNames(run.ChatID); !slices.Contains(got, "addon__sandbox__bash") || slices.Contains(got, "addon__office__read-doc") {
		t.Fatalf("tools = %v, want the sandbox tool only", got)
	}
}

func TestARunThatTakesTooLongIsStoppedAndFailed(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	sh := newShared(t)
	r := sh.rig(t, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Block: block}}
	}, studio.TriggerOptions{RunLimit: 200 * time.Millisecond})
	tr := r.schedule(t, studio.TriggerInput{})
	r.due(t)
	run := r.waitEnded(t, tr.ID)
	if run.Status != triggers.StatusFailed || run.Reason != "took longer than 200ms" {
		t.Fatalf("run = %+v", run)
	}
	until(t, "the chat to stop", func() bool { return !turn(t, r.svc, alice, run.ChatID).Running })
}

func TestTheOwnerCanStopARunWithoutABrowserOpen(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	r := newTrigRig(t, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Block: block}} })
	tr := r.schedule(t, studio.TriggerInput{})
	r.due(t)
	var run triggers.Run
	until(t, "the run to start", func() bool {
		run = r.lastRun(t, tr.ID)
		return run.Status == triggers.StatusRunning && run.ChatID != ""
	})
	until(t, "the turn to run", func() bool { return turn(t, r.svc, alice, run.ChatID).Running })
	if err := r.svc.Stop(context.Background(), alice, run.ChatID); err != nil {
		t.Fatal(err)
	}
	run = r.waitEnded(t, tr.ID)
	if run.Status != triggers.StatusDone || run.Reason != "stopped by the user" {
		t.Fatalf("run = %+v", run)
	}
}

func TestAfterCloseNothingStartsAndCloseDoesNotWaitForever(t *testing.T) {
	r := newTrigRig(t, saySummary)
	r.schedule(t, studio.TriggerInput{})
	done := make(chan struct{})
	go func() { r.host.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return")
	}
	if err := r.host.Pass(context.Background()); !errors.Is(err, studio.ErrTriggersClosed) {
		t.Fatalf("Pass after Close = %v", err)
	}
}

func TestARunLeftRunningByAFrontendThatDiedIsFailedByTheNextLeader(t *testing.T) {
	sh := newShared(t)
	r := sh.rig(t, saySummary, studio.TriggerOptions{})
	tr := r.schedule(t, studio.TriggerInput{})
	ctx := context.Background()
	// The dead frontend had claimed the run and started a chat that is not running now.
	run := triggers.Run{ID: "r-old", TriggerID: tr.ID, DueKey: "2026-10-27T08:00:00Z", Due: sh.clock.Now(), Status: triggers.StatusRunning, ChatID: "gone-chat", Started: sh.clock.Now()}
	if ok, err := sh.trg.Claim(ctx, run); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	r.pass(t)
	got, err := sh.trg.GetRun(ctx, "r-old")
	if err != nil || got.Status != triggers.StatusFailed || got.Reason != triggers.ReasonInterrupted {
		t.Fatalf("run = %+v, %v", got, err)
	}
}

func TestTwoFrontendsOnOneStoreRunADueTimeOnceAndTheNextLeaderEndsADeadRun(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	sh := newShared(t)
	script := func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Block: block}} }
	a := sh.rig(t, script, studio.TriggerOptions{})
	b := sh.rig(t, script, studio.TriggerOptions{})
	tr := a.schedule(t, studio.TriggerInput{})
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
	if len(runs) != 1 {
		t.Fatalf("a due time made %d runs", len(runs))
	}
	var run triggers.Run
	until(t, "the run to start", func() bool {
		run, _ = sh.trg.GetRun(ctx, runs[0].ID)
		return run.Status == triggers.StatusRunning && run.ChatID != ""
	})
	// The frontend that runs it dies: its host stops, and the reaper frees its chat.
	if a.sh.chats == nil {
		t.Fatal("no store")
	}
	reaped, err := sh.chats.ReapTurns(ctx, sh.clock.Now().Add(24*time.Hour).Add(time.Now().Sub(sh.clock.Now())))
	if err != nil {
		t.Fatal(err)
	}
	if len(reaped) == 0 {
		t.Fatal("the reaper found no running turn")
	}
	// Whichever host supervised the run, the next pass of the new leader ends it.
	a.host.Close()
	if err := b.host.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	until(t, "the dead run to be failed", func() bool {
		got, _ := sh.trg.GetRun(ctx, run.ID)
		return got.Status == triggers.StatusFailed
	})
	got, _ := sh.trg.GetRun(ctx, run.ID)
	if got.Reason != triggers.ReasonInterrupted {
		t.Fatalf("reason = %q", got.Reason)
	}
	if info, _ := sh.chats.Turn(ctx, alice, run.ChatID); info.Running {
		t.Fatal("the chat is still held")
	}
}

// The model sees paths of the user's home ("/Contracts"), never the internal
// path of the workspace.
func TestARunNamesFoldersAsTheUserSeesThem(t *testing.T) {
	sh := newShared(t)
	ctx := context.Background()
	if err := sh.files.EnsureHome(ctx, alice); err != nil {
		t.Fatal(err)
	}
	act := sh.files.Actor(alice)
	folder, _ := sh.files.Abs(alice, "/Contracts")
	if _, err := sh.files.Store().Mkdir(ctx, act, folder); err != nil {
		t.Fatal(err)
	}
	if _, err := sh.files.Store().Put(ctx, act, folder+"/AGENTS.md", strings.NewReader("Be brief."), workspace.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	r := sh.rig(t, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }, studio.TriggerOptions{})
	tr := r.schedule(t, studio.TriggerInput{Kind: triggers.KindFile, Folder: "/Contracts", Quiet: time.Second, Name: "Contracts"})
	r.pass(t)
	if _, err := sh.files.Store().Put(ctx, act, folder+"/a.txt", strings.NewReader("hello"), workspace.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	r.pass(t)
	sh.clock.Advance(2 * time.Second)
	r.pass(t)
	run := r.waitEnded(t, tr.ID)
	sent := r.h.Sent(r.chatOf(t, run).ID)
	if len(sent) != 1 {
		t.Fatalf("sent = %q", sent)
	}
	if !strings.Contains(sent[0], "# Folder rules: /Contracts") || strings.Contains(sent[0], folder) {
		t.Fatalf("the first message holds an internal path (%s):\n%s", folder, sent[0])
	}
	if got := r.host.RelPath(alice, run.File); got != "/Contracts/a.txt" {
		t.Fatalf("RelPath = %q", got)
	}
}
