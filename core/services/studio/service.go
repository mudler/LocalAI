// Package studio runs the chats of the new conversation API: one loop for the
// web chat, for saved agents and for connectors. It holds no state of its own
// that another frontend would need; pending messages and the turn claim live in
// the store.
package studio

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/nibharness"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// AnonymousPrincipal is the owner of conversations when auth is off. The loop
// needs a non-empty owner, so the empty user becomes this one.
const AnonymousPrincipal = "anonymous"

// ErrNoModel means a conversation was created without a model.
var ErrNoModel = errors.New("studio: a conversation needs a model")

// ErrFilesWhileRunning means a message with files was sent while a turn runs.
// Waiting messages are text, so files go only with the message that starts a turn.
var ErrFilesWhileRunning = errors.New("studio: files can be attached when no turn is running; wait for it or stop it")

// ErrSubAgentReadOnly means a user tried to send to a sub-agent conversation.
// It runs with the tools its parent chose, so only the parent starts it.
var ErrSubAgentReadOnly = errors.New("studio: a sub-agent conversation takes no messages from the user")

// UserLookup returns the active user with this ID. It is nil when auth is off.
type UserLookup func(ctx context.Context, id string) (*auth.User, error)

// Deps is what the service needs.
type Deps struct {
	Store    store.Store
	Provider *provider.Provider
	Users    UserLookup
	// StateDir holds the harness's private files, one folder per chat.
	StateDir string
	// HostTools supplies the tools a chat gets besides the ones the service adds
	// itself (for example the tools of MCP servers). A sub-agent gets all of
	// them, or the subset its parent names. Nil: none.
	HostTools func(ctx context.Context, a workspace.Actor) ([]session.Tool, error)
	// MCP returns the MCP tool executor for a user's chat with a model, or nil
	// when the model has none. Its tools become the chat's tools. Nil: no MCP.
	MCP func(ctx context.Context, user, model string) mcpendpoint.ToolExecutor
	// ContextTokens returns the context window of a model in tokens, or zero
	// when it is not known (the harness then uses its default). Nil: zero.
	ContextTokens func(model string) int
	// Bus carries live events and control messages between the frontends that
	// serve a chat. Nil: a bus for this process only.
	Bus Bus
	// HeartbeatEvery is how often a running turn tells the store it is alive.
	// Zero means 15 seconds.
	HeartbeatEvery time.Duration
	// Files is the workspace of the chats. Nil or disabled: the chats have no
	// files, and everything else works.
	Files *files.Service
	// Persistent is true when the store keeps conversations across a restart.
	Persistent bool
	// Harness replaces the nib harness. It is for tests.
	Harness session.Harness
	Logger  *slog.Logger
}

// CreateInput describes a new conversation.
type CreateInput struct {
	Title string
	Model string
}

// SendResult says what Send did with a message.
type SendResult struct {
	// Started is true when the message began a turn. Otherwise a turn was
	// running, and the message is pending under PendingID.
	Started   bool
	PendingID string
}

// Service runs conversations.
type Service struct {
	d   Deps
	mgr *session.Manager
	bus Bus
	log *slog.Logger

	mu    sync.Mutex
	runs  map[string]*run                 // the turn this process runs, by conversation
	pumps map[string]<-chan session.Event // the event channel read for each conversation
	locks map[string]*keyLock

	// Sub-agents; see subagent.go.
	children map[string]map[string]*child       // running sub-agents, by parent conversation
	allowed  map[string][]session.Tool          // the tools of a sub-agent conversation
	waiters  map[string][]chan store.TurnStatus // callers waiting for a turn to end
	closed   chan struct{}
}

type run struct {
	id      string
	stopped bool
	failed  bool
	// end stops what the turn started besides the loop itself: the heartbeat and
	// the listener for control messages.
	end func()
}

const defaultHeartbeat = 15 * time.Second

type keyLock struct {
	mu sync.Mutex
	n  int
}

