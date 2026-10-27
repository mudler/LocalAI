package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Message is what the user sends.
type Message struct {
	Text  string
	Files []Upload // stored in the chat folder before the turn starts
}

// Upload is one file that came with a message.
type Upload struct {
	Name string
	Body io.Reader
}

// Fixed texts of error events and of the checkpoint. They never carry the
// text of an internal error.
const (
	reasonAgent = "The agent stopped because of an error."
	// ReasonNotAllowed and ReasonQuota tell the user why the model refused.
	ReasonNotAllowed = "The model refused the request: you are not allowed to use it."
	ReasonQuota      = "The model refused the request: your quota is used up or the server is busy."
	reasonTranscript = "The transcript could not be saved."
	reasonSave       = "The chat could not be saved."
	reasonRefresh    = "The files you attached are not in the sandbox yet."
	reasonCheckpoint = "The changes of this turn could not be saved."
	maxPaths         = 200
	checkpointLimit  = 5 * time.Minute
)

// Chat is one conversation. A chat is in one of four states:
//
//	idle      no turn; Send starts one
//	running   a turn runs; Send injects, Stop interrupts
//	renaming  SetTitle moves the chat folder; Send waits
//	closed    Close was called; Open on the same ID reopens it
type Chat struct {
	m  *Manager
	id string
	// accMu guards the access of the chat: the actor, the user actor and the
	// view. Open replaces them when the actor's groups or the shared folders
	// have changed (see syncAccess).
	accMu       sync.RWMutex
	actor       workspace.Actor // with ChatID: the agent's actor
	user        workspace.Actor // without ChatID: uploads and renames
	view        workspace.View  // the view of the sandbox
	harnessView workspace.View  // the view that the harness prompt names
	dir         string
	turnCtx     context.Context // the context of the running turn; under mu

	mu       sync.Mutex
	rec      ChatRecord
	hs       HarnessSession
	closed   bool
	running  bool
	renaming chan struct{} // non-nil while SetTitle runs; closed at its end
	done     chan struct{} // closed when the running turn loop ends
	stopped  bool          // Stop was called in the running turn
	cancel   context.CancelFunc
	pending  []string // messages that arrived when the turn was ending
	swapping bool     // the harness is being swapped inside the turn; under mu
	// injectMu keeps a swap from closing a harness while send injects into it.
	injectMu sync.RWMutex
	turn     int
	text     strings.Builder // assistant text of the turn, not yet stored
	// The reasoning of the current step, not yet stored. It stops growing at
	// MaxReasoningBytes (reasonCut). reasonFirst is the time of its first
	// delta.
	reason      strings.Builder
	reasonCut   bool
	reasonFirst time.Time

	evMu sync.RWMutex // emit and Events read; reopen replaces the bus
	bus  *eventBus
	// endMu orders the end of a turn: turn_end is emitted before the next
	// turn can emit turn_start.
	endMu sync.Mutex

	// hostMu guards host, the host-side tools of the chat (see applyChatTools).
	hostMu sync.Mutex
	host   hostTools

	// cardMu guards cards, the tool cards that wait for their tool_end event.
	cardMu sync.Mutex
	cards  []pendingCard

	vmMu    sync.Mutex // the sandbox: create, resume, suspend, destroy
	vm      vmState
	lastUse atomic.Int64
	tmu     sync.Mutex
	timer   *time.Timer

	persistMu sync.Mutex // one record write at a time, so an old copy never wins
	// released is true while rec.HarnessState is dropped from memory (a
	// closed chat). The state is in the store; persist keeps it there.
	released bool
	// stateSeq counts the changes of rec.HarnessState; savedSeq is the count
	// that the store has.
	stateSeq, savedSeq int
}

type vmState struct {
	sb          sandbox.Sandbox
	busy        int // tool calls in flight
	unsupported bool
}

func newChat(m *Manager, a workspace.Actor, rec ChatRecord, view workspace.View) *Chat {
	a = copyActor(a)
	return &Chat{
		m: m, id: rec.ID, actor: a, user: userActor(a), view: view, rec: rec,
		dir: m.chatDir(rec.ID), turn: rec.Turns, bus: newEventBus(),
	}
}

// access returns the actor, the user actor and the view of the chat.
func (c *Chat) access() (actor, user workspace.Actor, view workspace.View) {
	c.accMu.RLock()
	defer c.accMu.RUnlock()
	return c.actor, c.user, c.view
}

