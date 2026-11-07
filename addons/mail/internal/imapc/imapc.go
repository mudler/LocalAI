// SPDX-License-Identifier: MIT

// Package imapc is the IMAP side of the Mail add-on. It reads with
// BODY.PEEK and EXAMINE only, so reading never marks mail as seen, and it
// writes one thing: a draft appended to the Drafts folder. It never deletes,
// moves, flags or expunges.
//
// A search is built as a structured criteria value and encoded by the IMAP
// library, which quotes strings and sends a literal for text that cannot be
// quoted, so model text is never joined into a command.
package imapc

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"

	"github.com/mudler/LocalAI/addons/mail/internal/account"
	"github.com/mudler/LocalAI/addons/mail/internal/dial"
)

// Errors. None holds text from the server.
var (
	ErrAuth       = errors.New("the mail server refused the username or password (some providers need an app password)")
	ErrSTARTTLS   = errors.New("the mail server does not offer STARTTLS, so the connection was not used")
	ErrServer     = errors.New("the mail server refused the request")
	ErrLost       = errors.New("the connection to the mail server was lost")
	ErrNoFolder   = errors.New("the folder does not exist")
	ErrNotAllowed = errors.New("the folder is not on the list of folders allowed for this connection")
	ErrNoMessage  = errors.New("there is no message with this UID in the folder")
	ErrNoPart     = errors.New("the message has no such part")
	ErrNoDrafts   = errors.New("the server names no Drafts folder: set the name of the drafts folder on the Mail page")
	ErrTooLarge   = errors.New("the part is too large to read")
	ErrNoUIDPlus  = errors.New("the server did not say where the draft was saved")
)

// Session is one logged-in connection. It is not safe for concurrent use.
type Session struct {
	c    *imapclient.Client
	acct account.Account
	stop func()
}

// Open connects, protects the connection as the account says, and logs in.
func Open(ctx context.Context, d *dial.Dialer, a account.Account) (*Session, error) {
	conn, err := d.Open(ctx, a.IMAP)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	stop := func() { close(done) }
	opts := &imapclient.Options{TLSConfig: d.TLSConfig(a.IMAP.Host)}
	var c *imapclient.Client
	if a.IMAP.Security == account.StartTLS {
		c, err = imapclient.NewStartTLS(conn, opts)
		if err != nil {
			stop()
			_ = conn.Close()
			return nil, mapErr(ctx, err, ErrSTARTTLS)
		}
	} else {
		c = imapclient.New(conn, opts)
	}
	s := &Session{c: c, acct: a, stop: stop}
	if err := s.login(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Session) login(ctx context.Context) error {
	var err error
	if s.c.Caps().Has(imap.Cap("AUTH=PLAIN")) {
		err = s.c.Authenticate(sasl.NewPlainClient("", s.acct.Username, s.acct.Password))
	} else {
		err = s.c.Login(s.acct.Username, s.acct.Password).Wait()
	}
	if err != nil {
		var ie *imap.Error
		if errors.As(err, &ie) {
			return ErrAuth
		}
		return mapErr(ctx, err, ErrLost)
	}
	return nil
}

// Close ends the session.
func (s *Session) Close() {
	_ = s.c.Close()
	s.stop()
}

// mapErr turns a library error into one of ours.
func mapErr(ctx context.Context, err error, fallback error) error {
	var (
		ie *imap.Error
		ne net.Error
	)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, dial.ErrTLS):
		return dial.ErrTLS
	case ctx.Err() != nil && !errors.Is(ctx.Err(), context.Canceled):
		return dial.ErrTimeout
	case errors.Is(err, os.ErrDeadlineExceeded), errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return dial.ErrTimeout
	case errors.As(err, &ie):
		switch ie.Code {
		case imap.ResponseCodeNonExistent, imap.ResponseCodeTryCreate:
			return ErrNoFolder
		case imap.ResponseCodeAuthenticationFailed:
			return ErrAuth
		}
		return ErrServer
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed):
		return ErrLost
	}
	return fallback
}

func (s *Session) check(folder string) error {
	if folder == "" || !s.acct.AllowsFolder(folder) {
		return ErrNotAllowed
	}
	return nil
}

