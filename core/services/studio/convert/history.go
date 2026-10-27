package convert

import (
	"context"
	"encoding/json"
	"strings"
	"time"
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
			out = append(out, EntryIn{Role: "tool", Text: "called " + m.Name, At: at})
		case "tool_result":
			line := "result of " + m.Name
			if text != "" {
				if len(text) > 500 {
					text = text[:500] + "…"
				}
				line += ": " + text
			}
			// A result joins the call before it, so one tool use is one line.
			if n := len(out); n > 0 && out[n-1].Role == "tool" && out[n-1].Text == "called "+m.Name {
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

func (r *run) historyStep(ctx context.Context) error {
	if !hasTable(r.src.DB, "chat_conversations") {
		return nil
	}
	var rows []struct {
		UserID  string `gorm:"column:user_id"`
		ConvID  string `gorm:"column:conv_id"`
		Content string `gorm:"column:content"`
	}
	if err := r.src.DB.Table("chat_conversations").Where("deleted_at IS NULL").Order("user_id, conv_id").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var c oldConversation
		if json.Unmarshal([]byte(row.Content), &c) != nil {
			r.add(KindChat, row.UserID, row.ConvID, StatusDropped, "the conversation cannot be read")
			continue
		}
		title := strings.TrimSpace(c.Name)
		if title == "" {
			title = "Imported chat"
		}
		owner, why := r.owner(ctx, row.UserID)
		if why != "" {
			r.add(KindChat, row.UserID, title, StatusSkipped, why)
			continue
		}
		key := "chat|" + row.UserID + "|" + row.ConvID
		if _, done := r.done(ctx, key); done {
			r.add(KindChat, row.UserID, title, StatusSkipped, "already converted")
			continue
		}
		created, updated := time.UnixMilli(c.CreatedAt).UTC(), time.UnixMilli(c.UpdatedAt).UTC()
		if c.CreatedAt == 0 {
			created = time.Now().UTC()
		}
		if c.UpdatedAt == 0 {
			updated = created
		}
		entries := entriesOf(c.History, created)
		if err := r.tg.ImportChat(ctx, owner, ChatIn{Title: title, Model: c.Model, Created: created, Updated: updated, Entries: entries}); err != nil {
			r.add(KindChat, row.UserID, title, StatusFailed, err.Error())
			continue
		}
		r.mark(ctx, key, "chat")
		r.add(KindChat, row.UserID, title, StatusConverted, "")
	}
	return nil
}