// New returns a Service.
func New(d Deps) (*Service, error) {
	if d.Store == nil || d.StateDir == "" {
		return nil, errors.New("studio: a store and a state folder are required")
	}
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	bus := d.Bus
	if bus == nil {
		bus = NewLocalBus()
	}
	s := &Service{d: d, bus: bus, log: log, runs: map[string]*run{}, pumps: map[string]<-chan session.Event{}, locks: map[string]*keyLock{},
		children: map[string]map[string]*child{}, allowed: map[string][]session.Tool{}, waiters: map[string][]chan store.TurnStatus{}, closed: make(chan struct{})}
	h := d.Harness
	if h == nil {
		if d.Provider == nil {
			return nil, errors.New("studio: a provider is required without a test harness")
		}
		h = nibharness.New(nibharness.Config{LLM: s.llmFor})
	}
	if d.Files == nil {
		d.Files = files.Disabled("")
		s.d.Files = d.Files
	}
	mgr, err := session.New(session.Options{Store: d.Store, Workspace: d.Files.Store(), Harness: h, StateDir: d.StateDir, Inputs: s.inputs, ChatTools: s.chatTools, Logger: log})
	if err != nil {
		return nil, err
	}
	s.mgr = mgr
	return s, nil
}

// Close stops every chat.
func (s *Service) Close(ctx context.Context) error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return s.mgr.Close(ctx)
}

// List returns the user's conversations, newest first. Sub-agent runs are
// included; they have Kind session.KindSubAgent and a ParentID.
func (s *Service) List(ctx context.Context, user string, limit int) ([]session.ChatRecord, error) {
	return s.d.Store.ListChats(ctx, principal(user), session.ChatCursor{}, limit)
}

func principal(user string) string {
	if user == "" {
		return AnonymousPrincipal
	}
	return user
}

func key(p, conv string) string { return p + "\x00" + conv }

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Service) lock(k string) func() {
	s.mu.Lock()
	l := s.locks[k]
	if l == nil {
		l = &keyLock{}
		s.locks[k] = l
	}
	l.n++
	s.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		s.mu.Lock()
		if l.n--; l.n == 0 {
			delete(s.locks, k)
		}
		s.mu.Unlock()
	}
}

// Create starts a new conversation for user.
func (s *Service) Create(ctx context.Context, user string, in CreateInput) (session.ChatRecord, error) {
	if strings.TrimSpace(in.Model) == "" {
		return session.ChatRecord{}, ErrNoModel
	}
	title := in.Title
	if title == "" {
		title = session.DefaultTitle
	}
	now := time.Now().UTC()
	rec := session.ChatRecord{ID: newID(), Principal: principal(user), Title: title, Created: now, Updated: now, Kind: session.KindChat, Model: in.Model}
	if err := s.d.Store.CreateChat(ctx, rec); err != nil {
		return session.ChatRecord{}, err
	}
	return rec, nil
}

// Get returns a conversation the user owns.
func (s *Service) Get(ctx context.Context, user, conv string) (session.ChatRecord, error) {
	return s.d.Store.GetChat(ctx, principal(user), conv)
}

// Messages returns transcript entries after seq.
func (s *Service) Messages(ctx context.Context, user, conv string, after int64, limit int) ([]session.Entry, error) {
	if _, err := s.Get(ctx, user, conv); err != nil {
		return nil, err
	}
	return s.d.Store.Entries(ctx, conv, after, limit)
}

// Turn returns the turn state of a conversation.
func (s *Service) Turn(ctx context.Context, user, conv string) (store.TurnInfo, error) {
	return s.d.Store.Turn(ctx, principal(user), conv)
}

// Principal returns the owner ID the service uses for a user: the user's ID,
// or the anonymous owner when authentication is off.
func (s *Service) Principal(user string) string { return principal(user) }

// Files returns the workspace of the chats. It is never nil.
func (s *Service) Files() *files.Service { return s.d.Files }

// Persistent reports whether conversations survive a restart of the server.
func (s *Service) Persistent() bool { return s.d.Persistent }

// Pending lists the messages waiting for the model.
func (s *Service) Pending(ctx context.Context, user, conv string) ([]store.Pending, error) {
	return s.d.Store.Pending(ctx, principal(user), conv)
}

// EditPending changes a waiting message.
func (s *Service) EditPending(ctx context.Context, user, conv, id, text string) error {
	return s.d.Store.EditPending(ctx, principal(user), conv, id, text)
}