// selectRO opens a folder read-only (EXAMINE), so nothing in it can change.
func (s *Session) selectRO(ctx context.Context, folder string) error {
	if err := s.check(folder); err != nil {
		return err
	}
	if _, err := s.c.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return mapErr(ctx, err, ErrServer)
	}
	return nil
}

// Folder is a mailbox of the server.
type Folder struct {
	Name string
	// Use is the SPECIAL-USE role without the backslash, lower case, or "".
	Use string
}

// Folders lists the folders that can hold mail and that the account allows.
func (s *Session) Folders(ctx context.Context) ([]Folder, error) {
	all, err := s.allFolders(ctx)
	if err != nil {
		return nil, err
	}
	var out []Folder
	for _, f := range all {
		if s.acct.AllowsFolder(f.Name) {
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *Session) allFolders(ctx context.Context) ([]Folder, error) {
	var opts *imap.ListOptions
	if s.c.Caps().Has(imap.CapListExtended) && s.c.Caps().Has(imap.CapSpecialUse) {
		opts = &imap.ListOptions{ReturnSpecialUse: true}
	}
	data, err := s.c.List("", "*", opts).Collect()
	if err != nil {
		return nil, mapErr(ctx, err, ErrServer)
	}
	var out []Folder
	for _, d := range data {
		skip := false
		use := ""
		for _, a := range d.Attrs {
			switch a {
			case imap.MailboxAttrNoSelect, imap.MailboxAttrNonExistent:
				skip = true
			case imap.MailboxAttrDrafts, imap.MailboxAttrSent, imap.MailboxAttrTrash, imap.MailboxAttrJunk, imap.MailboxAttrArchive, imap.MailboxAttrAll, imap.MailboxAttrFlagged:
				use = strings.ToLower(strings.TrimPrefix(string(a), "\\"))
			}
		}
		if !skip {
			out = append(out, Folder{Name: d.Mailbox, Use: use})
		}
	}
	return out, nil
}

// DraftsFolder finds the folder for drafts: the one the server marks \Drafts,
// else the configured name, else a folder called Drafts.
func (s *Session) DraftsFolder(ctx context.Context) (string, error) {
	all, err := s.allFolders(ctx)
	if err != nil {
		return "", err
	}
	for _, f := range all {
		if f.Use == "drafts" {
			return f.Name, s.check(f.Name)
		}
	}
	if want := s.acct.DraftsFolder; want != "" {
		for _, f := range all {
			if f.Name == want {
				return f.Name, s.check(f.Name)
			}
		}
		return "", ErrNoDrafts
	}
	for _, f := range all {
		if strings.EqualFold(f.Name, "Drafts") {
			return f.Name, s.check(f.Name)
		}
	}
	return "", ErrNoDrafts
}

// Addr is one address of a header.
type Addr struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

func addrs(in []imap.Address) []Addr {
	var out []Addr
	for _, a := range in {
		if v := a.Addr(); v != "" {
			out = append(out, Addr{Name: a.Name, Address: v})
		}
	}
	return out
}

// Query is the structured search of search-messages.
type Query struct {
	From, To, Subject, Text string
	Since, Before           time.Time
	Unseen, HasAttachment   bool
}

// Criteria is the IMAP search of the query.
func (q Query) Criteria() *imap.SearchCriteria {
	c := &imap.SearchCriteria{Since: q.Since, Before: q.Before}
	for _, h := range []struct{ k, v string }{{"From", q.From}, {"To", q.To}, {"Subject", q.Subject}} {
		if h.v != "" {
			c.Header = append(c.Header, imap.SearchCriteriaHeaderField{Key: h.k, Value: h.v})
		}
	}
	if q.Text != "" {
		c.Text = []string{q.Text}
	}
	if q.Unseen {
		c.NotFlag = []imap.Flag{imap.FlagSeen}
	}
	return c
}

// Summary is one line of a search.
type Summary struct {
	UID           uint32    `json:"uid"`
	MessageID     string    `json:"messageId,omitempty"`
	Subject       string    `json:"subject"`
	From          []Addr    `json:"from,omitempty"`
	To            []Addr    `json:"to,omitempty"`
	Date          time.Time `json:"date"`
	Size          int64     `json:"size"`
	Unseen        bool      `json:"unseen"`
	HasAttachment bool      `json:"hasAttachment"`
}

// Page is one page of a search, newest first.
type Page struct {
	Items []Summary
	// Total is how many messages matched the server-side search.
	Total int
	// Next is the cursor for the next page: ask for UIDs below it. Zero: no more.
	Next uint32
	// Scanned is how many messages were looked at for the attachment filter.
	Scanned int
}

// MaxScan bounds the messages examined to fill a page that filters on attachments.
const MaxScan = 500

// Search runs the query in a folder and returns a page, newest UID first.
// cursor is the UID the page starts below; zero starts at the newest.
func (s *Session) Search(ctx context.Context, folder string, q Query, cursor uint32, limit int) (Page, error) {
	if err := s.selectRO(ctx, folder); err != nil {
		return Page{}, err
	}
	data, err := s.c.UIDSearch(q.Criteria(), nil).Wait()
	if err != nil {
		return Page{}, mapErr(ctx, err, ErrServer)
	}
	uids := data.AllUIDs()
	// Newest first.
	for i, j := 0, len(uids)-1; i < j; i, j = i+1, j-1 {
		uids[i], uids[j] = uids[j], uids[i]
	}
	page := Page{Total: len(uids)}
	rest := uids
	if cursor > 0 {
		for len(rest) > 0 && uint32(rest[0]) >= cursor {
			rest = rest[1:]
		}
	}
	const batch = 50
	var last uint32 // the newest-first position reached: the next page starts below it
	for len(rest) > 0 && len(page.Items) < limit && page.Scanned < MaxScan {
		n := min(batch, len(rest), MaxScan-page.Scanned)
		if !q.HasAttachment {
			n = min(n, limit-len(page.Items))
		}
		chunk := rest[:n]
		rest = rest[n:]
		page.Scanned += n
		items, err := s.summaries(ctx, chunk)
		if err != nil {
			return Page{}, err
		}
		last = uint32(chunk[len(chunk)-1])
		for _, it := range items {
			if q.HasAttachment && !it.HasAttachment {
				continue
			}
			if len(page.Items) == limit {
				// The chunk had more matches than the page holds: continue
				// right after the last one that was returned.
				last = page.Items[len(page.Items)-1].UID
				rest = append(append([]imap.UID(nil), chunk...), rest...)
				for len(rest) > 0 && uint32(rest[0]) >= last {
					rest = rest[1:]
				}
				break
			}
			page.Items = append(page.Items, it)
		}
	}
	if len(rest) > 0 {
		page.Next = last
	}
	return page, nil
}

func uidSet(uids []imap.UID) imap.UIDSet {
	var set imap.UIDSet
	for _, u := range uids {
		set.AddNum(u)
	}
	return set
}

func (s *Session) summaries(ctx context.Context, uids []imap.UID) ([]Summary, error) {
	bufs, err := s.c.Fetch(uidSet(uids), &imap.FetchOptions{UID: true, Envelope: true, Flags: true, RFC822Size: true, BodyStructure: &imap.FetchItemBodyStructure{}}).Collect()
	if err != nil {
		return nil, mapErr(ctx, err, ErrServer)
	}
	out := make([]Summary, 0, len(bufs))
	for _, b := range bufs {
		it := Summary{UID: uint32(b.UID), Size: b.RFC822Size, Unseen: true}
		for _, f := range b.Flags {
			if f == imap.FlagSeen {
				it.Unseen = false
			}
		}
		if e := b.Envelope; e != nil {
			it.MessageID, it.Subject, it.Date = e.MessageID, e.Subject, e.Date
			it.From, it.To = addrs(e.From), addrs(e.To)
		}
		if b.BodyStructure != nil {
			for _, p := range Parts(b.BodyStructure) {
				if p.Role == RoleAttachment {
					it.HasAttachment = true
				}
			}
		}
		out = append(out, it)
	}
	// Newest first, as asked.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].UID > out[j-1].UID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// Uint formats a UID.
func Uint(u uint32) string { return strconv.FormatUint(uint64(u), 10) }
