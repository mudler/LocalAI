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
	hub *hub
	log *slog.Logger

	mu    sync.Mutex
	runs  map[string]*run // the turn this process runs, by conversation
	pumps map[string]bool // conversations whose events this process reads
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
}

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
	s := &Service{d: d, hub: newHub(), log: log, runs: map[string]*run{}, pumps: map[string]bool{}, locks: map[string]*keyLock{},
		children: map[string]map[string]*child{}, allowed: map[string][]session.Tool{}, waiters: map[string][]chan store.TurnStatus{}, closed: make(chan struct{})}
	h := d.Harness
	if h == nil {
		if d.Provider == nil {
			return nil, errors.New("studio: a provider is required without a test harness")
		}
		h = nibharness.New(nibharness.Config{LLM: s.llmFor})
	}
	mgr, err := session.New(session.Options{Store: d.Store, Harness: h, StateDir: d.StateDir, Inputs: s.inputs, ChatTools: s.chatTools, Logger: log})
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
	ch, cancel := s.hub.subscribe(key(p, conv))
	return ch, cancel, nil
}

// Send gives the conversation a message. With no turn running it starts one.
// With a turn running the message waits and reaches the model, together with
// any others, at the loop's next model request.
func (s *Service) Send(ctx context.Context, user, conv, text string) (SendResult, error) {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return SendResult{}, err
	}
	unlock := s.lock(key(p, conv))
	defer unlock()
	runID := newID()
	switch err := s.d.Store.ClaimTurn(ctx, p, conv, runID); {
	case err == nil:
		if err := s.startTurn(ctx, p, conv, runID, text); err != nil {
			return SendResult{}, err
		}
		return SendResult{Started: true}, nil
	case errors.Is(err, store.ErrTurnRunning):
		pend, err := s.d.Store.AddPending(ctx, p, conv, text)
		if err != nil {
			return SendResult{}, err
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

// Stop ends the running turn. Waiting messages stay.
func (s *Service) Stop(ctx context.Context, user, conv string) error {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return err
	}
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
		return false, err
	}
	texts := make([]string, len(taken))
	ids := make([]string, len(taken))
	for i, t := range taken {
		texts[i], ids[i] = t.Text, t.ID
	}
	if err := s.startTurn(ctx, p, conv, runID, strings.Join(texts, "\n\n")); err != nil {
		_ = s.d.Store.RestorePending(ctx, p, conv, ids)
		return false, err
	}
	s.hub.publish(key(p, conv), session.Event{Kind: session.KindPendingDelivered, IDs: ids})
	return true, nil
}

// startTurn runs text as a turn the claim runID already covers.
func (s *Service) startTurn(ctx context.Context, p, conv, runID, text string) error {
	chat, err := s.chat(ctx, p, conv)
	if err != nil {
		_ = s.d.Store.ReleaseTurn(ctx, p, conv, runID, store.TurnFailed)
		return err
	}
	k := key(p, conv)
	s.mu.Lock()
	s.runs[k] = &run{id: runID}
	s.mu.Unlock()
	if err := chat.Send(context.WithoutCancel(ctx), session.Message{Text: text}); err != nil {
		s.mu.Lock()
		delete(s.runs, k)
		s.mu.Unlock()
		_ = s.d.Store.ReleaseTurn(ctx, p, conv, runID, store.TurnFailed)
		return err
	}
	return nil
}

// chat opens the conversation in the loop and makes sure its events are read.
func (s *Service) chat(ctx context.Context, p, conv string) (*session.Chat, error) {
	chat, err := s.mgr.Open(ctx, workspace.Actor{Principal: p, ChatID: conv}, conv)
	if err != nil {
		return nil, err
	}
	k := key(p, conv)
	s.mu.Lock()
	start := !s.pumps[k]
	s.pumps[k] = true
	s.mu.Unlock()
	if start {
		go s.pump(chat, p, conv)
	}
	return chat, nil
}

// pump reads the events of one chat for its lifetime, publishes them, and
// settles the turn when it ends.
func (s *Service) pump(chat *session.Chat, p, conv string) {
	k := key(p, conv)
	defer func() {
		s.mu.Lock()
		delete(s.pumps, k)
		s.mu.Unlock()
	}()
	for ev := range chat.Events() {
		s.hub.publish(k, ev)
		switch ev.Kind {
		case session.KindError:
			s.mu.Lock()
			if r := s.runs[k]; r != nil {
				r.failed = true
			}
			s.mu.Unlock()
		case session.KindTurnEnd:
			s.finishTurn(p, conv)
		}
	}
}

// finishTurn records how the turn ended and, after a normal end only, starts
// the next turn from messages that arrived too late for the last request.
func (s *Service) finishTurn(p, conv string) {
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
	status := store.TurnDone
	switch {
	case r.stopped:
		status = store.TurnStopped
	case r.failed:
		status = store.TurnFailed
	}
	if err := s.d.Store.ReleaseTurn(ctx, p, conv, r.id, status); err != nil {
		s.log.Warn("studio: could not release the turn", "chat", conv, "error", err)
		return
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
			s.hub.publish(key(a.Principal, a.ChatID), session.Event{Kind: session.KindPendingDelivered, IDs: ids})
		}
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