// CancelPending removes a waiting message.
func (s *Service) CancelPending(ctx context.Context, user, conv, id string) error {
	return s.d.Store.CancelPending(ctx, principal(user), conv, id)
}

// Events subscribes to the live events of a conversation. Call cancel when done.
func (s *Service) Events(ctx context.Context, user, conv string) (<-chan session.Event, func(), error) {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return nil, nil, err
	}
	ch, cancel := s.bus.Subscribe(key(p, conv))
	return ch, cancel, nil
}

// Send gives the conversation a message. With no turn running it starts one.
// With a turn running the message waits and reaches the model, together with
// any others, at the loop's next model request.
func (s *Service) Send(ctx context.Context, user, conv, text string) (SendResult, error) {
	return s.SendWithFiles(ctx, user, conv, text, nil)
}

// SendWithFiles is Send with files attached. The files are saved in the chat's
// folder of the user's workspace and the model gets their paths. They go only
// with the message that starts a turn: with a turn running it is
// ErrFilesWhileRunning, and with files off it is files.ErrUnavailable.
func (s *Service) SendWithFiles(ctx context.Context, user, conv, text string, up []session.Upload) (SendResult, error) {
	rec, err := s.d.Store.GetChat(ctx, principal(user), conv)
	if err != nil {
		return SendResult{}, err
	}
	if rec.Kind == session.KindSubAgent {
		return SendResult{}, ErrSubAgentReadOnly
	}
	if len(up) > 0 && !s.d.Files.Available() {
		return SendResult{}, fmt.Errorf("%w: %s", files.ErrUnavailable, s.d.Files.Unavailable())
	}
	return s.send(ctx, user, conv, text, up)
}

// send is SendWithFiles without the check on the kind: the service itself
// starts sub-agent conversations through it.
func (s *Service) send(ctx context.Context, user, conv, text string, up []session.Upload) (SendResult, error) {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return SendResult{}, err
	}
	unlock := s.lock(key(p, conv))
	defer unlock()
	runID := newID()
	switch err := s.d.Store.ClaimTurn(ctx, p, conv, runID); {
	case err == nil:
		// Messages that wait go first: the new one must not overtake them.
		taken, terr := s.d.Store.TakePending(ctx, p, conv)
		if terr != nil {
			_ = s.d.Store.ReleaseTurn(ctx, p, conv, runID, store.TurnFailed)
			return SendResult{}, terr
		}
		texts := make([]string, 0, len(taken)+1)
		ids := make([]string, len(taken))
		for i, t := range taken {
			texts, ids[i] = append(texts, t.Text), t.ID
		}
		texts = append(texts, text)
		if err := s.startTurn(ctx, p, conv, runID, strings.Join(texts, "\n\n"), up); err != nil {
			_ = s.d.Store.RestorePending(ctx, p, conv, ids)
			return SendResult{}, err
		}
		if len(ids) > 0 {
			s.confirm(p, conv, ids)
			s.bus.Publish(key(p, conv), session.Event{Kind: session.KindPendingDelivered, IDs: ids})
		}
		return SendResult{Started: true}, nil
	case errors.Is(err, store.ErrTurnRunning):
		if len(up) > 0 {
			return SendResult{}, ErrFilesWhileRunning
		}
		pend, err := s.d.Store.AddPending(ctx, p, conv, text)
		if err != nil {
			return SendResult{}, err
		}
		// The turn may have ended on another frontend between the claim and the
		// queue. Nothing would start for this message then, so look again; the
		// claim decides who starts it.
		if info, terr := s.d.Store.Turn(ctx, p, conv); terr == nil && !info.Running && info.Last == store.TurnDone {
			if _, err := s.startFromPending(ctx, p, conv); err != nil {
				s.log.Warn("studio: could not start a turn for a message that arrived late", "chat", conv, "error", err)
			}
		}
		return SendResult{PendingID: pend.ID}, nil
	default:
		return SendResult{}, err
	}
}

// SendNow starts a turn from the waiting messages. It is what the user chooses
// after a Stop or a failure; nothing else starts them then.
func (s *Service) SendNow(ctx context.Context, user, conv string) error {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return err
	}
	unlock := s.lock(key(p, conv))
	defer unlock()
	_, err := s.startFromPending(ctx, p, conv)
	return err
}