func (c *Chat) principal() string {
	c.accMu.RLock()
	defer c.accMu.RUnlock()
	return c.actor.Principal
}

// Events returns the channel of live events. A consumer that falls behind
// loses text events: the oldest go first, past a ring of 512. It loses
// reasoning events the same way, past a ring of 2048 of their own. Under a backlog
// of more than 10,000 events it loses tool_start and tool_end events too.
// turn_start, checkpoint, turn_end and error are never dropped. Close closes
// the channel. The transcript is the durable record: see History.
func (c *Chat) Events() <-chan Event {
	c.evMu.RLock()
	defer c.evMu.RUnlock()
	return c.bus.ch
}

// emit queues an event. It never blocks. The caller must not hold c.mu.
func (c *Chat) emit(e Event) {
	if e.Turn == 0 {
		c.mu.Lock()
		e.Turn = c.turn
		c.mu.Unlock()
	}
	c.evMu.RLock()
	b := c.bus
	c.evMu.RUnlock()
	b.push(e)
}

func (c *Chat) appendEntry(ctx context.Context, e Entry) int64 {
	seq, err := c.m.o.Store.AppendEntry(ctx, c.id, e)
	if err != nil {
		c.m.log.Error("session: append entry", "chat", c.id, "error", err)
		c.emit(Event{Kind: KindError, Text: reasonTranscript})
		return 0
	}
	return seq
}

// persist stores the chat record. It returns the store's error: the caller
// decides what a failed save means. The in-memory record stays the newest, so
// a later persist retries the same data.
func (c *Chat) persist(ctx context.Context) error {
	c.persistMu.Lock()
	defer c.persistMu.Unlock()
	c.mu.Lock()
	rec := c.rec
	rec.Updated = time.Now().UTC()
	released := c.released
	seq := c.stateSeq
	c.mu.Unlock()
	if released {
		// A closed chat holds no harness state; do not overwrite the stored one.
		cur, err := c.m.o.Store.GetChat(ctx, c.principal(), c.id)
		if err != nil {
			return fmt.Errorf("session: save the chat: %w", err)
		}
		// Only the time is ours: another frontend may have changed the rest.
		cur.Updated = rec.Updated
		rec = cur
	}
	if err := c.m.o.Store.UpdateChat(ctx, rec); err != nil {
		c.m.log.Error("session: update chat", "chat", c.id, "error", err)
		return fmt.Errorf("session: save the chat: %w", err)
	}
	c.mu.Lock()
	c.rec.Updated = rec.Updated
	if !released {
		c.savedSeq = seq
	}
	c.mu.Unlock()
	return nil
}

func (c *Chat) snapshot() *sandbox.SnapshotRef {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rec.Snapshot == nil {
		return nil
	}
	s := *c.rec.Snapshot
	return &s
}

// setSnapshot stores the reference of a suspended sandbox. When it returns an
// error the reference is not saved: the caller must keep the sandbox live and
// try again, or the snapshot would be lost on a restart.
func (c *Chat) setSnapshot(s *sandbox.SnapshotRef) error {
	c.mu.Lock()
	old := c.rec.Snapshot
	c.rec.Snapshot = s
	c.mu.Unlock()
	if err := c.persist(context.Background()); err != nil {
		c.mu.Lock()
		c.rec.Snapshot = old
		c.mu.Unlock()
		return err
	}
	return nil
}

func (c *Chat) isRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running || c.renaming != nil
}

// startHarness starts the harness from the saved state. A state that the
// harness cannot read, or a missing state in a chat that has a transcript,
// starts a fresh session with the transcript in the system prompt.
func (c *Chat) startHarness(ctx context.Context) error {
	c.primeChatTools(ctx)
	c.mu.Lock()
	rec := c.rec
	c.mu.Unlock()
	prompt := c.m.o.SystemPrompt
	if prompt != "" {
		prompt += "\n\n"
	}
	c.accMu.Lock()
	c.harnessView = c.view
	view := c.view
	c.accMu.Unlock()
	prompt += LayoutPrompt(view)
	c.hostMu.Lock()
	turnPrompt := c.host.prompt
	c.hostMu.Unlock()
	// The six sandbox tools exist only when there is a sandbox to run them in;
	// without one they could only answer "cannot run commands".
	tools := c.hostWrappers()
	if c.m.o.Sandbox != nil {
		tools = append(NewTools(c.acquireCreate), tools...)
	}
	actor, _, _ := c.access()
	env := HarnessEnv{
		SystemPrompt: prompt,
		TurnPrompt:   turnPrompt,
		Tools:        c.guardTools(tools),
		Resume:       rec.HarnessState,
		Dir:          c.dir,
		Emit:         c.onHarnessEvent,
		Actor:        actor,
	}
	if c.m.o.Inputs != nil {
		env.Inputs = c.m.o.Inputs(actor)
	}
	var hs HarnessSession
	var err error
	if len(env.Resume) > 0 {
		if hs, err = c.m.o.Harness.Start(ctx, env); err != nil {
			c.m.log.Warn("session: the harness cannot read its saved state", "chat", c.id, "error", err)
			env.Resume = nil
		}
	}
	if hs == nil {
		if sum := c.summary(ctx); sum != "" {
			env.SystemPrompt += "\n\nThe conversation so far, from the transcript:\n" + sum
		}
		if hs, err = c.m.o.Harness.Start(ctx, env); err != nil {
			return fmt.Errorf("session: start the harness: %w", err)
		}
	}
	c.mu.Lock()
	c.hs = hs
	c.mu.Unlock()
	return nil
}

