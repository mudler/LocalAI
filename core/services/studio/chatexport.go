package studio

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// ExportVersion is the version of the JSON export format.
const ExportVersion = 1

// Limits of an imported chat.
const (
	maxImportEntries     = 20000
	maxImportEntryText   = 1 << 20
	maxImportAttachments = 1000 // per message
)

// ErrBadImport means an imported chat is not valid.
var ErrBadImport = fmt.Errorf("studio: the chat to import is not valid")

// ChatExport is a chat as a file: enough to read it elsewhere or to import it
// again. It holds no files and no model state.
type ChatExport struct {
	Version    int                  `json:"version"`
	ExportedAt time.Time            `json:"exported_at"`
	Title      string               `json:"title"`
	Model      string               `json:"model"`
	Settings   session.ChatSettings `json:"settings"`
	Created    time.Time            `json:"created"`
	Updated    time.Time            `json:"updated"`
	Messages   []ChatExportMessage  `json:"messages"`
}

// ChatExportMessage is one line of an exported transcript. Role is user,
// assistant, tool or reasoning. A user message carries the files it attached
// (their paths inside the user's home, not the files themselves) and the text
// the model read from them, apart from the words.
type ChatExportMessage struct {
	Role        string               `json:"role"`
	Text        string               `json:"text"`
	Attachments []session.Attachment `json:"attachments,omitempty"`
	Note        string               `json:"note,omitempty"`
	At          time.Time            `json:"at"`
}

// Export returns the chat as a ChatExport. Checkpoints are left out.
func (s *Service) Export(ctx context.Context, user, conv string) (ChatExport, error) {
	p := principal(user)
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil {
		return ChatExport{}, err
	}
	entries, err := s.d.Store.Entries(ctx, conv, 0, 0)
	if err != nil {
		return ChatExport{}, err
	}
	out := ChatExport{Version: ExportVersion, ExportedAt: time.Now().UTC(), Title: rec.Title, Model: rec.Model,
		Settings: rec.Settings, Created: rec.Created, Updated: rec.Updated, Messages: []ChatExportMessage{}}
	for _, e := range entries {
		if e.Role == session.RoleCheckpoint || e.Role == session.RoleGoal {
			continue
		}
		d := session.DisplayEntry(e)
		out.Messages = append(out.Messages, ChatExportMessage{Role: d.Role, Text: d.Text, Attachments: d.Attachments, Note: d.Note, At: d.At})
	}
	return out, nil
}

// Markdown renders the export as a Markdown document. Reasoning is left out;
// a tool call is one quoted line.
func (e ChatExport) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", e.Title)
	fmt.Fprintf(&b, "_Model: %s. Exported %s._\n", e.Model, e.ExportedAt.Format("2006-01-02 15:04 MST"))
	for _, m := range e.Messages {
		switch m.Role {
		case session.RoleUser:
			b.WriteString("\n## You\n")
			if m.Text != "" {
				fmt.Fprintf(&b, "\n%s\n", m.Text)
			}
			if len(m.Attachments) > 0 {
				b.WriteString("\nAttached:\n")
				for _, a := range m.Attachments {
					if a.Size > 0 {
						fmt.Fprintf(&b, "- %s (%s)\n", a.Name, session.SizeLabel(a.Size))
					} else {
						fmt.Fprintf(&b, "- %s\n", a.Name)
					}
				}
			}
		case session.RoleAssistant:
			fmt.Fprintf(&b, "\n## Assistant\n\n%s\n", m.Text)
		case session.RoleTool:
			fmt.Fprintf(&b, "\n> Tool: %s\n", strings.ReplaceAll(strings.TrimSpace(m.Text), "\n", "\n> "))
		}
	}
	return b.String()
}