// Stop ends the running turn. Waiting messages stay. The turn may run on another
// frontend, which then hears the request over the bus.
func (s *Service) Stop(ctx context.Context, user, conv string) error {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return err
	}
	k := key(p, conv)
	s.mu.Lock()
	local := s.runs[k] != nil
	s.mu.Unlock()
	if !local {
		s.bus.Control(k, ControlMsg{Stop: true})
		return nil
	}
	return s.stopLocal(ctx, p, conv)
}

// stopLocal stops a turn that runs in this process.
func (s *Service) stopLocal(ctx context.Context, p, conv string) error {
	k := key(p, conv)
	s.mu.Lock()
	r := s.runs[k]
	if r != nil {
		r.stopped = true
	}
	s.mu.Unlock()
	if r == nil {
		return nil
	}
	chat, err := s.chat(ctx, p, conv)
	if err != nil {
		return err
	}
	return chat.Stop(ctx)
}

// startFromPending starts a turn whose message is the waiting messages joined
// in order. It reports whether it started one. The caller holds the lock.
func (s *Service) startFromPending(ctx context.Context, p, conv string) (bool, error) {
	runID := newID()
	if err := s.d.Store.ClaimTurn(ctx, p, conv, runID); err != nil {
		if errors.Is(err, store.ErrTurnRunning) {
			return false, nil // a turn is running; it takes them at its next request
		}
		return false, err
	}
	taken, err := s.d.Store.TakePending(ctx, p, conv)
	if err != nil || len(taken) == 0 {
		_ = s.d.Store.ReleaseTurn(ctx, p, conv, runID, store.TurnDone)
		if err == nil {
			// A message may have been queued on another frontend while this claim
			// was held: it saw a running turn and did not start one. Look again
			// now that the claim is free.
			if left, perr := s.d.Store.Pending(ctx, p, conv); perr == nil && len(left) > 0 {
				return s.startFromPending(ctx, p, conv)
			}
		}
		return false, err
	}
	texts := make([]string, len(taken))
	ids := make([]string, len(taken))
	for i, t := range taken {
		texts[i], ids[i] = t.Text, t.ID
	}
	if err := s.startTurn(ctx, p, conv, runID, strings.Join(texts, "\n\n"), nil); err != nil {
		_ = s.d.Store.RestorePending(ctx, p, conv, ids)
		return false, err
	}
	s.confirm(p, conv, ids)
	s.bus.Publish(key(p, conv), session.Event{Kind: session.KindPendingDelivered, IDs: ids})
	return true, nil
}

// startTurn runs text as a turn the claim runID already covers.
func (s *Service) startTurn(ctx context.Context, p, conv, runID, text string, up []session.Upload) error {
	chat, err := s.chat(ctx, p, conv)
	if err != nil {
		_ = s.d.Store.ReleaseTurn(ctx, p, conv, runID, store.TurnFailed)
		return err
	}
	k := key(p, conv)
	// While the turn runs, tell the store it is alive and listen for a stop
	// requested on another frontend.
	hbCtx, stopBeat := context.WithCancel(context.Background())
	go s.heartbeat(hbCtx, p, conv, runID)
	stopListening := s.bus.OnControl(k, func(m ControlMsg) {
		if m.Stop {
			if err := s.stopLocal(context.Background(), p, conv); err != nil {
				s.log.Warn("studio: could not stop a turn on request", "chat", conv, "error", err)
			}
		}
	})
	end := func() { stopBeat(); stopListening() }
	s.mu.Lock()
	s.runs[k] = &run{id: runID, end: end}
	s.mu.Unlock()
	if err := chat.Send(context.WithoutCancel(ctx), session.Message{Text: text, Files: up}); err != nil {
		s.mu.Lock()
		delete(s.runs, k)
		s.mu.Unlock()
		end()
		_ = s.d.Store.ReleaseTurn(ctx, p, conv, runID, store.TurnFailed)
		return err
	}
	return nil
}

