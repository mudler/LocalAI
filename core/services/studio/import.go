package studio

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/convert"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// importBatch is how many entries an import writes at once, and importPause the
// wait between two writes.
const (
	importBatch = 200
	importPause = 5 * time.Millisecond
)

// ImportedEntry is one line of a transcript that comes from outside the loop.
type ImportedEntry struct {
	Role, Text string
	// Attachments and Note belong to a user message (see session.Entry).
	Attachments []session.Attachment
	Note        string
	// Invoked is the command of a user message.
	Invoked *session.Invocation
	At      time.Time
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
	batch := make([]session.Entry, 0, importBatch)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := s.d.Store.AppendEntries(ctx, rec.ID, batch)
		batch = batch[:0]
		// Give the writer back between batches: on SQLite a write holds the
		// lock of the whole database, and logins wait behind it.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(importPause):
		}
		return err
	}
	for _, e := range entries {
		switch e.Role {
		case session.RoleUser, session.RoleAssistant, session.RoleTool, session.RoleReasoning, session.RoleCompacted, session.RoleCleared:
		default:
			continue
		}
		text := e.Text
		if e.Role == session.RoleReasoning && len(text) > session.MaxReasoningBytes {
			text = strings.ToValidUTF8(text[:session.MaxReasoningBytes], "")
		}
		entry := session.Entry{Role: e.Role, Text: text, At: e.At.UTC()}
		if e.Role == session.RoleUser {
			// An old export holds the list of files in the text.
			entry.Note, entry.Attachments, entry.Invoked = e.Note, e.Attachments, e.Invoked
			entry = session.DisplayEntry(entry)
		}
		batch = append(batch, entry)
		if len(batch) == importBatch {
			if err := flush(); err != nil {
				return fail(err)
			}
		}
	}
	if err := flush(); err != nil {
		return fail(err)
	}
	if err := s.d.Store.UpdateChat(ctx, rec); err != nil {
		return fail(err)
	}
	return rec, nil
}

// BrowserImport is the outcome of ImportBrowserChats.
type BrowserImport struct {
	// Imported chats are new; Skipped ones were there already or hold nothing
	// that can be imported; Failed ones could not be stored.
	Imported, Skipped, Failed int
}

// ImportBrowserChats stores the chats the old chat page kept in the browser:
// data is the value of localStorage "localai_chats_data". A chat the user has
// already, with the same title and creation time, is skipped, so an upload can
// be repeated.
func (s *Service) ImportBrowserChats(ctx context.Context, user string, data []byte) (BrowserImport, error) {
	chats, unusable, err := convert.ParseBrowserChats(data)
	if err != nil {
		return BrowserImport{}, err
	}
	out := BrowserImport{Skipped: unusable}
	have, err := s.ListChats(ctx, user, 0)
	if err != nil {
		return out, err
	}
	type key struct {
		title   string
		created int64
	}
	seen := map[key]bool{}
	for _, r := range have {
		seen[key{r.Title, r.Created.UnixMilli()}] = true
	}
	for _, c := range chats {
		k := key{c.Title, c.Created.UnixMilli()}
		if seen[k] {
			out.Skipped++
			continue
		}
		entries := make([]ImportedEntry, len(c.Entries))
		for i, e := range c.Entries {
			entries[i] = ImportedEntry{Role: e.Role, Text: e.Text, At: e.At}
		}
		if _, err := s.ImportChat(ctx, user, c.Title, c.Model, c.Created, c.Updated, entries); err != nil {
			out.Failed++
			continue
		}
		seen[k] = true
		out.Imported++
	}
	return out, nil
}
