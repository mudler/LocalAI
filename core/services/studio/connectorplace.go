package studio

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The place of a connector chat is where the platform shows it: the chat and
// the thread of the last message the bot answered. A tool that runs in the
// turn, and the host when a run reports back, need it to write to the same
// place. A connector chat is made for a sender or a thread, not for one
// platform chat, so the host keeps the place of each chat next to the chat
// mapping, under a key that cannot be the ID of a sender. Nothing in the
// store changes: the chat column holds "chat\x1fthread".
const (
	placeKeyPrefix = "place:"
	placeSep       = "\x1f"
	// maxPlaceValue is the width of the chat column of the stores.
	maxPlaceValue = 128
)

func placeKey(chatID string) string { return placeKeyPrefix + chatID }

func encodePlace(chat, thread string) string { return chat + placeSep + thread }

func decodePlace(v string) (chat, thread string, ok bool) {
	chat, thread, ok = strings.Cut(v, placeSep)
	return chat, thread, ok && chat != ""
}

// rememberPlace keeps where the message m was written, for the chat chatID of
// binding b. A place that does not fit the store is not kept: the chat then
// has no place, and what needs one falls back to the Inbox.
func (c *Connectors) rememberPlace(ctx context.Context, b connectors.Binding, chatID string, m contract.ConnectorMessage) {
	key, val := placeKey(chatID), encodePlace(m.Chat, m.Thread)
	if len(key) > contract.MaxConnectorSender || len(val) > maxPlaceValue {
		return
	}
	if cur, ok, err := c.o.Store.ChatOf(ctx, b.ID, key); err == nil && ok && cur == val {
		return
	}
	if err := c.o.Store.SetChat(ctx, b.ID, key, val); err != nil {
		c.log.Warn("studio: could not keep the place of a connector chat", "binding", b.ID, "error", err)
	}
}

// PlaceOf returns the platform chat and thread where the connector chat chatID
// of the binding was last written to. It is false when the chat has none.
func (c *Connectors) PlaceOf(ctx context.Context, bindingID, chatID string) (chat, thread string, ok bool) {
	v, found, err := c.o.Store.ChatOf(ctx, bindingID, placeKey(chatID))
	if err != nil || !found {
		return "", "", false
	}
	return decodePlace(v)
}