// heartbeat tells the store that a turn is alive until ctx ends.
func (s *Service) heartbeat(ctx context.Context, p, conv, runID string) {
	every := s.d.HeartbeatEvery
	if every <= 0 {
		every = defaultHeartbeat
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			err := s.d.Store.TouchTurn(ctx, p, conv, runID)
			if err == nil || ctx.Err() != nil {
				continue
			}
			if errors.Is(err, store.ErrNotClaimed) {
				// Another frontend reaped this turn as dead and may have started a
				// new one. Two turns on one chat must not run: stop this one.
				s.log.Warn("studio: a turn lost its claim and is stopping", "chat", conv)
				if serr := s.stopLocal(context.Background(), p, conv); serr != nil {
					s.log.Warn("studio: could not stop a turn that lost its claim", "chat", conv, "error", serr)
				}
				return
			}
			s.log.Warn("studio: could not record a turn's heartbeat", "chat", conv, "error", err)
		}
	}
}

// ReapStale ends the turns that have not beaten for olderThan and tells the
// watchers of each. A frontend that died leaves such turns behind. In a cluster
// one frontend runs this on a timer; a standalone process runs it once at
// startup with a zero duration, because every claim then belongs to a process
// that no longer exists.
func (s *Service) ReapStale(ctx context.Context, olderThan time.Duration) (int, error) {
	reaped, err := s.d.Store.ReapTurns(ctx, time.Now().Add(-olderThan))
	for _, r := range reaped {
		k := key(r.Principal, r.ChatID)
		s.bus.Publish(k, session.Event{Kind: session.KindError, Text: "The turn was interrupted."})
		s.bus.Publish(k, session.Event{Kind: session.KindTurnEnd})
	}
	return len(reaped), err
}

// chat opens the conversation in the loop and makes sure its events are read.
func (s *Service) chat(ctx context.Context, p, conv string) (*session.Chat, error) {
	if s.d.Files.Available() {
		// The loop needs the user's home before it opens a chat.
		if err := s.d.Files.EnsureHome(ctx, p); err != nil {
			return nil, err
		}
	}
	chat, err := s.mgr.Open(ctx, workspace.Actor{Principal: p, ChatID: conv}, conv)
	if err != nil {
		return nil, err
	}
	k := key(p, conv)
	// A chat that was closed after its last turn has a new event channel when
	// it reopens, so the pump is told apart by its channel, not by the chat:
	// the pump of the old channel may not have noticed that it ended yet.
	evs := chat.Events()
	s.mu.Lock()
	start := s.pumps[k] != evs
	s.pumps[k] = evs
	s.mu.Unlock()
	if start {
		go s.pump(chat, evs, p, conv)
	}
	return chat, nil
}

// pump reads the events of one chat for its lifetime, publishes them, and
// settles the turn when it ends.
func (s *Service) pump(chat *session.Chat, evs <-chan session.Event, p, conv string) {
	k := key(p, conv)
	defer func() {
		s.mu.Lock()
		if s.pumps[k] == evs { // a newer pump may have taken the place
			delete(s.pumps, k)
		}
		s.mu.Unlock()
	}()
	for ev := range evs {
		s.bus.Publish(k, ev)
		switch ev.Kind {
		case session.KindError:
			s.mu.Lock()
			if r := s.runs[k]; r != nil {
				r.failed = true
			}
			s.mu.Unlock()
		case session.KindTurnEnd:
			s.finishTurn(chat, p, conv)
		}
	}
}

