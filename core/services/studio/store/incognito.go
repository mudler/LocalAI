package store

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/privacy"
	"github.com/mudler/LocalAI/core/services/studio/chatsearch"
	"github.com/mudler/LocalAI/core/services/studio/connsets"
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// Incognito keeps the incognito chats in memory and every other chat in
// the store of the server. A chat is incognito when the context that creates
// it carries a privacy level (privacy.WithLevel), or when its parent is: a
// sub-agent of an incognito chat is incognito too. Nothing of such a chat
// reaches the server's store, so it is never listed, searched or kept across
// a restart; it ends when the person discards it, when it has been idle for
// IncognitoIdle, or with the process.
//
// The incognito chats live in the memory of one process. In a cluster the
// page talks to the frontend that holds its chat (see the incognito docs); a
// request for it that reaches another frontend gets "not found".
type Incognito struct {
	Store
	mem Store

	mu    sync.Mutex
	chats map[string]*incognitoChat
}

type incognitoChat struct {
	level     privacy.Level
	principal string
	parent    string
	lastUse   time.Time
}

// IncognitoIdle is how long an incognito chat stays without any read or
// write before the sweep deletes it. A page that is open reads its chat far
// more often than this.
const IncognitoIdle = time.Hour

// NewIncognito wraps persistent: the chats created with a privacy level in
// their context go to a store in memory, every other chat to persistent. The
// second value answers the level of a chat and lists the idle incognito
// chats.
func NewIncognito(persistent Store) (Store, *Incognito) {
	mem, err := NewJSON("")
	if err != nil {
		// The memory store has no file to open; it cannot fail.
		panic(err)
	}
	s := &Incognito{Store: persistent, mem: mem, chats: map[string]*incognitoChat{}}
	if idx, ok := persistent.(chatsearch.Index); ok {
		// The search reads the server's store only: an incognito chat is
		// never a result.
		return incognitoIndexStore{Incognito: s, Index: idx}, s
	}
	return s, s
}

// incognitoIndexStore keeps the search of a server store that has one.
type incognitoIndexStore struct {
	*Incognito
	chatsearch.Index
}

// Level returns the privacy level of a chat: Normal for a chat of the
// server's store.
func (s *Incognito) Level(chatID string) privacy.Level {
	if s == nil {
		return privacy.Normal
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.chats[chatID]; c != nil {
		return c.level
	}
	return privacy.Normal
}

func (s *Incognito) pick(chatID string) Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.chats[chatID]; c != nil {
		c.lastUse = time.Now()
		return s.mem
	}
	return s.Store
}

// Idle returns the incognito chats not used since before. A sub-agent is not
// listed: it goes with its parent.
func (s *Incognito) Idle(before time.Time) []IdleChat {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []IdleChat
	for id, c := range s.chats {
		if c.parent == "" && c.lastUse.Before(before) {
			out = append(out, IdleChat{Principal: c.principal, ID: id})
		}
	}
	return out
}

// IdleChat names an incognito chat that nobody used for a while.
type IdleChat struct{ Principal, ID string }

func (s *Incognito) CreateChat(ctx context.Context, c session.ChatRecord) error {
	level := privacy.FromContext(ctx)
	if c.ParentID != "" {
		if pl := s.Level(c.ParentID); pl > level {
			level = pl
		}
	}
	if !level.Incognito() {
		return s.Store.CreateChat(ctx, c)
	}
	s.mu.Lock()
	s.chats[c.ID] = &incognitoChat{level: level, principal: c.Principal, parent: c.ParentID, lastUse: time.Now()}
	s.mu.Unlock()
	if err := s.mem.CreateChat(ctx, c); err != nil {
		s.mu.Lock()
		delete(s.chats, c.ID)
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *Incognito) UpdateChat(ctx context.Context, c session.ChatRecord) error {
	return s.pick(c.ID).UpdateChat(ctx, c)
}

func (s *Incognito) DeleteChat(ctx context.Context, principal, chatID string) error {
	if err := s.pick(chatID).DeleteChat(ctx, principal, chatID); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.chats, chatID)
	var children []string
	for id, c := range s.chats {
		if c.parent == chatID {
			children = append(children, id)
		}
	}
	s.mu.Unlock()
	// The sub-agents of an incognito chat go with it.
	for _, id := range children {
		_ = s.DeleteChat(ctx, principal, id)
	}
	return nil
}