// summary renders the end of the transcript as plain text, at most 8 KiB.
// Checkpoints and reasoning are left out: the model never sees reasoning.
func (c *Chat) summary(ctx context.Context) string {
	es, err := c.History(ctx)
	if err != nil || len(es) == 0 {
		return ""
	}
	var lines []string
	size := 0
	for i := len(es) - 1; i >= 0; i-- {
		if es[i].Role == RoleCheckpoint || es[i].Role == RoleReasoning {
			continue
		}
		l := es[i].Role + ": " + CapText(es[i].Text, 1000)
		if size += len(l) + 1; size > 8<<10 {
			break
		}
		lines = append([]string{l}, lines...)
	}
	return strings.Join(lines, "\n")
}

// reopen revives a closed chat.
func (c *Chat) reopen(ctx context.Context) error {
	c.mu.Lock()
	if !c.closed {
		c.mu.Unlock()
		return nil
	}
	released := c.released
	c.mu.Unlock()
	if released {
		// Close dropped the harness state from memory: load it again.
		rec, err := c.m.o.Store.GetChat(ctx, c.principal(), c.id)
		if err != nil {
			return err
		}
		// The whole record: another frontend may have run turns since.
		c.mu.Lock()
		c.rec, c.released, c.turn = rec, false, rec.Turns
		c.mu.Unlock()
	}
	if err := c.startHarness(ctx); err != nil {
		return err
	}
	c.evMu.Lock()
	c.bus = newEventBus()
	c.evMu.Unlock()
	c.mu.Lock()
	c.closed = false
	c.mu.Unlock()
	return nil
}

// onHarnessEvent receives the events of the harness: text, tool calls and
// errors. The chat stores the transcript from them and adds the events of the
// turn itself.
func (c *Chat) onHarnessEvent(e Event) {
	ctx := context.Background()
	c.mu.Lock()
	turn := c.turn
	c.mu.Unlock()
	e.Turn = turn
	e.Change = nil // the chat alone emits checkpoints
	switch e.Kind {
	case KindReasoning:
		if e.Text == "" {
			return
		}
		e.Tool = nil
		c.addReasoning(ctx, turn, e.Text)
	case KindText:
		c.flushReasoning(ctx, turn)
		c.mu.Lock()
		c.text.WriteString(e.Text)
		c.mu.Unlock()
	case KindToolStart:
		e.Tool = capTool(e.Tool)
		c.flushReasoning(ctx, turn)
		c.flushText(ctx, turn)
	case KindToolEnd:
		e.Tool = capTool(e.Tool)
		if e.Tool != nil {
			if e.Tool.OK {
				e.Tool.Card = c.takeCard(e.Tool.Name, e.Tool.Output)
			}
			e.Tool.Seq = c.appendEntry(ctx, Entry{Turn: turn, Role: RoleTool, Card: e.Tool.Card, Text: e.Tool.Name + ": " + e.Tool.Summary + "\n" + e.Tool.Output})
		}
	case KindError:
		// An error event carries one of the fixed reasons, never the text
		// of an internal error.
		e.Text = fixedReason(e.Text)
		e.Tool = nil
		// Only the chat knows whether an error ended the turn; a harness
		// cannot claim it.
		e.Fatal = false
		c.flushReasoning(ctx, turn)
	case KindCompacted:
		// Only the token counts travel; the transcript keeps every message.
		e.Text, e.Tool = "", nil
	default:
		return // turn_start, checkpoint and turn_end belong to the chat
	}
	c.emit(e)
}

