package convert

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// MaxBrowserChats is how many chats one upload of browser chats may hold.
const MaxBrowserChats = 1000

// ErrBrowserForm means the upload is not what the old chat page kept in the
// browser.
var ErrBrowserForm = errors.New("convert: not the chat store of the old page")

// BrowserChat is one chat from the browser store of the old page, ready to
// import.
type BrowserChat struct {
	Title, Model     string
	Created, Updated time.Time
	Entries          []EntryIn
}

// ParseBrowserChats reads the value the old page kept in localStorage under
// "localai_chats_data": {"chats": [{"id", "name", "model", "history",
// "createdAt", "updatedAt", ...}], ...}. It returns the chats that can be
// imported and the number that cannot: a chat with no creation date (the date is
// what tells a second upload that a chat is there already) or with no message
// the new chat shows. Messages are read as the conversion of the saved
// conversations reads them.
func ParseBrowserChats(data []byte) ([]BrowserChat, int, error) {
	var store struct {
		Chats []oldConversation `json:"chats"`
	}
	if err := json.Unmarshal(data, &store); err != nil || store.Chats == nil {
		return nil, 0, ErrBrowserForm
	}
	if len(store.Chats) > MaxBrowserChats {
		return nil, 0, ErrBrowserForm
	}
	var out []BrowserChat
	unusable := 0
	for _, c := range store.Chats {
		if c.CreatedAt <= 0 {
			unusable++
			continue
		}
		created := time.UnixMilli(c.CreatedAt).UTC()
		updated := created
		if c.UpdatedAt > c.CreatedAt {
			updated = time.UnixMilli(c.UpdatedAt).UTC()
		}
		entries := entriesOf(c.History, created)
		if len(entries) == 0 {
			unusable++
			continue
		}
		title := strings.TrimSpace(c.Name)
		if title == "" {
			title = "Imported chat"
		}
		out = append(out, BrowserChat{Title: title, Model: c.Model, Created: created, Updated: updated, Entries: entries})
	}
	return out, unusable, nil
}
