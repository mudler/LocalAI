package studio

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// This file keeps what the owner reads about a connector chat in the web
// interface: who wrote each message and how the host saw them, where the chat
// is on the platform, and what the host did on its own. All of it is display
// data taken from the message as it came in. Nothing here reads the platform
// again, and nothing changes what the model gets.

// PlaceInfo describes where a connector chat lives on the platform, for the
// header of the chat. Every field is optional: an add-on that does not tell a
// channel name or a link leaves them empty and the header shows less.
type PlaceInfo struct {
	// Direct is true for a conversation with one person.
	Direct bool `json:"direct,omitempty"`
	// Thread is true when the chat belongs to a thread of the platform.
	Thread bool `json:"thread,omitempty"`
	// Name is the channel or group name the add-on gave.
	Name string `json:"name,omitempty"`
	// Link opens the thread or message on the platform. It is an https
	// address the add-on built; the host never builds one.
	Link string `json:"link,omitempty"`
}

const (
	infoKeyPrefix = "pi:"
	linkKeyPrefix = "pl:"
	// maxPlaceName and maxPlaceLink keep the stored value inside the width of
	// the chat column of the stores (maxPlaceValue).
	maxPlaceName = 60
	maxPlaceLink = maxPlaceValue
)

func infoKey(chatID string) string { return infoKeyPrefix + chatID }
func linkKey(chatID string) string { return linkKeyPrefix + chatID }

// safeLink returns link when it is a plain https address that fits the
// store, and "" otherwise. A link that is not https, carries a login, or is
// too long is dropped: the header then shows no link rather than a doubtful
// one.
func safeLink(link string) string {
	link = strings.TrimSpace(link)
	if link == "" || len(link) > maxPlaceLink || !utf8.ValidString(link) {
		return ""
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	for _, r := range link {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return ""
		}
	}
	return link
}

// rememberInfo keeps the place information of the chat chatID, once: the
// first message that makes the chat is the thread root or the first words of
// the person, and later messages do not move it. A value that did not change is
// not written again.
func (c *Connectors) rememberInfo(ctx context.Context, b connectors.Binding, chatID string, m contract.ConnectorMessage) {
	info := PlaceInfo{Direct: m.Direct, Thread: m.Thread != "", Name: cutRunes(flatten(m.ChatName), maxPlaceName)}
	raw, err := json.Marshal(info)
	if err != nil || len(raw) > maxPlaceValue {
		raw, _ = json.Marshal(PlaceInfo{Direct: info.Direct, Thread: info.Thread})
	}
	if cur, ok, err := c.o.Store.ChatOf(ctx, b.ID, infoKey(chatID)); err != nil || !ok || cur != string(raw) {
		if err := c.o.Store.SetChat(ctx, b.ID, infoKey(chatID), string(raw)); err != nil {
			c.log.Warn("studio: could not keep the place information of a connector chat", "binding", b.ID, "error", err)
		}
	}
	if link := safeLink(m.Link); link != "" {
		if _, ok, err := c.o.Store.ChatOf(ctx, b.ID, linkKey(chatID)); err == nil && !ok {
			if err := c.o.Store.SetChat(ctx, b.ID, linkKey(chatID), link); err != nil {
				c.log.Warn("studio: could not keep the link of a connector chat", "binding", b.ID, "error", err)
			}
		}
	}
}

// InfoOf returns the place information of a connector chat of a binding. It is
// false for a chat made before the information was kept.
func (c *Connectors) InfoOf(ctx context.Context, bindingID, chatID string) (PlaceInfo, bool) {
	var info PlaceInfo
	v, ok, err := c.o.Store.ChatOf(ctx, bindingID, infoKey(chatID))
	if err != nil || !ok || json.Unmarshal([]byte(v), &info) != nil {
		return PlaceInfo{}, false
	}
	if l, ok, err := c.o.Store.ChatOf(ctx, bindingID, linkKey(chatID)); err == nil && ok {
		info.Link = safeLink(l)
	}
	return info, true
}

// standingOf is how the host sees the sender of m: the first of owner (paired
// with the bot), verified (holds a role that the kind declares), listed (on
// the allowlist itself), channel (let in because its room is listed) and
// anyone (the connection accepts everyone).
func (c *Connectors) standingOf(b connectors.Binding, m contract.ConnectorMessage) string {
	switch {
	case m.SenderID != "" && b.IsPaired(m.SenderID, c.o.Clock()):
		return session.StandingOwner
	case len(m.Roles) > 0:
		return session.StandingVerified
	case b.Allows(m.SenderID, m.SenderUsername):
		return session.StandingListed
	case !m.Direct && b.Allows(m.Chat, ""):
		return session.StandingChannel
	}
	return session.StandingAnyone
}

// senderFor is the sender the transcript names for m. The name is the one the
// add-on resolved; the handle is the fallback, and with neither the interface
// shows the platform ID in small print.
func (c *Connectors) senderFor(b connectors.Binding, m contract.ConnectorMessage) *session.Sender {
	return &session.Sender{
		ID:       m.SenderID,
		Name:     cutRunes(flatten(m.SenderName), threadNameRunes),
		Username: cutRunes(flatten(m.SenderUsername), threadNameRunes),
		Standing: c.standingOf(b, m),
	}
}

// noteChat records, in the chat that the message m belongs to, something the
// host did on its own about it. It writes only to a chat that exists: a message
// that never made a chat leaves nothing to show. text is the sentence the
// sender was told, if any. It never blocks the message and only logs a failure.
func (c *Connectors) noteChat(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, code, text string) {
	id, ok, err := c.o.Store.ChatOf(ctx, b.ID, c.conversationOf(ctx, b, m))
	if err != nil || !ok {
		return
	}
	if err := c.svc.NoteEntry(ctx, b.Owner, id, session.Entry{Role: session.RoleHostNote, Note: code, Text: cutRunes(text, 400)}); err != nil {
		c.log.Warn("studio: could not note a host action in a connector chat", "binding", b.ID, "error", err)
	}
}

// NoteEntry appends a display-only line to the transcript of a chat of user:
// a line the model never reads. Only the chat's owner gets one; the line is
// never sent to the model.
func (s *Service) NoteEntry(ctx context.Context, user, conv string, e session.Entry) error {
	if e.Role != session.RoleHostNote && e.Role != session.RoleThread {
		return session.ErrInvalid
	}
	if _, err := s.Get(ctx, user, conv); err != nil {
		return err
	}
	_, err := s.d.Store.AppendEntry(ctx, conv, e)
	return err
}

// noteHeldBack shows the owner, in the chat, the line that told the model which
// tools the sender may not use and why (deniedToolsNote). The reasons are
// written for people: they name what the owner can change. A process notes a
// line once for a chat, not at every turn.
func (s *Service) noteHeldBack(ctx context.Context, rec session.ChatRecord, line string) {
	reasons := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, deniedNoteStart), deniedNoteEnd))
	if reasons == "" {
		return
	}
	if last, ok := s.heldNotes.Load(rec.ID); ok && last == reasons {
		return
	}
	s.heldNotes.Store(rec.ID, reasons)
	e := session.Entry{Role: session.RoleHostNote, Note: session.HostNoteHeldBack, Text: reasons}
	if _, err := s.d.Store.AppendEntry(context.WithoutCancel(ctx), rec.ID, e); err != nil {
		s.log.Warn("studio: could not note the tools held back in a connector chat", "chat", rec.ID, "error", err)
	}
}
