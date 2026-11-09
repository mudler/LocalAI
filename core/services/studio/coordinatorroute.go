package studio

import (
	"context"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// A reply of a contact reaches the coordinator through the binding that
// carried the message to it. The binding keeps one chat for each sender (or for
// each chat). When the sender or the chat is an external contact of a
// coordinator of the owner of the binding, the message goes to the chat of that
// coordinator instead. The lookup is read only: it never writes the chat map
// of the binding, so a coordinator that is deleted leaves nothing behind.

// contactKeyOf is the key of the conversation of a contact on a binding, the
// key that conversationOf gives to a message of that sender or chat.
func contactKeyOf(b connectors.Binding, chat, sender string) string {
	if b.ConversationMode() == connectors.ConversationChat {
		return hashKey("chat:", chat)
	}
	if sender != "" {
		return sender
	}
	return chat
}

// messageKey is the key a message would have as a contact reply. A thread of
// the platform does not matter here: the contact is the sender or the chat.
func messageKey(b connectors.Binding, m contract.ConnectorMessage) string {
	return contactKeyOf(b, m.Chat, m.SenderID)
}

// externalOn lists the external contacts, on the binding, of the coordinators
// of the binding's owner, as (config, contact) pairs in a stable order.
func (h *Coordinators) externalOn(ctx context.Context, b connectors.Binding) ([]coordinator.Config, error) {
	cfgs, err := h.o.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(cfgs, func(i, j int) bool {
		if !cfgs[i].Created.Equal(cfgs[j].Created) {
			return cfgs[i].Created.Before(cfgs[j].Created)
		}
		return cfgs[i].AgentID < cfgs[j].AgentID
	})
	var out []coordinator.Config
	for _, c := range cfgs {
		if c.Principal != b.Owner {
			continue
		}
		for _, ct := range c.Contacts {
			if ct.Kind == coordinator.ContactExternal && ct.Binding == b.ID {
				out = append(out, c)
				break
			}
		}
	}
	return out, nil
}

// find returns the coordinator and the contact that the message m is a reply
// of, on b. A contact is the sender in the chat the creator listed: the same
// sender in another chat (a group the bot is in) is not it, or the answer of
// the turn would go to a channel that nobody listed. When two coordinators list
// the same contact, the older one gets the reply.
func (h *Coordinators) find(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage) (coordinator.Config, coordinator.Contact, bool) {
	key := messageKey(b, m)
	cfgs, err := h.externalOn(ctx, b)
	if err != nil {
		h.log.Warn("studio: could not read the coordinators to route a reply", "binding", b.ID, "error", err)
		return coordinator.Config{}, coordinator.Contact{}, false
	}
	for _, c := range cfgs {
		for _, ct := range c.Contacts {
			if ct.Kind == coordinator.ContactExternal && ct.Binding == b.ID && contactKeyOf(b, ct.Chat, ct.Sender) == key && ct.Chat == m.Chat {
				return c, ct, true
			}
		}
	}
	return coordinator.Config{}, coordinator.Contact{}, false
}

// RouteOf returns the chat of the coordinator of the owner of b whose external
// contact sent m (see find). The chat is made again when it was deleted. ok is
// false when no coordinator lists that contact: the message then follows the
// normal path of the binding.
func (h *Coordinators) RouteOf(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage) (chatID string, ok bool) {
	if h == nil || messageKey(b, m) == "" {
		return "", false
	}
	cfg, _, found := h.find(ctx, b, m)
	if !found {
		return "", false
	}
	rec, err := h.EnsureChat(ctx, cfg)
	if err != nil {
		h.log.Warn("studio: could not get the chat of a coordinator to route a reply", "coordinator", cfg.AgentID, "error", err)
		return "", false
	}
	return rec.ID, true
}

// Frame is the text the coordinator reads for a message that RouteOf routed to
// chatID: it names the contact. A sender that is not a listed contact (it
// cannot be, for a message that was routed, but the text is built for any
// caller) is named as such, so the model never reads it as a contact.
func (h *Coordinators) Frame(ctx context.Context, b connectors.Binding, m contract.ConnectorMessage, chatID string) string {
	by := cleanName(m.SenderName)
	if by == "" {
		by = cleanName(m.SenderUsername)
	}
	cfgs, _ := h.externalOn(ctx, b)
	for _, c := range cfgs {
		if c.ChatID != chatID {
			continue
		}
		for _, ct := range c.Contacts {
			if ct.Kind != coordinator.ContactExternal || ct.Binding != b.ID {
				continue
			}
			if contactKeyOf(b, ct.Chat, ct.Sender) != messageKey(b, m) {
				continue
			}
			head := "Reply from " + cleanName(ct.Name)
			// A team is a chat: say who in it wrote.
			if b.ConversationMode() == connectors.ConversationChat && by != "" {
				head += ", written by " + by
			}
			return "[" + head + "]\n" + m.Text
		}
	}
	if by == "" {
		by = "an unknown sender"
	}
	return "[Message from " + by + ", not a listed contact]\n" + m.Text
}

// cleanName makes a name safe to put in the bracket line of a frame: no
// control characters, no closing bracket, at most 80 runes.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ']' || r == '[' {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	for utf8.RuneCountInString(s) > 80 {
		_, n := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-n]
	}
	return s
}
