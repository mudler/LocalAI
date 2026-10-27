package convert

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type oldConversation struct {
	Name      string          `json:"name"`
	Model     string          `json:"model"`
	History   json.RawMessage `json:"history"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
}

// textOf reads the text of a message: a string, or the text parts of a list.
func textOf(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	var out []string
	for _, p := range parts {
		if (p.Type == "text" || p.Type == "") && p.Text != "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

// toolOf reads the name and the text of a tool message. The old page stored the
// call and the result as JSON text in the content, {"type":"tool_call","name":
// ...} and {"type":"tool_result","name":...,"result":...}; the fields beside the
// content are the fallback for a message without that form.
func toolOf(name string, content json.RawMessage) (toolName, text string) {
	toolName, text = name, strings.TrimSpace(textOf(content))
	var body struct {
		Name   string          `json:"name"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal([]byte(text), &body) != nil || body.Name == "" && body.Result == nil {
		return toolName, text
	}
	if body.Name != "" {
		toolName = body.Name
	}
	text = ""
	if body.Result != nil {
		var s string
		if json.Unmarshal(body.Result, &s) == nil {
			text = strings.TrimSpace(s)
		} else if string(body.Result) != "null" {
			text = string(body.Result)
		}
	}
	return toolName, text
}

// attachmentsOf counts what the old page attached to messages and the new chat
// does not carry over: the files of a message and the parts of its content that
// are not text (pictures, audio).
func attachmentsOf(history json.RawMessage) int {
	var msgs []struct {
		Content json.RawMessage   `json:"content"`
		Files   []json.RawMessage `json:"files"`
	}
	if json.Unmarshal(history, &msgs) != nil {
		return 0
	}
	n := 0
	for _, m := range msgs {
		n += len(m.Files)
		var parts []struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(m.Content, &parts) == nil {
			for _, p := range parts {
				if p.Type != "" && p.Type != "text" {
					n++
				}
			}
		}
	}
	return n
}

