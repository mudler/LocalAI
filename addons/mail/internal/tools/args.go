// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/internal/protocol"
)

// bad is a refusal of the request with a short, fixed reason. The reason
// reaches the model, so it never holds text from a mail or from the account.
func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", protocol.ErrBadRequest, fmt.Sprintf(format, a...))
}

// decodeArgs reads the arguments of a tool, less the tool name, into v. An
// unknown field is a refusal.
func decodeArgs(raw map[string]json.RawMessage, v any) error {
	b, _ := json.Marshal(raw)
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return bad("the arguments do not match the tool")
	}
	return nil
}

// readArgs holds the arguments of every read tool; a tool uses its own.
type readArgs struct {
	Mailbox       string `json:"mailbox"`
	Folder        string `json:"folder"`
	UID           uint32 `json:"uid"`
	Part          string `json:"part"`
	Offset        int    `json:"offset"`
	From          string `json:"from"`
	To            string `json:"to"`
	Subject       string `json:"subject"`
	Text          string `json:"text"`
	Since         string `json:"since"`
	Before        string `json:"before"`
	Unseen        bool   `json:"unseen"`
	HasAttachment bool   `json:"hasAttachment"`
	Cursor        uint32 `json:"cursor"`
	Limit         int    `json:"limit"`
}

// writeArgs holds the arguments of both write tools.
type writeArgs struct {
	Mailbox    string   `json:"mailbox"`
	To         []string `json:"to"`
	Cc         []string `json:"cc"`
	Subject    string   `json:"subject"`
	Body       string   `json:"body"`
	InReplyTo  string   `json:"inReplyTo"`
	References []string `json:"references"`
}

// searchText accepts text for a search: valid UTF-8, bounded, and no control
// character except tab and line breaks, which the IMAP library sends as a
// literal.
func searchText(what, s string) error {
	if utf8.RuneCountInString(s) > 200 || !utf8.ValidString(s) {
		return bad("%s has at most 200 characters", what)
	}
	for _, r := range s {
		if (r < 0x20 && r != '\t' && r != '\r' && r != '\n') || r == 0x7f {
			return bad("%s has a control character", what)
		}
	}
	return nil
}

func parseDay(what, s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, bad("%s is a date as YYYY-MM-DD", what)
	}
	return t, nil
}

// cut shortens s to at most max bytes at a character boundary.
func cut(s string, max int) (string, int) {
	if len(s) <= max {
		return s, 0
	}
	n := max
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], len(s) - n
}