func (s *Incognito) ListChildren(ctx context.Context, principal, parentID string) ([]session.ChatRecord, error) {
	return s.pick(parentID).ListChildren(ctx, principal, parentID)
}

func (s *Incognito) ReapTurns(ctx context.Context, idle time.Duration) ([]ReapedTurn, error) {
	a, err := s.Store.ReapTurns(ctx, idle)
	b, merr := s.mem.ReapTurns(ctx, idle)
	if err == nil {
		err = merr
	}
	return append(a, b...), err
}

func (s *Incognito) ListResumable(ctx context.Context) ([]ResumableTurn, error) {
	// An incognito chat never resumes by itself: nobody may be watching it.
	return s.Store.ListResumable(ctx)
}

// The methods below send each call to the store that holds the chat.

func (s *Incognito) GetChat(ctx context.Context, principal, chatID string) (session.ChatRecord, error) {
	return s.pick(chatID).GetChat(ctx, principal, chatID)
}

func (s *Incognito) AppendEntry(ctx context.Context, chatID string, e session.Entry) (int64, error) {
	return s.pick(chatID).AppendEntry(ctx, chatID, e)
}

func (s *Incognito) AppendEntries(ctx context.Context, chatID string, entries []session.Entry) error {
	return s.pick(chatID).AppendEntries(ctx, chatID, entries)
}

func (s *Incognito) Entries(ctx context.Context, chatID string, after int64, limit int) ([]session.Entry, error) {
	return s.pick(chatID).Entries(ctx, chatID, after, limit)
}

func (s *Incognito) SavePartial(ctx context.Context, chatID string, turn int, text string) error {
	return s.pick(chatID).SavePartial(ctx, chatID, turn, text)
}

func (s *Incognito) TakePartial(ctx context.Context, chatID string) (turn int, text string, err error) {
	return s.pick(chatID).TakePartial(ctx, chatID)
}

func (s *Incognito) AddPending(ctx context.Context, principal, chatID, text string) (Pending, error) {
	return s.pick(chatID).AddPending(ctx, principal, chatID, text)
}

func (s *Incognito) AddPendingFrom(ctx context.Context, principal, chatID, text string, sender *session.Sender) (Pending, error) {
	return s.pick(chatID).AddPendingFrom(ctx, principal, chatID, text, sender)
}

func (s *Incognito) Pending(ctx context.Context, principal, chatID string) ([]Pending, error) {
	return s.pick(chatID).Pending(ctx, principal, chatID)
}

func (s *Incognito) EditPending(ctx context.Context, principal, chatID, pendingID, text string) error {
	return s.pick(chatID).EditPending(ctx, principal, chatID, pendingID, text)
}

func (s *Incognito) CancelPending(ctx context.Context, principal, chatID, pendingID string) error {
	return s.pick(chatID).CancelPending(ctx, principal, chatID, pendingID)
}

func (s *Incognito) TakePending(ctx context.Context, principal, chatID string) ([]Pending, error) {
	return s.pick(chatID).TakePending(ctx, principal, chatID)
}

func (s *Incognito) ConfirmPending(ctx context.Context, principal, chatID string, ids []string) error {
	return s.pick(chatID).ConfirmPending(ctx, principal, chatID, ids)
}

func (s *Incognito) RestorePending(ctx context.Context, principal, chatID string, ids []string) error {
	return s.pick(chatID).RestorePending(ctx, principal, chatID, ids)
}

func (s *Incognito) SetSkillsOff(ctx context.Context, principal, chatID string, off []string) error {
	return s.pick(chatID).SetSkillsOff(ctx, principal, chatID, off)
}

func (s *Incognito) SetAddonsOff(ctx context.Context, principal, chatID string, off []string) error {
	return s.pick(chatID).SetAddonsOff(ctx, principal, chatID, off)
}

func (s *Incognito) SetMcpOff(ctx context.Context, principal, chatID string, off []string) error {
	return s.pick(chatID).SetMcpOff(ctx, principal, chatID, off)
}

func (s *Incognito) SetPluginsOff(ctx context.Context, principal, chatID string, off []string) error {
	return s.pick(chatID).SetPluginsOff(ctx, principal, chatID, off)
}

func (s *Incognito) SetBuiltinsOff(ctx context.Context, principal, chatID string, off []string) error {
	return s.pick(chatID).SetBuiltinsOff(ctx, principal, chatID, off)
}