// entriesOf maps the messages of the old page to transcript lines: user and
// assistant text, thinking as reasoning, and each tool call or result as one
// tool line. It keeps the newest MaxMessages.
func entriesOf(history json.RawMessage, at time.Time) []EntryIn {
	var msgs []struct {
		Role    string          `json:"role"`
		Name    string          `json:"name"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(history, &msgs) != nil {
		return nil
	}
	var out []EntryIn
	for _, m := range msgs {
		text := strings.TrimSpace(textOf(m.Content))
		switch m.Role {
		case "user", "assistant":
			if text != "" {
				out = append(out, EntryIn{Role: m.Role, Text: text, At: at})
			}
		case "thinking", "reasoning":
			if text != "" {
				out = append(out, EntryIn{Role: "reasoning", Text: text, At: at})
			}
		case "tool_call":
			name, _ := toolOf(m.Name, m.Content)
			out = append(out, EntryIn{Role: "tool", Text: "called " + name, At: at})
		case "tool_result":
			name, text := toolOf(m.Name, m.Content)
			line := "result of " + name
			if text != "" {
				if len(text) > 500 {
					// Cut on a rune boundary: a cut inside a character is not text.
					cut := 500
					for cut > 0 && !utf8.RuneStart(text[cut]) {
						cut--
					}
					text = text[:cut] + "…"
				}
				line += ": " + text
			}
			// A result joins the call before it, so one tool use is one line.
			if n := len(out); n > 0 && out[n-1].Role == "tool" && out[n-1].Text == "called "+name {
				out[n-1].Text = line
			} else {
				out = append(out, EntryIn{Role: "tool", Text: line, At: at})
			}
		}
	}
	if len(out) > MaxMessages {
		out = out[len(out)-MaxMessages:]
	}
	return out
}

// historyPage is how many conversations are listed at once. The content of a
// conversation is read one at a time, and only when it is not converted yet.
const historyPage = 100

type convRef struct {
	UserID string `gorm:"column:user_id"`
	ConvID string `gorm:"column:conv_id"`
}

func (r *run) historyStep(ctx context.Context) error {
	if !hasTable(r.src.DB, "chat_conversations") {
		return nil
	}
	var after *convRef
	for {
		q := r.src.DB.WithContext(ctx).Table("chat_conversations").Select("user_id, conv_id").Where("deleted_at IS NULL")
		if after != nil {
			q = q.Where("(user_id > ? OR (user_id = ? AND conv_id > ?))", after.UserID, after.UserID, after.ConvID)
		}
		var page []convRef
		if err := q.Order("user_id, conv_id").Limit(historyPage).Find(&page).Error; err != nil {
			r.add(KindChat, "", "saved conversations", StatusFailed, "could not read the table: "+err.Error())
			return nil
		}
		for _, ref := range page {
			r.convertChat(ctx, ref)
		}
		if len(page) < historyPage {
			return nil
		}
		after = &page[len(page)-1]
	}
}

// convertChat imports one conversation. The mark is made before the import,
// with the creation time the chat will get, so a process that dies half way
// leaves a trace: the next run finds the mark, discards the half chat that has
// that title and time, and imports again. Without it the chat would be twice.
func (r *run) convertChat(ctx context.Context, ref convRef) {
	key := "chat|" + ref.UserID + "|" + ref.ConvID
	mark, marked := r.done(ctx, key)
	if marked && !strings.HasPrefix(mark, refImporting) {
		return // converted or dropped; its line is in the report of the run that did it
	}
	owner, why := r.owner(ctx, ref.UserID)
	if why != "" {
		r.add(KindChat, ref.UserID, ref.ConvID, StatusSkipped, why)
		return
	}
	var content string
	if err := r.src.DB.WithContext(ctx).Table("chat_conversations").Select("content").
		Where("user_id = ? AND conv_id = ? AND deleted_at IS NULL", ref.UserID, ref.ConvID).Limit(1).Scan(&content).Error; err != nil {
		r.add(KindChat, ref.UserID, ref.ConvID, StatusFailed, "could not read the conversation: "+err.Error())
		return
	}
	var c oldConversation
	if json.Unmarshal([]byte(content), &c) != nil {
		r.drop(ctx, key, KindChat, ref.UserID, ref.ConvID, "the conversation cannot be read")
		return
	}
	title := strings.TrimSpace(c.Name)
	if title == "" {
		title = "Imported chat"
	}
	created, updated := time.UnixMilli(c.CreatedAt).UTC(), time.UnixMilli(c.UpdatedAt).UTC()
	if c.CreatedAt == 0 {
		created = time.Now().UTC().Truncate(time.Millisecond)
	}
	if marked {
		// A run that died after the mark: take its time and clear what it left.
		if ms, err := strconv.ParseInt(strings.TrimPrefix(mark, refImporting), 10, 64); err == nil {
			created = time.UnixMilli(ms).UTC()
		}
		if err := r.tg.DiscardChat(ctx, owner, title, created); err != nil {
			r.add(KindChat, ref.UserID, title, StatusFailed, "could not clear an earlier import: "+err.Error())
			return
		}
	}
	if c.UpdatedAt == 0 {
		updated = created
	}
	if err := r.marks.Mark(ctx, key, refImporting+strconv.FormatInt(created.UnixMilli(), 10)); err != nil {
		r.add(KindChat, ref.UserID, title, StatusFailed, "could not remember the conversion: "+err.Error())
		return
	}
	entries := entriesOf(c.History, created)
	if err := r.tg.ImportChat(ctx, owner, ChatIn{Title: title, Model: c.Model, Created: created, Updated: updated, Entries: entries}); err != nil {
		r.add(KindChat, ref.UserID, title, StatusFailed, err.Error())
		return
	}
	r.mark(ctx, key, "chat")
	reason := ""
	if n := attachmentsOf(c.History); n > 0 {
		reason = "pictures, audio and files are not converted: " + plural(n, "attachment")
	}
	r.add(KindChat, ref.UserID, title, StatusConverted, reason)
}
