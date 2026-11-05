package studio

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/chatsearch"
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
// assistant, tool, reasoning, or one of the markers of the context commands:
// compacted (Text says the sizes) and cleared (the model saw nothing before it). A user message carries the files it attached
// (their paths inside the user's home, not the files themselves) and the text
// the model read from them, apart from the words.
type ChatExportMessage struct {
	Role        string               `json:"role"`
	Text        string               `json:"text"`
	Attachments []session.Attachment `json:"attachments,omitempty"`
	Note        string               `json:"note,omitempty"`
	// Invoked is the skill or saved agent a user message was sent with.
	Invoked *session.Invocation `json:"invoked,omitempty"`
	At      time.Time           `json:"at"`
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
		if e.Role == session.RoleCheckpoint || e.Role == session.RoleGoal || session.DisplayOnly(e.Role) {
			continue
		}
		d := session.DisplayEntry(e)
		out.Messages = append(out.Messages, ChatExportMessage{Role: d.Role, Text: d.Text, Attachments: d.Attachments, Note: d.Note, Invoked: d.Invoked, At: d.At})
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
			if m.Invoked != nil {
				label := "Skill"
				if m.Invoked.Kind == session.InvokeAgent {
					label = "Agent"
				}
				fmt.Fprintf(&b, "\n_%s: %s_\n", label, m.Invoked.Name)
			}
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
		case session.RoleCompacted:
			fmt.Fprintf(&b, "\n_%s tokens._\n", m.Text)
		case session.RoleCleared:
			b.WriteString("\n---\n\n_Context cleared. The model saw nothing above this line from here on._\n")
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
		case session.RoleUser, session.RoleAssistant, session.RoleTool, session.RoleReasoning, session.RoleCompacted, session.RoleCleared:
			e := ImportedEntry{Role: m.Role, Text: m.Text, At: m.At}
			if m.Role == session.RoleUser {
				e.Note, e.Attachments, e.Invoked = m.Note, m.Attachments, cleanInvocation(m.Invoked)
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
	if state := s.rebuildState(ctx, rec, stored); len(state) > 0 {
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

// SearchHit is a message (or a chat title) that matches a search. Chat, InTitle,
// Seq and Snippet are what the first version of the search gave.
type SearchHit = chatsearch.Hit

// SearchOptions is a search of the chats of one user.
type SearchOptions struct {
	// Query is the text to find: words that must all be in a message, or a
	// phrase in double quotes.
	Query string
	// Limit is the most hits; 0 is 50.
	Limit int
	// AgentID, Origin, Role, Since and Until narrow the search; see
	// chatsearch.Query.
	AgentID, Origin, Role string
	Since, Until          time.Time
	// ChatID limits the search to one chat.
	ChatID string
	// PerChat returns the best message of each chat.
	PerChat bool
	// Titles also matches chat titles.
	Titles bool
	// SnippetRunes is the width of a snippet; 0 is the default.
	SnippetRunes int
	// Exclude drops the chats it returns true for.
	Exclude func(session.ChatRecord) bool
}

// ErrSearchUnavailable means the chat store cannot search text.
var ErrSearchUnavailable = errors.New("studio: this chat store cannot search text")

// SearchChats finds the user's chats by their messages (and titles) as the
// store's index allows, newest and best match first. It looks at every chat of
// the user except sub-agent chats; the owner is applied by the store in the
// query itself.
func (s *Service) SearchChats(ctx context.Context, user string, o SearchOptions) (chatsearch.Result, error) {
	if len(chatsearch.Terms(o.Query)) == 0 {
		return chatsearch.Result{}, fmt.Errorf("%w: empty query", session.ErrInvalid)
	}
	idx, ok := s.d.Store.(chatsearch.Index)
	if !ok {
		return chatsearch.Result{}, ErrSearchUnavailable
	}
	return idx.SearchText(ctx, chatsearch.Query{
		Principal: principal(user), Text: o.Query, Limit: o.Limit, AgentID: o.AgentID, Origin: o.Origin,
		Role: o.Role, Since: o.Since, Until: o.Until, ChatID: o.ChatID, PerChat: o.PerChat, Titles: o.Titles,
		SnippetRunes: o.SnippetRunes, Exclude: o.Exclude,
	})
}

// Search finds the user's chats whose title or user and assistant messages
// contain every word of query, ignoring case: one hit per chat.
func (s *Service) Search(ctx context.Context, user, query string, limit int) ([]SearchHit, error) {
	res, err := s.SearchChats(ctx, user, SearchOptions{Query: query, Limit: limit, PerChat: true, Titles: true})
	return res.Hits, err
}

// cleanInvocation keeps the invocation of an imported message only when it is
// one this server writes: a known kind and a short name.
func cleanInvocation(inv *session.Invocation) *session.Invocation {
	if inv == nil || (inv.Kind != session.InvokeSkill && inv.Kind != session.InvokeAgent) ||
		!utf8.ValidString(inv.Name) || !utf8.ValidString(inv.ID) || len(inv.Name) > 256 || len(inv.ID) > 256 {
		return nil
	}
	return &session.Invocation{Kind: inv.Kind, Name: inv.Name, ID: inv.ID}
}