// Limits of a tool event, as documented on ToolEvent.
const (
	maxSummary = 200
	maxOutput  = 2048
)

// capTool returns a copy of t with the documented limits applied, so that no
// harness can put long text in an event.
func capTool(t *ToolEvent) *ToolEvent {
	if t == nil {
		return nil
	}
	cp := *t
	cp.Card = nil // only a host tool sets a card, through the chat
	cp.Seq = 0    // only the chat knows the entry of a call
	cp.Name = CapText(cp.Name, maxSummary)
	cp.Summary = CapText(cp.Summary, maxSummary)
	cp.Output = CapText(cp.Output, maxOutput)
	return &cp
}

// fixedReason returns text when it is one of the fixed reasons, and the
// generic reason otherwise.
func fixedReason(text string) string {
	switch text {
	case reasonAgent, reasonTranscript, reasonSave, reasonRefresh, reasonCheckpoint, ReasonNotAllowed, ReasonQuota:
		return text
	}
	return reasonAgent
}

// Marks the model client puts in the error of a refused request, so that the
// loop can tell the user why without showing the text of an internal error.
const (
	MarkNotAllowed = "[studio:not-allowed]"
	MarkQuota      = "[studio:quota]"
)

// reasonFor picks the fixed reason for an error that ended a turn.
func reasonFor(err error) string {
	switch msg := err.Error(); {
	case strings.Contains(msg, MarkNotAllowed):
		return ReasonNotAllowed
	case strings.Contains(msg, MarkQuota):
		return ReasonQuota
	}
	return reasonAgent
}

