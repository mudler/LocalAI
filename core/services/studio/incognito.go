package studio

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/privacy"
	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// Incognito chats. A chat created with a privacy level in its context (the
// web UI's incognito switch, see core/privacy) lives in memory only
// (store/incognito.go) and keeps its level for its whole life:
//
//	memory-off      no memory is read or saved (memoryState), no knowledge is
//	                written, the notes of a project are not read
//	zero-retention  memory-off, and the chat only gets the tools that keep
//	                nothing (zeroRetentionTools); every model request and tool
//	                call carries the level, so traces, logs and caches of the
//	                server keep nothing of it either
//
// What would make the chat outlive the person's session is refused:
// attachments, which are written to the person's files, a fork, a move to a
// project. An incognito chat never runs on a chat worker and never resumes by
// itself.

// ErrIncognito means the action would keep something of an incognito chat.
var ErrIncognito = errors.New("studio: not available in an incognito chat")

// zeroRetentionTools are the only tools a zero-retention chat gets. They read
// and keep nothing on the server. The list is explicit on purpose: a tool
// added later is left out until someone checks what it keeps. The collection
// search carries the level into its embedding calls (collections/privacy.go).
var zeroRetentionTools = []string{
	webSearchName, webFetchName,
	"list_collections", "search_collection",
	"search_chats", "read_chat",
	contextfiles.ToolName, loadSkillName,
}

// zeroRetentionNote tells the model why it has so few tools.
const zeroRetentionNote = "This chat is incognito with zero retention: nothing of it is kept on the server. You have no tools that write files, save notes or memory, run commands, start sub-agents or schedule anything. If asked to, say so."

// chatPrivacy is the privacy level of a chat: the level it was created with,
// or the one ctx carries, whichever is higher.
func (s *Service) chatPrivacy(ctx context.Context, rec session.ChatRecord) privacy.Level {
	l := privacy.FromContext(ctx)
	if cl := s.incognito.Level(rec.ID); cl > l {
		l = cl
	}
	if rec.ParentID != "" {
		if pl := s.incognito.Level(rec.ParentID); pl > l {
			l = pl
		}
	}
	return l
}

// ChatPrivacy is the privacy level of a chat, for the API.
func (s *Service) ChatPrivacy(chatID string) privacy.Level { return s.incognito.Level(chatID) }

// chatTools gives the tools of a turn, limited by the chat's privacy level.
func (s *Service) chatTools(ctx context.Context, x session.Access) (session.TurnTools, error) {
	tt, err := s.chatToolsAll(ctx, x)
	if err != nil {
		return tt, err
	}
	a := x.Actor()
	if s.incognito.Level(a.ChatID) < privacy.ZeroRetention {
		return tt, nil
	}
	tt.Tools = slices.DeleteFunc(slices.Clone(tt.Tools), func(t session.Tool) bool {
		return !slices.Contains(zeroRetentionTools, t.Name)
	})
	// Nothing to save into: the session has no work folder for the chat.
	tt.WorkFolder = false
	tt.Note = joinNote(zeroRetentionNote, tt.Note)
	return tt, nil
}

func joinNote(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " " + b
}

// refuseIncognito returns ErrIncognito for a chat that is incognito.
func (s *Service) refuseIncognito(conv string) error {
	if s.incognito.Level(conv).Incognito() {
		return ErrIncognito
	}
	return nil
}

// incognitoSweepEvery is how often the idle incognito chats are looked for.
const incognitoSweepEvery = 5 * time.Minute

// sweepIncognito deletes the incognito chats nobody used for incognitoIdle:
// a page that was closed without discarding its chat leaves it behind.
func (s *Service) sweepIncognito(every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-s.closed:
			return
		case <-tick.C:
			s.discardIncognito(time.Now().Add(-store.IncognitoIdle))
		}
	}
}

// discardIncognito deletes the incognito chats last used before before.
func (s *Service) discardIncognito(before time.Time) {
	for _, c := range s.incognito.Idle(before) {
		if err := s.Delete(context.Background(), c.Principal, c.ID); err != nil && !errors.Is(err, session.ErrNotFound) {
			s.log.Warn("studio: could not discard an idle incognito chat", "chat", c.ID, "error", err)
		}
	}
}