// finishTurn records how the turn ended and, after a normal end only, starts
// the next turn from messages that arrived too late for the last request.
func (s *Service) finishTurn(chat *session.Chat, p, conv string) {
	ctx := context.Background()
	k := key(p, conv)
	unlock := s.lock(k)
	defer unlock()
	s.mu.Lock()
	r := s.runs[k]
	delete(s.runs, k)
	s.mu.Unlock()
	if r == nil {
		return
	}
	if r.end != nil {
		r.end()
	}
	// Another frontend may run the next turn, so nothing of this chat is kept in
	// memory past its turn, and is saved before the claim is given up: the next turn here loads the record again.
	if err := chat.Close(); err != nil {
		s.log.Warn("studio: could not close the chat after its turn", "chat", conv, "error", err)
	}
	status := store.TurnDone
	switch {
	case r.stopped:
		status = store.TurnStopped
	case r.failed:
		status = store.TurnFailed
	}
	if err := s.d.Store.ReleaseTurn(ctx, p, conv, r.id, status); err != nil {
		s.log.Warn("studio: could not release the turn", "chat", conv, "error", err)
		if !errors.Is(err, store.ErrNotClaimed) {
			return
		}
		// The claim was reaped: the turn is over all the same. Whoever holds the
		// chat now decides what runs next; this one only cleans up after itself.
		status = store.TurnInterrupted
	}
	s.notifyWaiters(k, status)
	if status != store.TurnDone {
		// A stopped or failed turn takes its running sub-agents with it, and the
		// waiting messages stay for the user to decide.
		s.stopChildren(p, conv)
		return
	}
	// The next turn is started from another goroutine: this one reads the events
	// that the new turn will produce.
	go func() {
		unlock := s.lock(k)
		defer unlock()
		if _, err := s.startFromPending(ctx, p, conv); err != nil {
			s.log.Warn("studio: could not start the next turn", "chat", conv, "error", err)
		}
	}()
}

// inputs is the source of pending messages for one chat's harness. It runs
// before each model request: it takes what is waiting, and if the request
// fails, puts it back.
func (s *Service) inputs(a workspace.Actor) func() ([]string, func(error)) {
	return func() ([]string, func(error)) {
		ctx := context.Background()
		taken, err := s.d.Store.TakePending(ctx, a.Principal, a.ChatID)
		if err != nil || len(taken) == 0 {
			return nil, nil
		}
		texts := make([]string, len(taken))
		ids := make([]string, len(taken))
		for i, t := range taken {
			texts[i], ids[i] = t.Text, t.ID
		}
		return texts, func(err error) {
			if err != nil {
				if rerr := s.d.Store.RestorePending(ctx, a.Principal, a.ChatID, ids); rerr != nil {
					s.log.Warn("studio: could not put pending messages back", "chat", a.ChatID, "error", rerr)
				}
				return
			}
			for _, t := range texts {
				if aerr := s.d.Store.AppendEntry(ctx, a.ChatID, session.Entry{Role: session.RoleUser, Text: t}); aerr != nil {
					s.log.Warn("studio: could not record a delivered message", "chat", a.ChatID, "error", aerr)
				}
			}
			s.confirm(a.Principal, a.ChatID, ids)
			s.bus.Publish(key(a.Principal, a.ChatID), session.Event{Kind: session.KindPendingDelivered, IDs: ids})
		}
	}
}

// confirm tells the store that the messages are in the transcript, so a turn
// that is reaped later does not give them back.
func (s *Service) confirm(p, conv string, ids []string) {
	if err := s.d.Store.ConfirmPending(context.Background(), p, conv, ids); err != nil {
		s.log.Warn("studio: could not confirm delivered messages", "chat", conv, "error", err)
	}
}

// llmFor gives the harness of a chat its model client, acting as the chat's owner.
func (s *Service) llmFor(a workspace.Actor) (nibharness.Model, error) {
	ctx := context.Background()
	rec, err := s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return nibharness.Model{}, err
	}
	var user *auth.User
	if s.d.Users != nil && a.Principal != AnonymousPrincipal {
		if user, err = s.d.Users(ctx, a.Principal); err != nil {
			return nibharness.Model{}, fmt.Errorf("studio: load the chat's user: %w", err)
		}
	}
	m := nibharness.Model{LLM: s.d.Provider.LLM(user, rec.Model), Name: rec.Model}
	if s.d.ContextTokens != nil {
		m.ContextTokens = s.d.ContextTokens(rec.Model)
	}
	return m, nil
}

// Delete removes a conversation and its transcript. A running turn is stopped
// first, and so are its running sub-agents. Another user's conversation is
// session.ErrNotFound.
func (s *Service) Delete(ctx context.Context, user, conv string) error {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return err
	}
	if err := s.Stop(ctx, p, conv); err != nil {
		return err
	}
	s.stopChildren(p, conv)
	return s.mgr.Delete(ctx, workspace.Actor{Principal: p, ChatID: conv}, conv)
}