// addReasoning buffers a reasoning delta. Answer text that is still buffered
// is stored first, so the entries keep the order of the events.
func (c *Chat) addReasoning(ctx context.Context, turn int, delta string) {
	c.mu.Lock()
	pending := c.text.Len() > 0
	c.mu.Unlock()
	if pending {
		c.flushText(ctx, turn)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reason.Len() == 0 && !c.reasonCut {
		c.reasonFirst = time.Now()
	}
	if c.reasonCut {
		return
	}
	if room := MaxReasoningBytes - len(reasoningCut); c.reason.Len()+len(delta) > room {
		c.reason.WriteString(CapText(delta, room-c.reason.Len()))
		c.reasonCut = true
		return
	}
	c.reason.WriteString(delta)
}

const reasoningCut = "…"

// flushReasoning stores the reasoning of the step, when there is any. A store
// error is logged and dropped: reasoning never fails a turn.
func (c *Chat) flushReasoning(ctx context.Context, turn int) {
	c.mu.Lock()
	t, cut, first := c.reason.String(), c.reasonCut, c.reasonFirst
	c.reason.Reset()
	c.reasonCut = false
	c.mu.Unlock()
	if strings.TrimSpace(t) == "" {
		return
	}
	if cut {
		t += reasoningCut
	}
	dur := max(time.Since(first), time.Millisecond)
	e := Entry{Turn: turn, Role: RoleReasoning, Text: t, Dur: dur}
	if _, err := c.m.o.Store.AppendEntry(ctx, c.id, e); err != nil {
		c.m.log.Error("session: append reasoning entry", "chat", c.id, "error", err)
	}
}

func (c *Chat) flushText(ctx context.Context, turn int) {
	c.mu.Lock()
	t := c.text.String()
	c.text.Reset()
	c.mu.Unlock()
	if strings.TrimSpace(t) != "" {
		c.appendEntry(ctx, Entry{Turn: turn, Role: RoleAssistant, Text: t})
	}
}

// History returns the whole transcript.
func (c *Chat) History(ctx context.Context) ([]Entry, error) {
	var all []Entry
	var after int64
	for {
		page, err := c.m.o.Store.Entries(ctx, c.id, after, 500)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < 500 {
			return all, nil
		}
		after = page[len(page)-1].Seq
	}
}

// Send starts a turn, or injects the message into the running one. Uploads
// are stored in the chat folder before the turn starts; a message with files
// is refused with ErrTurnRunning while a turn runs.
func (c *Chat) Send(ctx context.Context, msg Message) error { return c.send(ctx, msg, false) }

// send is Send. logged is true when the message is in the transcript already.
func (c *Chat) send(ctx context.Context, msg Message, logged bool) error {
	text := strings.TrimSpace(msg.Text)
	if text == "" && len(msg.Files) == 0 {
		return fmt.Errorf("%w: empty message", ErrInvalid)
	}
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return ErrClosed
		}
		ch := c.renaming
		if ch == nil {
			break
		}
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if c.running {
		if c.stopped {
			// Stop was called and the turn has not ended: a message would be
			// queued and then dropped.
			c.mu.Unlock()
			return fmt.Errorf("%w: the turn is stopping", ErrTurnRunning)
		}
		hs, turn, swapping := c.hs, c.turn, c.swapping
		if hs == nil || swapping {
			// The harness is being swapped: queue the message for the next
			// turn, which runs on the new harness.
			c.mu.Unlock()
			if len(msg.Files) > 0 {
				return fmt.Errorf("%w: files can be sent when the turn has ended", ErrTurnRunning)
			}
			if !logged {
				c.appendEntry(ctx, Entry{Turn: turn, Role: RoleUser, Text: text})
			}
			return c.queuePending(ctx, msg, text)
		}
		// Taken under mu, so a swap that starts later waits for this inject.
		c.injectMu.RLock()
		c.mu.Unlock()
		if len(msg.Files) > 0 {
			c.injectMu.RUnlock()
			return fmt.Errorf("%w: files can be sent when the turn has ended", ErrTurnRunning)
		}
		if !logged {
			c.appendEntry(ctx, Entry{Turn: turn, Role: RoleUser, Text: text})
		}
		err := hs.Inject(text)
		c.injectMu.RUnlock()
		if !errors.Is(err, ErrNoTurn) {
			return err
		}
		// The turn is ending. Queue the message for the next one, or start
		// it here when the turn has ended meanwhile.
		c.mu.Lock()
		if c.running {
			if c.stopped {
				c.mu.Unlock()
				return fmt.Errorf("%w: the turn is stopping", ErrTurnRunning)
			}
			c.pending = append(c.pending, text)
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()
		return c.send(ctx, msg, true)
	}
	// Reserve the turn, then store the files without the lock.
	c.running, c.stopped = true, false
	c.done = make(chan struct{})
	done := c.done
	c.mu.Unlock()

	paths, err := c.saveUploads(ctx, msg.Files)
	if err != nil {
		c.mu.Lock()
		c.endLoopLocked(done)
		c.mu.Unlock()
		return err
	}
	full := composeMessage(text, paths)
	if !logged {
		c.appendEntry(ctx, Entry{Turn: c.turnNow() + 1, Role: RoleUser, Text: full})
	}
	c.m.wg.Add(1)
	go c.loop(full, len(paths) > 0, done)
	return nil
}

// queuePending queues text for the next turn of the running loop, or starts
// it when the turn has ended meanwhile. The message is in the transcript.
func (c *Chat) queuePending(ctx context.Context, msg Message, text string) error {
	c.mu.Lock()
	if c.running {
		if c.stopped {
			c.mu.Unlock()
			return fmt.Errorf("%w: the turn is stopping", ErrTurnRunning)
		}
		c.pending = append(c.pending, text)
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	return c.send(ctx, msg, true)
}

func (c *Chat) turnNow() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turn
}

// endLoopLocked ends the running state. The caller holds c.mu.
func (c *Chat) endLoopLocked(done chan struct{}) {
	c.running, c.cancel, c.pending = false, nil, nil
	close(done)
}

func composeMessage(text string, guestPaths []string) string {
	if len(guestPaths) == 0 {
		return text
	}
	var b strings.Builder
	if text != "" {
		b.WriteString(text + "\n\n")
	}
	b.WriteString("The user attached these files:\n")
	for _, p := range guestPaths {
		b.WriteString("- " + p + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// loop runs the turn, then the messages that arrived while it was ending.
// The chat leaves the running state before turn_end is emitted, so a consumer
// that sees turn_end can call SetTitle or Send at once. endMu keeps the next
// turn_start behind that turn_end.
func (c *Chat) loop(text string, refresh bool, done chan struct{}) {
	defer c.m.wg.Done()
	for {
		n := c.runTurn(text, refresh)
		c.mu.Lock()
		more := len(c.pending) > 0 && !c.closed && !c.stopped
		if more {
			text, c.pending, refresh = strings.Join(c.pending, "\n\n"), nil, false
		} else {
			c.endLoopLocked(done)
		}
		c.endMu.Lock()
		c.mu.Unlock()
		c.emit(Event{Kind: KindTurnEnd, Turn: n})
		c.endMu.Unlock()
		c.touch()
		if !more {
			return
		}
	}
}

// runTurn runs one turn up to and including its checkpoint event, and returns
// the turn number. The loop emits turn_end.
func (c *Chat) runTurn(text string, refresh bool) int {
	c.mu.Lock()
	c.turn++
	n := c.turn
	ctx, cancel := context.WithCancel(c.m.ctx)
	c.cancel, c.turnCtx = cancel, ctx
	hs := c.hs
	skip := c.stopped
	c.mu.Unlock()
	c.clearCards()
	defer func() {
		cancel()
		c.clearCards()
		c.mu.Lock()
		c.turnCtx = nil
		c.mu.Unlock()
	}()

	c.endMu.Lock()
	c.emit(Event{Kind: KindTurnStart, Turn: n})
	c.endMu.Unlock()
	note := ""
	if !skip {
		var hookRefresh bool
		note, hookRefresh = c.applyChatTools(ctx)
		c.mu.Lock()
		hs = c.hs // a changed set of tools restarted the harness
		c.mu.Unlock()
		// The files that the prompt block names changed. Make the sandbox see
		// them, as it sees an upload.
		refresh = refresh || hookRefresh
	}
	if refresh && !skip && ctx.Err() == nil {
		if err := c.refresh(ctx); err != nil {
			c.m.log.Warn("session: refresh before the turn", "chat", c.id, "error", err)
			c.emit(Event{Kind: KindError, Turn: n, Text: reasonRefresh})
		}
	}
	if !skip && hs != nil {
		toModel := text
		if note != "" {
			// The note is for the model only: the transcript keeps the user's words.
			toModel = "(System note: " + note + ")\n\n" + text
		}
		err := safeSend(ctx, hs, toModel, c.m.log)
		c.mu.Lock()
		stopped := c.stopped
		c.mu.Unlock()
		if err != nil && !(stopped && (errors.Is(err, context.Canceled) || ctx.Err() != nil)) {
			c.m.log.Error("session: the harness failed", "chat", c.id, "turn", n, "error", err)
			c.emit(Event{Kind: KindError, Turn: n, Text: reasonFor(err), Fatal: true})
		}
	}
	c.flushReasoning(context.Background(), n)
	c.flushText(context.Background(), n)

	ch := c.checkpoint(n, text)
	c.appendEntry(context.Background(), Entry{Turn: n, Role: RoleCheckpoint, Rev: ch.Rev, Text: strings.Join(ch.Paths, "\n")})
	// The turn count advances with every turn, so that a restart never
	// numbers a turn twice. The state stays the old one when Export fails.
	c.mu.Lock()
	c.rec.Turns = n
	c.mu.Unlock()
	if hs != nil {
		if state, err := hs.Export(); err != nil {
			c.m.log.Error("session: export the harness state", "chat", c.id, "error", err)
		} else {
			c.mu.Lock()
			c.rec.HarnessState = state
			c.stateSeq++
			c.mu.Unlock()
		}
	}
	if err := c.persist(context.Background()); err != nil {
		c.emit(Event{Kind: KindError, Turn: n, Text: reasonSave})
	}
	c.emit(Event{Kind: KindCheckpoint, Turn: n, Change: &ch})
	return n
}

// checkpoint commits the turn's changes, when the chat has a live sandbox.
func (c *Chat) checkpoint(n int, text string) ChangeEvent {
	c.vmMu.Lock()
	defer c.vmMu.Unlock()
	if c.vm.sb == nil {
		return ChangeEvent{}
	}
	title := fmt.Sprintf("Turn %d: %s", n, CapText(strings.SplitN(text, "\n", 2)[0], 60))
	ctx, cancel := context.WithTimeout(context.Background(), checkpointLimit)
	defer cancel()
	sweepTemp(ctx, c.vm.sb)
	cs, conflicts, err := c.vm.sb.Checkpoint(ctx, title)
	if err != nil {
		c.m.log.Error("session: checkpoint", "chat", c.id, "turn", n, "error", err)
		return ChangeEvent{Err: reasonCheckpoint}
	}
	seen := map[string]bool{}
	var paths []string
	for _, op := range cs.Ops {
		for _, p := range []string{op.NewPath, op.OldPath} {
			if p != "" && !seen[p] && len(paths) < maxPaths {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	return ChangeEvent{Rev: cs.Rev, Paths: paths, Conflicts: conflicts}
}

// Stop interrupts the running turn and waits until it has ended. The turn
// still checkpoints, so partial work is kept and can be undone. Messages that
// are queued for the next turn are dropped.
func (c *Chat) Stop(ctx context.Context) error {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil
	}
	c.stopped, c.pending = true, nil
	cancel, done, hs := c.cancel, c.done, c.hs
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if hs != nil {
		_ = hs.Interrupt()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the turn, closes the harness and the event channel. The chat
// stays stored; its sandbox follows the idle rule. Open with the same ID
// opens it again.
func (c *Chat) Close() error { return c.close(context.Background()) }

// close is Close. ctx bounds the wait for the running turn.
func (c *Chat) close(ctx context.Context) error {
	// Mark the chat closed first, so that no Send starts a turn while Close
	// waits for the running one.
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	sctx, cancel := context.WithTimeout(ctx, checkpointLimit+time.Minute)
	defer cancel()
	stopErr := c.Stop(sctx)
	c.mu.Lock()
	hs := c.hs
	c.hs = nil
	c.mu.Unlock()
	var err error
	if hs != nil {
		err = hs.Close()
	}
	c.evMu.RLock()
	b := c.bus
	c.evMu.RUnlock()
	b.close()
	c.releaseMemory(ctx)
	return errors.Join(stopErr, err)
}

// saveUploads stores the files in the chat folder and returns their guest paths.
func (c *Chat) saveUploads(ctx context.Context, files []Upload) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	if c.m.o.Workspace == nil {
		return nil, ErrNoWorkspace
	}
	folder, err := c.ensureFolder(ctx)
	if err != nil {
		return nil, err
	}
	actor, user, _ := c.access()
	home := HomePath(actor.Principal)
	var out []string
	for _, f := range files {
		if f.Body == nil {
			return nil, fmt.Errorf("%w: file %q has no body", ErrInvalid, f.Name)
		}
		name := path.Base(strings.ReplaceAll(f.Name, "\\", "/"))
		if name == "." || name == "/" || name == ".." {
			name = "file"
		}
		n, err := c.m.o.Workspace.Put(ctx, user, ChatsPath(actor.Principal)+"/"+folder+"/"+name, f.Body, workspace.PutOptions{})
		if err != nil {
			return nil, fmt.Errorf("session: store %q: %w", name, err)
		}
		out = append(out, GuestRoot+"/"+GuestHome+strings.TrimPrefix(n.Path, home))
	}
	return out, nil
}

// ensureFolder creates the chat folder on first use and returns its name.
func (c *Chat) ensureFolder(ctx context.Context) (string, error) {
	c.m.layoutMu.Lock()
	defer c.m.layoutMu.Unlock()
	c.mu.Lock()
	rec := c.rec
	c.mu.Unlock()
	if rec.Folder != "" {
		return rec.Folder, nil
	}
	actor, user, _ := c.access()
	chats := ChatsPath(actor.Principal)
	name, err := uniqueName(FolderName(rec.Created, rec.Title), func(n string) (bool, error) {
		_, err := c.m.o.Workspace.Stat(ctx, user, chats+"/"+n)
		if errors.Is(err, workspace.ErrNotFound) {
			return false, nil
		}
		return err == nil, err
	})
	if err != nil {
		return "", err
	}
	if _, err := c.m.o.Workspace.Mkdir(ctx, user, chats+"/"+name); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.rec.Folder = name
	c.mu.Unlock()
	// The folder name stays in memory when the save fails; the save at the end
	// of the turn stores it.
	_ = c.persist(ctx)
	return name, nil
}

// SetTitle renames the chat and, when the chat folder exists, the folder. It
// returns ErrTurnRunning while a turn runs. When the chat has a sandbox, the
// sandbox is refreshed so that it sees the new folder name: its processes end.
func (c *Chat) SetTitle(ctx context.Context, title string) error {
	title = CleanTitle(title)
	if title == "" {
		return fmt.Errorf("%w: empty title", ErrInvalid)
	}
	c.mu.Lock()
	switch {
	case c.closed:
		c.mu.Unlock()
		return ErrClosed
	case c.running || c.renaming != nil:
		c.mu.Unlock()
		return ErrTurnRunning
	}
	c.renaming = make(chan struct{})
	ch := c.renaming
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.renaming = nil
		c.mu.Unlock()
		close(ch)
	}()

	c.mu.Lock()
	old, created := c.rec.Folder, c.rec.Created
	c.mu.Unlock()
	if base := FolderName(created, title); old != "" && !strings.EqualFold(base, old) {
		if err := c.renameFolder(ctx, old, base); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.rec.Title = title
	c.mu.Unlock()
	return c.persist(ctx)
}

// renameFolder moves the chat folder old to a free name that starts with
// base. It commits what the sandbox wrote under the old name first, and
// refreshes the sandbox afterwards so that it sees the new name.
func (c *Chat) renameFolder(ctx context.Context, old, base string) error {
	if c.m.o.Workspace == nil {
		return nil
	}
	sb, release, err := c.acquire(ctx, false)
	if err != nil {
		return err
	}
	if sb != nil {
		defer release()
		sweepTemp(ctx, sb)
		if _, _, err := sb.Checkpoint(ctx, "Rename chat folder"); err != nil {
			return fmt.Errorf("session: save the sandbox before the rename: %w", err)
		}
	}
	actor, user, _ := c.access()
	chats := ChatsPath(actor.Principal)
	c.m.layoutMu.Lock()
	name, err := uniqueName(base, func(n string) (bool, error) {
		if n == old {
			return false, nil
		}
		_, err := c.m.o.Workspace.Stat(ctx, user, chats+"/"+n)
		if errors.Is(err, workspace.ErrNotFound) {
			return false, nil
		}
		return err == nil, err
	})
	if err == nil && name != old {
		_, err = c.m.o.Workspace.Move(ctx, user, chats+"/"+old, chats+"/"+name)
	}
	c.m.layoutMu.Unlock()
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.rec.Folder = name
	c.mu.Unlock()
	if sb != nil {
		if err := sb.Refresh(ctx); err != nil {
			c.m.log.Warn("session: refresh after the rename", "chat", c.id, "error", err)
		}
	}
	return nil
}

// sweepTemp removes the temporary files that an interrupted write left in the
// guest, so that no checkpoint commits one. Its errors are ignored: the
// checkpoint still runs.
func sweepTemp(ctx context.Context, sb sandbox.Sandbox) {
	sctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	p, err := sb.Exec(sctx, sandbox.Cmd{Args: []string{"find", ".", "-name", tempPrefix + "*", "-type", "f", "-delete"}, Dir: GuestRoot})
	if err == nil {
		_, _ = p.Wait()
	}
}

// stopTimer stops the idle timer, if one is armed.
func (c *Chat) stopTimer() {
	c.tmu.Lock()
	defer c.tmu.Unlock()
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}

// safeSend runs hs.Send and turns a panic into an error, so that a bug in a
// harness ends one turn and not the process.
func safeSend(ctx context.Context, hs HarnessSession, text string, log *slog.Logger) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("session: the harness panicked", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			err = errors.New("the harness panicked")
		}
	}()
	return hs.Send(ctx, text)
}

// guardTools makes a panic in a tool an error result, so that the harness
// goes on and the process lives.
func (c *Chat) guardTools(tools []Tool) []Tool {
	log := c.m.log
	out := make([]Tool, len(tools))
	for i, t := range tools {
		run := t.Run
		name := t.Name
		t.Run = func(ctx context.Context, args json.RawMessage) (res string, err error) {
			defer func() {
				if r := recover(); r != nil {
					log.Error("session: a tool panicked", "tool", name, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
					c.emit(Event{Kind: KindError, Text: reasonAgent})
					res, err = "", errors.New("the tool failed")
				}
			}()
			// Whatever context the harness passes, Stop must end the tool:
			// bind it to the context of the turn.
			c.mu.Lock()
			tctx := c.turnCtx
			c.mu.Unlock()
			if tctx == nil {
				tctx = c.m.ctx
			}
			rctx, cancel := context.WithCancel(ctx)
			defer cancel()
			defer context.AfterFunc(tctx, cancel)()
			return run(rctx, args)
		}
		out[i] = t
	}
	return out
}

// releaseMemory drops what a closed chat does not need: the harness state (it is in
// the store) and the event bus. A stuck bus would otherwise keep its 512-slot
// channel for as long as the manager lives. The state stays in memory when it
// cannot be saved.
func (c *Chat) releaseMemory(ctx context.Context) {
	c.mu.Lock()
	dirty := c.stateSeq != c.savedSeq
	c.mu.Unlock()
	if dirty && c.persist(ctx) != nil {
		return
	}
	c.mu.Lock()
	c.rec.HarnessState, c.released = nil, true
	c.mu.Unlock()
	c.evMu.Lock()
	c.bus = closedBus()
	c.evMu.Unlock()
}