// ImportExport stores an exported chat as a new chat of the user. model, when
// not empty, replaces the model of the file; the file must name one otherwise.
// The caller checks that the user may use the model.
func (s *Service) ImportExport(ctx context.Context, user string, in ChatExport, model string) (session.ChatRecord, error) {
	if in.Version != ExportVersion {
		return session.ChatRecord{}, fmt.Errorf("%w: version %d is not supported", ErrBadImport, in.Version)
	}
	if len(in.Messages) > maxImportEntries {
		return session.ChatRecord{}, fmt.Errorf("%w: more than %d messages", ErrBadImport, maxImportEntries)
	}
	if model == "" {
		model = in.Model
	}
	if strings.TrimSpace(model) == "" {
		return session.ChatRecord{}, ErrNoModel
	}
	if err := ValidateSettings(in.Settings); err != nil {
		return session.ChatRecord{}, err
	}
	entries := make([]ImportedEntry, 0, len(in.Messages))
	for _, m := range in.Messages {
		if !utf8.ValidString(m.Text) || len(m.Text) > maxImportEntryText || !utf8.ValidString(m.Note) || len(m.Note) > maxImportEntryText {
			return session.ChatRecord{}, fmt.Errorf("%w: a message is too large or not text", ErrBadImport)
		}
		if err := checkImportAttachments(m.Attachments); err != nil {
			return session.ChatRecord{}, err
		}
		switch m.Role {
		case session.RoleUser, session.RoleAssistant, session.RoleTool, session.RoleReasoning:
			e := ImportedEntry{Role: m.Role, Text: m.Text, At: m.At}
			if m.Role == session.RoleUser {
				e.Note, e.Attachments = m.Note, m.Attachments
			}
			entries = append(entries, e)
		default:
			return session.ChatRecord{}, fmt.Errorf("%w: unknown role %q", ErrBadImport, m.Role)
		}
	}
	rec, err := s.ImportChat(ctx, user, session.CleanTitle(in.Title), model, in.Created, in.Updated, entries)
	if err != nil {
		return session.ChatRecord{}, err
	}
	p := rec.Principal
	if in.Settings != (session.ChatSettings{}) {
		if err := s.d.Store.SetChatMeta(ctx, p, rec.ID, session.ChatMeta{Settings: &in.Settings}); err != nil {
			_ = s.d.Store.DeleteChat(ctx, p, rec.ID)
			return session.ChatRecord{}, err
		}
	}
	stored, err := s.d.Store.Entries(ctx, rec.ID, 0, 0)
	if err != nil {
		return session.ChatRecord{}, err
	}
	if state := s.rebuildState(stored); len(state) > 0 {
		cur, err := s.d.Store.GetChat(ctx, p, rec.ID)
		if err != nil {
			return session.ChatRecord{}, err
		}
		cur.HarnessState = state
		if err := s.d.Store.UpdateChat(ctx, cur); err != nil {
			return session.ChatRecord{}, err
		}
	}
	return s.d.Store.GetChat(ctx, p, rec.ID)
}

// checkImportAttachments refuses attachments that are not paths inside a home
// folder. The files themselves are not in an export.
func checkImportAttachments(atts []session.Attachment) error {
	if len(atts) > maxImportAttachments {
		return fmt.Errorf("%w: a message has more than %d files", ErrBadImport, maxImportAttachments)
	}
	for _, a := range atts {
		if !utf8.ValidString(a.Path) || !utf8.ValidString(a.Name) || len(a.Path) > 4096 || len(a.Name) > 1024 ||
			!strings.HasPrefix(a.Path, "/") || path.Clean(a.Path) != a.Path || strings.ContainsAny(a.Path, "\n\x00") {
			return fmt.Errorf("%w: an attachment has a path that is not allowed", ErrBadImport)
		}
	}
	return nil
}

// SearchHit is a chat that matches a search.
type SearchHit struct {
	Chat session.ChatRecord
	// InTitle is true when the title matches.
	InTitle bool
	// Seq is the first matching transcript line, or 0 when only the title matches.
	Seq int64
	// Snippet is the matching text with a little around it.
	Snippet string
}

// Limits of a search.
const (
	maxSearchChats = 500
	maxSearchBytes = 64 << 20
)

// Search finds the user's chats whose title or user and assistant messages
// contain query, ignoring case. The newest chats come first. It reads at most
// the newest maxSearchChats chats and maxSearchBytes of text.
func (s *Service) Search(ctx context.Context, user, query string, limit int) ([]SearchHit, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, fmt.Errorf("%w: empty query", session.ErrInvalid)
	}
	chats, err := s.ListChats(ctx, user, maxSearchChats)
	if err != nil {
		return nil, err
	}
	var (
		hits  []SearchHit
		spent int
	)
	for _, rec := range chats {
		if limit > 0 && len(hits) >= limit {
			break
		}
		hit := SearchHit{Chat: rec}
		if strings.Contains(strings.ToLower(rec.Title), q) {
			hit.InTitle = true
		}
		if spent < maxSearchBytes {
			entries, err := s.d.Store.Entries(ctx, rec.ID, 0, 0)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				spent += len(e.Text)
				if e.Role != session.RoleUser && e.Role != session.RoleAssistant {
					continue
				}
				// The words the person reads, not the list of files the model gets.
				text := session.DisplayEntry(e).Text
				if i := strings.Index(strings.ToLower(text), q); i >= 0 {
					hit.Seq, hit.Snippet = e.Seq, snippet(text, i, len(q))
					break
				}
			}
		}
		if hit.InTitle || hit.Seq != 0 {
			hits = append(hits, hit)
		}
	}
	return hits, nil
}

// snippet returns the text around a match, about 120 bytes wide, on rune
// boundaries.
func snippet(text string, at, n int) string {
	at = min(at, len(text))
	from, to := max(0, at-50), min(len(text), at+n+70)
	for from > 0 && !utf8.RuneStart(text[from]) {
		from--
	}
	for to < len(text) && !utf8.RuneStart(text[to]) {
		to++
	}
	out := strings.Join(strings.Fields(text[from:to]), " ")
	if from > 0 {
		out = "…" + out
	}
	if to < len(text) {
		out += "…"
	}
	return out
}