func (s *Incognito) SetBuiltinsOn(ctx context.Context, principal, chatID string, on []string) error {
	return s.pick(chatID).SetBuiltinsOn(ctx, principal, chatID, on)
}

func (s *Incognito) SetConnections(ctx context.Context, principal, chatID string, connections connsets.Sets) error {
	return s.pick(chatID).SetConnections(ctx, principal, chatID, connections)
}

func (s *Incognito) SetScopedAgent(ctx context.Context, principal, chatID string, scoped bool) error {
	return s.pick(chatID).SetScopedAgent(ctx, principal, chatID, scoped)
}

func (s *Incognito) SetProject(ctx context.Context, principal, chatID, projectID string) error {
	return s.pick(chatID).SetProject(ctx, principal, chatID, projectID)
}

func (s *Incognito) SetPulledProjects(ctx context.Context, principal, chatID string, ids []string) error {
	return s.pick(chatID).SetPulledProjects(ctx, principal, chatID, ids)
}

func (s *Incognito) SetCollections(ctx context.Context, principal, chatID string, names []string) error {
	return s.pick(chatID).SetCollections(ctx, principal, chatID, names)
}

func (s *Incognito) SetHostConsent(ctx context.Context, principal, chatID string, c *session.HostConsent) error {
	return s.pick(chatID).SetHostConsent(ctx, principal, chatID, c)
}

func (s *Incognito) UpdateHostConsent(ctx context.Context, principal, chatID string, change func(cur *session.HostConsent) (*session.HostConsent, error)) error {
	return s.pick(chatID).UpdateHostConsent(ctx, principal, chatID, change)
}

func (s *Incognito) UpdateJobs(ctx context.Context, principal, chatID string, change func(cur []session.JobStub) ([]session.JobStub, error)) error {
	return s.pick(chatID).UpdateJobs(ctx, principal, chatID, change)
}

func (s *Incognito) UpdateEgress(ctx context.Context, principal, chatID string, change func(*egress.ChatState) error) error {
	return s.pick(chatID).UpdateEgress(ctx, principal, chatID, change)
}

func (s *Incognito) UpdateGoal(ctx context.Context, principal, chatID string, change func(cur *session.Goal) (*session.Goal, error)) error {
	return s.pick(chatID).UpdateGoal(ctx, principal, chatID, change)
}

func (s *Incognito) UpdateDock(ctx context.Context, principal, chatID string, change func(cur *session.Dock) (*session.Dock, error)) error {
	return s.pick(chatID).UpdateDock(ctx, principal, chatID, change)
}

func (s *Incognito) SetChatMeta(ctx context.Context, principal, chatID string, m session.ChatMeta) error {
	return s.pick(chatID).SetChatMeta(ctx, principal, chatID, m)
}

func (s *Incognito) TruncateEntries(ctx context.Context, principal, chatID string, keep int64) error {
	return s.pick(chatID).TruncateEntries(ctx, principal, chatID, keep)
}

func (s *Incognito) ClaimTurn(ctx context.Context, principal, chatID, runID string) error {
	return s.pick(chatID).ClaimTurn(ctx, principal, chatID, runID)
}

func (s *Incognito) ReleaseTurn(ctx context.Context, principal, chatID, runID string, status TurnStatus) error {
	return s.pick(chatID).ReleaseTurn(ctx, principal, chatID, runID, status)
}

func (s *Incognito) TouchTurn(ctx context.Context, principal, chatID, runID string) error {
	return s.pick(chatID).TouchTurn(ctx, principal, chatID, runID)
}

func (s *Incognito) SetTurnOwner(ctx context.Context, principal, chatID, runID, instance string) error {
	return s.pick(chatID).SetTurnOwner(ctx, principal, chatID, runID, instance)
}

func (s *Incognito) MarkInterrupted(ctx context.Context, principal, chatID, runID string, delay time.Duration) error {
	return s.pick(chatID).MarkInterrupted(ctx, principal, chatID, runID, delay)
}

func (s *Incognito) ClaimResume(ctx context.Context, principal, chatID, runID string) error {
	return s.pick(chatID).ClaimResume(ctx, principal, chatID, runID)
}

func (s *Incognito) CancelResume(ctx context.Context, principal, chatID string) error {
	return s.pick(chatID).CancelResume(ctx, principal, chatID)
}

func (s *Incognito) Turn(ctx context.Context, principal, chatID string) (TurnInfo, error) {
	return s.pick(chatID).Turn(ctx, principal, chatID)
}
