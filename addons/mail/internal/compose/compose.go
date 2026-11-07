// SPDX-License-Identifier: MIT

// Package compose builds the mail messages the add-on sends or saves as
// drafts. Everything that goes into a header is checked first: a value with a
// line break would let a model (or the text of a mail it read) add headers
// such as Bcc, so such a value is refused, never cleaned.
package compose

import (
	"bytes"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/mail/internal/account"
)

// Limits of one message.
const (
	MaxRecipients  = 20
	MaxSubjectRune = 255
	MaxBodyBytes   = 64 << 10
	MaxReferences  = 20
	MaxIDBytes     = 255
)

// ErrInvalid is wrapped by every refusal. The text names the field, never its value.
var ErrInvalid = errors.New("invalid message")

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Message is what a send or a draft carries.
type Message struct {
	FromName, FromAddress string
	To, Cc                []string
	Subject, Body         string
	// InReplyTo is the Message-ID of the message answered, References the IDs
	// of its thread, both without angle brackets.
	InReplyTo  string
	References []string
	// MessageID is the ID of this message without angle brackets.
	MessageID string
	Date      time.Time
}

var idShape = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+/=?^_{|}~.-]+@[A-Za-z0-9.\[\]:-]+$`)

// ValidID accepts a Message-ID without angle brackets.
func ValidID(id string) bool {
	return id != "" && len(id) <= MaxIDBytes && idShape.MatchString(id)
}

// Check validates the message without building it.
func (m Message) Check() error {
	if len(m.To) == 0 {
		return bad("to needs at least one address")
	}
	if len(m.To)+len(m.Cc) > MaxRecipients {
		return bad("at most %d recipients in to and cc together", MaxRecipients)
	}
	seen := map[string]bool{}
	for _, list := range [][]string{m.To, m.Cc} {
		for _, a := range list {
			if account.ValidAddress(a) != nil {
				return bad("an address in to or cc is not a plain address like name@example.com")
			}
			if seen[strings.ToLower(a)] {
				return bad("an address is listed twice")
			}
			seen[strings.ToLower(a)] = true
		}
	}
	if account.ValidAddress(m.FromAddress) != nil {
		return bad("the sender address of the connection")
	}
	if strings.TrimSpace(m.Subject) == "" || utf8.RuneCountInString(m.Subject) > MaxSubjectRune || hasControl(m.Subject) || !utf8.ValidString(m.Subject) {
		return bad("subject has 1 to %d characters on one line", MaxSubjectRune)
	}
	if hasControl(m.FromName) || utf8.RuneCountInString(m.FromName) > account.MaxNameRunes {
		return bad("the sender name of the connection")
	}
	if strings.TrimSpace(m.Body) == "" {
		return bad("body is required")
	}
	if len(m.Body) > MaxBodyBytes || !utf8.ValidString(m.Body) || strings.ContainsRune(m.Body, 0) {
		return bad("body is longer than %d bytes or not text", MaxBodyBytes)
	}
	if m.InReplyTo != "" && !ValidID(m.InReplyTo) {
		return bad("inReplyTo is not a Message-ID")
	}
	if len(m.References) > MaxReferences {
		return bad("at most %d references", MaxReferences)
	}
	for _, r := range m.References {
		if !ValidID(r) {
			return bad("a reference is not a Message-ID")
		}
	}
	if !ValidID(m.MessageID) {
		return bad("message id")
	}
	return nil
}

// hasControl reports a control character, which includes CR and LF.
func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return true
		}
	}
	return false
}

// Build returns the message as bytes with CRLF line ends: plain text only,
// UTF-8, quoted-printable. There is no HTML part and no attachment.
func Build(m Message) ([]byte, error) {
	if err := m.Check(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	from := (&mail.Address{Name: m.FromName, Address: m.FromAddress}).String()
	h("From", from)
	h("To", joinAddrs(m.To))
	if len(m.Cc) > 0 {
		h("Cc", joinAddrs(m.Cc))
	}
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	d := m.Date
	if d.IsZero() {
		d = time.Now()
	}
	h("Date", d.Format(time.RFC1123Z))
	h("Message-ID", "<"+m.MessageID+">")
	if m.InReplyTo != "" {
		h("In-Reply-To", "<"+m.InReplyTo+">")
	}
	if len(m.References) > 0 {
		refs := make([]string, len(m.References))
		for i, r := range m.References {
			refs[i] = "<" + r + ">"
		}
		h("References", strings.Join(refs, " "))
	}
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("Content-Transfer-Encoding", "quoted-printable")
	b.WriteString("\r\n")
	body := strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\r", "\n")
	body = strings.TrimRight(body, "\n") + "\n"
	w := quotedprintable.NewWriter(&b)
	w.Binary = false
	if _, err := w.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n"))); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func joinAddrs(l []string) string { return strings.Join(l, ", ") }
