package studio

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// ImportedEntry is one line of a transcript that comes from outside the loop.
type ImportedEntry struct {
	Role, Text string
	At         time.Time
}

// ImportChat stores a finished chat of the user, made elsewhere: the title, the
// model it talked to and a transcript. It keeps the given times. The chat has no
// turn, no folder and no sandbox, so the next message starts it as any other.
// Roles other than user, assistant, tool and reasoning are left out.
func (s *Service) ImportChat(ctx context.Context, user, title, model string, created, updated time.Time, entries []ImportedEntry) (session.ChatRecord, error) {
	if strings.TrimSpace(title) == "" {
		title = session.DefaultTitle
	}
	if created.IsZero() {
		created = time.Now().UTC()
	}
	if updated.Before(created) {
		updated = created
	}
	rec := session.ChatRecord{ID: newID(), Principal: principal(user), Title: title, Created: created.UTC(), Updated: updated.UTC(),
		Kind: session.KindChat, Model: model}
	if err := s.d.Store.CreateChat(ctx, rec); err != nil {
		return session.ChatRecord{}, err
	}
	fail := func(err error) (session.ChatRecord, error) {
		_ = s.d.Store.DeleteChat(ctx, rec.Principal, rec.ID) // no half-imported chat is left behind
		return session.ChatRecord{}, err
	}
	for _, e := range entries {
		switch e.Role {
		case session.RoleUser, session.RoleAssistant, session.RoleTool, session.RoleReasoning:
		default:
			continue
		}
		text := e.Text
		if e.Role == session.RoleReasoning && len(text) > session.MaxReasoningBytes {
			text = strings.ToValidUTF8(text[:session.MaxReasoningBytes], "")
		}
		if err := s.d.Store.AppendEntry(ctx, rec.ID, session.Entry{Role: e.Role, Text: text, At: e.At.UTC()}); err != nil {
			return fail(err)
		}
	}
	if err := s.d.Store.UpdateChat(ctx, rec); err != nil {
		return fail(err)
	}
	return rec, nil
}
