package studio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Errors a UserLookup returns for a user the trigger host must tell apart.
var (
	// ErrNoUser means the user does not exist any more.
	ErrNoUser = errors.New("studio: the user does not exist")
	// ErrUserDisabled means the account exists but is not active.
	ErrUserDisabled = errors.New("studio: the user is not active")
	// ErrChatsOff means the account is active but an administrator switched the
	// chats feature off for it. Whatever the account started by itself (a
	// trigger, a bot) stops with it.
	ErrChatsOff = errors.New("studio: the chats feature is off for this account")
)

// Defaults of the trigger host.
const (
	DefaultTriggerTick = 5 * time.Second
	DefaultRunLimit    = 15 * time.Minute
	maxTriggersPerUser = 50
	stopWait           = 20 * time.Second

	reasonUserStop = "stopped by the user"
)

// TriggerOptions configures the trigger host.
type TriggerOptions struct {
	// Store holds triggers and runs. Required.
	Store triggers.Store
	// Clock gives the time; nil means time.Now. A test sets a fake one.
	Clock func() time.Time
	// Tick is the time between two passes; zero means DefaultTriggerTick.
	Tick time.Duration
	// RunLimit is how long a run may take; zero means DefaultRunLimit.
	RunLimit time.Duration
	// Lead says whether this frontend runs the triggers now. With a database,
	// one frontend leads at a time (the host gives it an advisory lock). Nil:
	// always.
	Lead func(ctx context.Context) bool
	// Logger; nil means slog.Default.
	Logger *slog.Logger
}

// Triggers runs the triggers of the chats. A run is a chat of the trigger's
// owner, made through the same service as any chat: the model route, its
// feature gate, allowlist and quota apply to it.
//
// State mode: shared. Triggers, runs and the change-feed cursor are in the
// store; the Runner (the in-memory part) runs on one frontend at a time, the
// leader, and a new leader rebuilds it from the store. A due time or a file
// version is one unique claim, so it runs once.
type Triggers struct {
	svc *Service
	o   TriggerOptions
	log *slog.Logger

	mu     sync.Mutex // guards runner, sup and shut
	shut   bool       // Close has begun: no supervisor may be added to wg
	runner *triggers.Runner
	sup    map[string]struct{} // chats whose run this process supervises

	wmu sync.Mutex // serialises writes of triggers

	wake chan struct{}
	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

// ErrTriggersClosed means the host has stopped.
var ErrTriggersClosed = errors.New("studio: the trigger host has stopped")

// NewTriggers returns the trigger host of a service. It does nothing until Run
// or Pass is called.
func NewTriggers(svc *Service, o TriggerOptions) (*Triggers, error) {
	if svc == nil || o.Store == nil {
		return nil, errors.New("studio: the trigger host needs a service and a store")
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.Tick <= 0 {
		o.Tick = DefaultTriggerTick
	}
	if o.RunLimit <= 0 {
		o.RunLimit = DefaultRunLimit
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Triggers{svc: svc, o: o, log: o.Logger, sup: map[string]struct{}{}, wake: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

// Notify asks Run for a pass at once. The file hook calls it after a write.
func (h *Triggers) Notify() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// Run makes a pass at each tick and at each Notify while this frontend leads,
// until ctx ends. It drops the Runner when leadership is lost.
func (h *Triggers) Run(ctx context.Context) {
	tick := time.NewTicker(h.o.Tick)
	defer tick.Stop()
	defer h.dropRunner()
	for {
		if ctx.Err() != nil {
			return
		}
		if h.o.Lead == nil || h.o.Lead(ctx) {
			if err := h.Pass(ctx); err != nil && ctx.Err() == nil && !errors.Is(err, ErrTriggersClosed) {
				h.log.Warn("studio: trigger pass failed", "error", err)
			}
		} else {
			h.dropRunner()
		}
		select {
		case <-ctx.Done():
			return
		case <-h.done:
			return
		case <-tick.C:
		case <-h.wake:
		}
	}
}

// Close stops the host: no pass starts a run, and the supervisors of the runs
// that go on end without finishing them. A run that was going stays running;
// the next leader marks it interrupted when its chat is not running.
func (h *Triggers) Close() {
	// Set shut under mu: a supervisor is added to wg only with mu held and shut
	// false, so no Add can run once Wait starts.
	h.mu.Lock()
	h.shut = true
	h.mu.Unlock()
	h.once.Do(func() { close(h.done) })
	h.dropRunner()
	h.wg.Wait()
}

func (h *Triggers) closed() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

func (h *Triggers) dropRunner() {
	h.mu.Lock()
	h.runner = nil
	h.mu.Unlock()
}

func (h *Triggers) currentRunner() *triggers.Runner {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runner
}

// ensureRunner returns the Runner of this leadership term, building it once.
func (h *Triggers) ensureRunner() (*triggers.Runner, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed() {
		return nil, ErrTriggersClosed
	}
	if h.runner != nil {
		return h.runner, nil
	}
	opts := triggers.Options{
		Store: h.o.Store, Admin: workspace.Actor{Principal: files.AdminPrincipal},
		Start: h.start, Stop: h.stopRun, Live: h.live, Clock: h.o.Clock, Tick: h.o.Tick, Interrupted: h.tellInterrupted,
		Logf: func(format string, args ...any) { h.log.Warn("studio: " + fmt.Sprintf(format, args...)) },
	}
	if h.svc.d.Files.Available() {
		opts.Workspace = h.svc.d.Files.Store()
	}
	r, err := triggers.NewRunner(opts)
	if err != nil {
		return nil, err
	}
	h.runner = r
	return r, nil
}

// Pass makes one pass as the leader: the Runner claims due runs and starts the
// queued ones, then the host ends the runs whose chat is over. A test calls it
// instead of Run.
func (h *Triggers) Pass(ctx context.Context) error {
	r, err := h.ensureRunner()
	if err != nil {
		return err
	}
	err = r.Sync(ctx)
	h.reconcile(ctx)
	return err
}

// live is Options.Live: whether the turn of a run's chat still goes on,
// anywhere in the cluster.
func (h *Triggers) live(chatID string) bool {
	ctx := context.Background()
	run, err := h.o.Store.RunByChat(ctx, chatID)
	if err != nil {
		return false
	}
	t, err := h.o.Store.GetTrigger(ctx, run.TriggerID)
	if err != nil {
		return false
	}
	info, err := h.svc.Turn(ctx, t.Owner, chatID)
	return err == nil && info.Running
}

// stopRun is Options.Stop: it stops the chat of a run. It works across the
// cluster, as a Stop of the chat does.
func (h *Triggers) stopRun(chatID string) {
	ctx, cancel := context.WithTimeout(context.Background(), stopWait)
	defer cancel()
	run, err := h.o.Store.RunByChat(ctx, chatID)
	if err != nil {
		return
	}
	t, err := h.o.Store.GetTrigger(ctx, run.TriggerID)
	if err != nil {
		return
	}
	if err := h.svc.Stop(ctx, t.Owner, chatID); err != nil {
		h.log.Warn("studio: could not stop the chat of a run", "chat", chatID, "error", err)
	}
}

// start is Options.Start. An error that is not a skip is a failed run; both are
// told to the owner here, because the Runner records them without the host.
func (h *Triggers) start(ctx context.Context, t triggers.Trigger, run triggers.Run) (string, error) {
	id, err := h.startRun(ctx, t, run)
	var skip *triggers.SkipError
	if err != nil && ctx.Err() != nil && !errors.As(err, &skip) {
		return "", ctx.Err() // the host stops: the Runner puts the run back
	}
	if err != nil {
		h.tell(context.WithoutCancel(ctx), t, "", notify.KindRunFailed, notify.StatusFailed, t.Name+" did not run", err.Error())
	}
	return id, err
}

func (h *Triggers) startRun(ctx context.Context, t triggers.Trigger, run triggers.Run) (string, error) {
	owner := t.Owner
	if err := h.checkOwner(ctx, t); err != nil {
		return "", err
	}
	if t.AgentID != "" {
		if _, err := h.svc.Agent(ctx, owner, t.AgentID); err != nil {
			if errors.Is(err, agents.ErrNotFound) {
				h.switchOff(t, "its agent was deleted")
				return "", errors.New("the agent was deleted")
			}
			return "", errors.New(triggers.CutReason("could not read the agent: " + err.Error()))
		}
	}
	if t.Kind == triggers.KindFile {
		if err := h.checkFolder(ctx, t); err != nil {
			return "", err
		}
	}
	model, srcSkills, srcAddons, srcMCP, err := h.runSettings(ctx, t)
	if err != nil {
		return "", err
	}
	text, err := h.runMessage(ctx, t, run)
	if err != nil {
		return "", err
	}
	rec, err := h.svc.CreateAs(ctx, owner, CreateInput{Title: h.runTitle(t), Model: model, AgentID: t.AgentID},
		Origin{Kind: session.OriginTrigger, ID: t.ID})
	if err != nil {
		return "", err
	}
	started := false
	defer func() {
		if started {
			return
		}
		bg := context.WithoutCancel(ctx)
		if err := h.svc.Delete(bg, owner, rec.ID); err != nil {
			h.log.Warn("studio: could not delete the chat of a failed start", "chat", rec.ID, "error", err)
		}
	}()
	if t.AgentID == "" {
		if err := h.svc.d.Store.SetSkillsOff(ctx, owner, rec.ID, srcSkills); err != nil {
			return "", err
		}
		if err := h.svc.d.Store.SetAddonsOff(ctx, owner, rec.ID, srcAddons); err != nil {
			return "", err
		}
		if err := h.svc.d.Store.SetMcpOff(ctx, owner, rec.ID, srcMCP); err != nil {
			return "", err
		}
	}
	events, cancel, err := h.svc.Events(ctx, owner, rec.ID) // before Send: the end must not be missed
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	if h.shut {
		h.mu.Unlock()
		cancel()
		return "", ErrTriggersClosed
	}
	h.sup[rec.ID] = struct{}{}
	h.wg.Add(1) // the supervisor, which calls Done when it ends
	h.mu.Unlock()
	if _, err := h.svc.SendBackground(ctx, owner, rec.ID, text, nil); err != nil {
		cancel()
		h.mu.Lock()
		delete(h.sup, rec.ID)
		h.mu.Unlock()
		h.wg.Done()
		return "", err
	}
	started = true
	go h.supervise(run, t, rec.ID, events, cancel)
	return rec.ID, nil
}

// checkOwner skips the run when the owner cannot run it. A disabled account
// can be enabled again, so the trigger stays on; a user who no longer exists
// switches it off.
func (h *Triggers) checkOwner(ctx context.Context, t triggers.Trigger) error {
	if h.svc.d.Users == nil {
		return nil
	}
	u, err := h.svc.d.Users(ctx, t.Owner)
	switch {
	case err == nil:
		if h.svc.d.Allowed != nil && !h.svc.d.Allowed(u, auth.FeatureTriggers) {
			return &triggers.SkipError{Reason: "the owner may no longer use triggers"}
		}
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, ErrUserDisabled):
		return &triggers.SkipError{Reason: "the owner's account is disabled"}
	case errors.Is(err, ErrChatsOff):
		return &triggers.SkipError{Reason: "chats are switched off for the owner's account"}
	case errors.Is(err, ErrNoUser):
		h.switchOff(t, "its owner no longer exists")
		return &triggers.SkipError{Reason: "the owner no longer exists"}
	}
	return errors.New(triggers.CutReason("could not read the owner: " + err.Error()))
}

// checkFolder checks that the owner can still read the folder of a file
// trigger. A folder that is gone or not readable skips the run and switches the
// trigger off; another error fails the run and leaves the trigger on.
func (h *Triggers) checkFolder(ctx context.Context, t triggers.Trigger) error {
	if !h.svc.d.Files.Available() {
		return errors.New("files are not available on this server")
	}
	n, err := h.svc.d.Files.Store().Stat(ctx, h.svc.d.Files.Actor(t.Owner), t.Folder)
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case err == nil && n.Kind == workspace.KindDir:
		return nil
	case err == nil || errors.Is(err, workspace.ErrNotFound):
		h.switchOff(t, "its folder is not readable")
		return &triggers.SkipError{Reason: triggers.ReasonNotReadable}
	}
	return errors.New(triggers.CutReason("could not check the folder: " + err.Error()))
}

// runSettings returns the model of a run and, for a trigger with no agent, the
// switches (skills, add-ons, MCP) of the chat that proposed it, as they are now.
func (h *Triggers) runSettings(ctx context.Context, t triggers.Trigger) (model string, skillsOff, addonsOff, mcpOff []string, err error) {
	model = t.Model
	if t.AgentID == "" && t.SourceChatID != "" {
		if src, gerr := h.svc.d.Store.GetChat(ctx, t.Owner, t.SourceChatID); gerr == nil {
			skillsOff, addonsOff, mcpOff = src.SkillsOff, src.AddonsOff, src.McpOff
			if model == "" {
				model = src.Model
			}
		}
	}
	if model == "" && t.AgentID != "" {
		// The agent may name the model.
		if ag, aerr := h.svc.Agent(ctx, t.Owner, t.AgentID); aerr == nil {
			model = ag.Model
		}
	}
	if strings.TrimSpace(model) == "" {
		return "", nil, nil, nil, errors.New("no model: choose one for the trigger")
	}
	return model, skillsOff, addonsOff, mcpOff, nil
}

// runTitle is "<trigger name> · <date time> <zone>".
func (h *Triggers) runTitle(t triggers.Trigger) string {
	now := h.o.Clock()
	loc := now.Location()
	if t.TZ != "" {
		if l, err := t.Location(); err == nil {
			loc = l
		}
	}
	now = now.In(loc)
	title := t.Name + " · " + now.Format("2006-01-02 15:04")
	if t.TZ == "" || now.Format("MST") != "UTC" {
		title += " " + now.Format("MST")
	}
	return title
}

// runMessage is the first message of a run: the prompt, the rules of the folder
// (untrusted text, as the package says), and for a file run the file.
func (h *Triggers) runMessage(ctx context.Context, t triggers.Trigger, run triggers.Run) (string, error) {
	parts := []string{strings.TrimSpace(t.Prompt)}
	if h.svc.d.Files.Available() {
		folder := session.HomePath(t.Owner)
		if t.Kind == triggers.KindFile {
			folder = t.Folder
		}
		rules, err := triggers.RulesAs(ctx, h.svc.d.Files.Store(), h.svc.d.Files.Actor(t.Owner), folder,
			func(dir string) string { return h.RelPath(t.Owner, dir) })
		if err != nil {
			h.log.Warn("studio: could not read the folder rules for a run", "error", err)
		} else if rules != "" {
			parts = append(parts, rules)
		}
	}
	if run.File != "" {
		p := run.File
		if rel := h.svc.d.Files.Rel(t.Owner, run.File); rel != "" {
			p = rel
		}
		parts = append(parts, "A new file arrived: "+p)
	}
	return strings.Join(parts, "\n\n"), nil
}

// switchOff turns a trigger off and tells the owner. It reads the trigger again
// and changes only Enabled, so an edit stays and a deleted trigger stays gone.
func (h *Triggers) switchOff(t triggers.Trigger, why string) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	ctx := context.Background()
	cur, err := h.o.Store.GetTrigger(ctx, t.ID)
	if err != nil || !cur.Enabled {
		return
	}
	cur.Enabled = false
	if err := h.o.Store.PutTrigger(ctx, cur); err != nil {
		h.log.Warn("studio: could not switch off a trigger", "trigger", t.ID, "error", err)
		return
	}
	h.tell(ctx, t, "", notify.KindNeedsAttention, notify.StatusFailed, t.Name+" was switched off", "It was switched off because "+why+".")
}

// supervise waits for the end of the run's turn and reports it. It also
// enforces the time limit. It leaves a run alone when the host closes: the next
// leader finishes it.
func (h *Triggers) supervise(run triggers.Run, t triggers.Trigger, chatID string, events <-chan session.Event, cancel func()) {
	defer h.wg.Done()
	defer cancel()
	defer func() {
		h.mu.Lock()
		delete(h.sup, chatID)
		h.mu.Unlock()
	}()
	end := h.svc.awaitTurn(t.Owner, chatID, events, h.o.RunLimit, h.done)
	switch end.How {
	case TurnAbandoned:
		return
	case TurnTimedOut:
		h.stopRun(chatID)
		h.finish(run, t, chatID, triggers.StatusFailed, "took longer than "+limitText(h.o.RunLimit))
	default:
		status, reason := outcome(end.Info.Last, end.ErrText)
		h.finish(run, t, chatID, status, reason)
	}
}

// outcome maps how a turn ended to the state of its run.
func outcome(last store.TurnStatus, errText string) (triggers.Status, string) {
	switch last {
	case store.TurnStopped:
		return triggers.StatusDone, reasonUserStop
	case store.TurnFailed:
		if errText == "" {
			errText = "the turn failed"
		}
		return triggers.StatusFailed, errText
	case store.TurnInterrupted:
		return triggers.StatusFailed, triggers.ReasonInterrupted
	}
	return triggers.StatusDone, ""
}

// finishTimeout bounds the store calls that record the end of a run.
const finishTimeout = 30 * time.Second

// finish records the end of a run and tells the owner. The Runner of this term
// does it when it exists; otherwise the store is written directly, which is what
// Runner.Finish does.
func (h *Triggers) finish(run triggers.Run, t triggers.Trigger, chatID string, status triggers.Status, reason string) {
	// The store may hang; do not hold the runner for good.
	ctx, cancel := context.WithTimeout(context.Background(), finishTimeout)
	defer cancel()
	cur, err := h.o.Store.GetRun(ctx, run.ID)
	if err != nil || cur.Status.Final() {
		return // deleted with its trigger, or ended already
	}
	if r := h.currentRunner(); r != nil {
		err = r.Finish(ctx, run.ID, status, reason)
	} else {
		cur.Status, cur.Ended = status, h.o.Clock().UTC()
		if reason != "" {
			cur.Reason = triggers.CutReason(reason)
		}
		err = h.o.Store.SetRunStatus(ctx, cur)
	}
	if err != nil {
		h.log.Warn("studio: could not record the end of a run", "run", run.ID, "error", err)
		return
	}
	if status == triggers.StatusFailed {
		h.tell(ctx, t, chatID, notify.KindRunFailed, notify.StatusFailed, t.Name+" failed", reason)
		return
	}
	body := reason
	if body == "" {
		body = h.svc.finalText(ctx, chatID)
	}
	h.tell(ctx, t, chatID, notify.KindRunEnded, notify.StatusOK, t.Name+" finished", body)
}

// tellInterrupted tells the owner of a run that a restart cut short. The Runner
// calls it once for each such run, when it ends the run.
func (h *Triggers) tellInterrupted(run triggers.Run) {
	ctx := context.Background()
	t, err := h.o.Store.GetTrigger(ctx, run.TriggerID)
	if err != nil {
		return // the trigger is gone, and so is its owner's interest
	}
	h.tell(ctx, t, run.ChatID, notify.KindRunFailed, notify.StatusFailed, t.Name+" failed", run.Reason)
}

// reconcile finishes the runs that no supervisor of this process watches and
// whose chat is over or past its time: a run that a former leader started, or
// whose chat a reaper freed after its frontend died.
func (h *Triggers) reconcile(ctx context.Context) {
	runs, err := h.o.Store.RunningRuns(ctx)
	if err != nil {
		h.log.Warn("studio: could not list the running trigger runs", "error", err)
		return
	}
	for _, run := range runs {
		if run.ChatID == "" || ctx.Err() != nil {
			continue
		}
		h.mu.Lock()
		_, mine := h.sup[run.ChatID]
		h.mu.Unlock()
		if mine {
			continue
		}
		t, err := h.o.Store.GetTrigger(ctx, run.TriggerID)
		if err != nil {
			continue
		}
		info, err := h.svc.Turn(ctx, t.Owner, run.ChatID)
		switch {
		case err != nil && errors.Is(err, session.ErrNotFound):
			h.finish(run, t, run.ChatID, triggers.StatusFailed, "the chat was deleted")
		case err != nil:
			continue
		case !info.Running:
			status, reason := outcome(info.Last, "")
			h.finish(run, t, run.ChatID, status, reason)
		case !run.Started.IsZero() && h.o.Clock().Sub(run.Started) > h.o.RunLimit:
			h.stopRun(run.ChatID)
			h.finish(run, t, run.ChatID, triggers.StatusFailed, "took longer than "+limitText(h.o.RunLimit))
		}
	}
}

// tell adds a notification for the owner of a trigger. A failure is logged.
func (h *Triggers) tell(ctx context.Context, t triggers.Trigger, chatID, kind, status, title, body string) {
	if h.svc.d.Notify == nil {
		return
	}
	link := ""
	if chatID != "" {
		link = notify.ChatLink(chatID)
	}
	if _, err := h.svc.d.Notify.Add(ctx, notify.Notification{Principal: t.Owner, Kind: kind, Status: status, Title: title, Body: body, Link: link}); err != nil {
		h.log.Warn("studio: could not add a notification", "error", err)
	}
}

// limitText writes a duration the short way: 15m, not 15m0s.
func limitText(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
