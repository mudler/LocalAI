// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/mail/internal/dial"
	"github.com/mudler/LocalAI/addons/mail/internal/imapc"
	"github.com/mudler/LocalAI/addons/mail/internal/mimetext"
	"github.com/mudler/LocalAI/addons/mail/internal/smtpc"
)

func (c call) open() (*imapc.Session, error) {
	return imapc.Open(c.ctx, c.r.Dialer, c.acct)
}

func (c call) folder() error {
	f := c.args.Folder
	if f == "" || len(f) > 200 || !utf8.ValidString(f) || strings.ContainsAny(f, "\r\n\x00") {
		return bad("folder is required")
	}
	if !c.acct.AllowsFolder(f) {
		return bad("%s", imapc.ErrNotAllowed.Error())
	}
	return nil
}

type folderJSON struct {
	Name string `json:"name"`
	Use  string `json:"use,omitempty"`
}

func (c call) listFolders() (any, error) {
	s, err := c.open()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	fs, err := s.Folders(c.ctx)
	if err != nil {
		return nil, err
	}
	out := []folderJSON{}
	for _, f := range fs {
		if len(out) == 100 {
			break
		}
		out = append(out, folderJSON{Name: mimetext.Line(f.Name, 200), Use: f.Use})
	}
	return map[string]any{"mailbox": c.acct.Address, "folders": out, "count": len(out)}, nil
}

type addrJSON struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

func addrList(in []imapc.Addr, max int) []addrJSON {
	var out []addrJSON
	for _, a := range in {
		if len(out) == max {
			break
		}
		out = append(out, addrJSON{Name: mimetext.Line(a.Name, 100), Address: mimetext.Line(a.Address, 254)})
	}
	return out
}

type summaryJSON struct {
	UID           uint32     `json:"uid"`
	MessageID     string     `json:"messageId,omitempty"`
	Subject       string     `json:"subject"`
	From          []addrJSON `json:"from,omitempty"`
	To            []addrJSON `json:"to,omitempty"`
	Date          string     `json:"date,omitempty"`
	Size          int64      `json:"size"`
	Unseen        bool       `json:"unseen"`
	HasAttachment bool       `json:"hasAttachment"`
}

func day(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func summaries(in []imapc.Summary) []summaryJSON {
	out := make([]summaryJSON, 0, len(in))
	for _, s := range in {
		out = append(out, summaryJSON{UID: s.UID, MessageID: mimetext.Line(s.MessageID, 255), Subject: mimetext.Line(s.Subject, 300),
			From: addrList(s.From, 3), To: addrList(s.To, 5), Date: day(s.Date), Size: s.Size, Unseen: s.Unseen, HasAttachment: s.HasAttachment})
	}
	return out
}

func (c call) search() (any, error) {
	a := c.args
	if err := c.folder(); err != nil {
		return nil, err
	}
	for _, f := range []struct{ n, v string }{{"from", a.From}, {"to", a.To}, {"subject", a.Subject}, {"text", a.Text}} {
		if err := searchText(f.n, f.v); err != nil {
			return nil, err
		}
	}
	since, err := parseDay("since", a.Since)
	if err != nil {
		return nil, err
	}
	before, err := parseDay("before", a.Before)
	if err != nil {
		return nil, err
	}
	if !since.IsZero() && !before.IsZero() && !since.Before(before) {
		return nil, bad("since is not before before")
	}
	limit := a.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return nil, bad("limit is 1 to %d", MaxLimit)
	}
	s, err := c.open()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	p, err := s.Search(c.ctx, a.Folder, imapc.Query{From: a.From, To: a.To, Subject: a.Subject, Text: a.Text, Since: since, Before: before, Unseen: a.Unseen, HasAttachment: a.HasAttachment}, a.Cursor, limit)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"folder": a.Folder, "total": p.Total, "count": len(p.Items), "messages": summaries(p.Items)}
	if p.Next != 0 {
		out["next"] = p.Next
	}
	if a.HasAttachment {
		out["scanned"] = p.Scanned
		out["note"] = "The attachment filter looks at the newest messages first, up to 500 per call; use next to go on."
	}
	return out, nil
}

type attachmentJSON struct {
	Part string `json:"part"`
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
	Size int64  `json:"size"`
	Text bool   `json:"text"`
}

func (c call) getMessage() (any, error) {
	if err := c.folder(); err != nil {
		return nil, err
	}
	if c.args.UID < 1 {
		return nil, bad("uid is required")
	}
	s, err := c.open()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	m, err := s.Get(c.ctx, c.args.Folder, c.args.UID)
	if err != nil {
		return nil, err
	}
	body, omitted := cut(m.Body, MaxBodyBytes)
	atts := make([]attachmentJSON, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		if len(atts) == maxListed {
			break
		}
		atts = append(atts, attachmentJSON{Part: a.Part, Name: mimetext.Line(a.Name, 200), Type: mimetext.Line(a.Type, 100), Size: a.Size, Text: a.Text})
	}
	out := map[string]any{
		"folder": c.args.Folder, "uid": m.UID, "messageId": mimetext.Line(m.MessageID, 255), "subject": mimetext.Line(m.Subject, 300),
		"from": addrList(m.From, 5), "to": addrList(m.To, 20), "cc": addrList(m.Cc, 20), "date": day(m.Date),
		"unseen": m.Unseen, "body": body, "attachments": atts,
	}
	if len(m.InReplyTo) > 0 {
		out["inReplyTo"] = clean(m.InReplyTo)
	}
	if len(m.References) > 0 {
		out["references"] = clean(m.References)
	}
	if omitted > 0 || m.PartCut {
		out["bodyCut"] = true
		out["bodyBytesOmitted"] = omitted
	}
	if m.Converted {
		out["bodyFromHtml"] = true
	}
	if len(m.Attachments) > maxListed {
		out["attachmentsOmitted"] = len(m.Attachments) - maxListed
	}
	return out, nil
}

func clean(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, mimetext.Line(id, 255))
	}
	return out
}

func (c call) getThread() (any, error) {
	if err := c.folder(); err != nil {
		return nil, err
	}
	if c.args.UID < 1 {
		return nil, bad("uid is required")
	}
	s, err := c.open()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	th, err := s.Thread(c.ctx, c.args.Folder, c.args.UID, maxListed)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"folder": c.args.Folder, "count": len(th), "messages": summaries(th),
		"note": "Only messages in this folder are found, by Message-ID, References and In-Reply-To. Search the other folders for the rest."}
	return out, nil
}

var partShape = regexp.MustCompile(`^[1-9][0-9]{0,2}(\.[1-9][0-9]{0,2}){0,7}$`)

func (c call) readAttachment() (any, error) {
	if err := c.folder(); err != nil {
		return nil, err
	}
	if c.args.UID < 1 {
		return nil, bad("uid is required")
	}
	if !partShape.MatchString(c.args.Part) {
		return nil, bad("part is a part number from get-message, such as 2 or 1.2")
	}
	if c.args.Offset < 0 {
		return nil, bad("offset is 0 or more")
	}
	s, err := c.open()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	p, err := s.ReadPart(c.ctx, c.args.Folder, c.args.UID, c.args.Part)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"folder": c.args.Folder, "uid": c.args.UID, "part": c.args.Part, "name": mimetext.Line(p.Name, 200), "type": mimetext.Line(p.Type, 100), "size": p.Size}
	if p.Binary {
		out["binary"] = true
		out["note"] = "This is not a text type, so its content is not shown."
		return out, nil
	}
	if c.args.Offset > len(p.Text) {
		return nil, bad("offset is past the end of the text (%d bytes)", len(p.Text))
	}
	// Start at a character boundary, then take at most MaxAttachmentBytes,
	// fewer when the escaped JSON would not fit the result.
	start := c.args.Offset
	for start < len(p.Text) && !utf8.RuneStart(p.Text[start]) {
		start++
	}
	n := MaxAttachmentBytes
	for {
		chunk, _ := cut(p.Text[start:], n)
		out["text"], out["offset"], out["totalBytes"] = chunk, start, len(p.Text)
		if next := start + len(chunk); next < len(p.Text) {
			out["next"], out["bytesLeft"] = next, len(p.Text)-next
		} else {
			delete(out, "next")
			delete(out, "bytesLeft")
		}
		if p.Converted {
			out["fromHtml"] = true
		}
		b, _ := json.Marshal(out)
		if len(b) <= MaxPayload-1024 || n < 1024 {
			return out, nil
		}
		n = n * 3 / 4
	}
}

type partResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func result(err error) partResult {
	if err == nil {
		return partResult{OK: true}
	}
	for _, k := range known {
		if errors.Is(err, k) {
			return partResult{Error: err.Error()}
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return partResult{Error: dial.ErrTimeout.Error()}
	}
	return partResult{Error: "the check failed"}
}

// check tries the IMAP login and the SMTP login, one after the other, and
// reports each. It reads no mail and sends none.
func (c call) check() (any, error) {
	s, err := c.open()
	if err == nil {
		_, err = s.Folders(c.ctx)
		s.Close()
	}
	imapRes := result(err)
	smtpRes := result(smtpc.Check(c.ctx, c.r.Dialer, c.acct))
	return map[string]any{"mailbox": c.acct.Address, "imap": imapRes, "smtp": smtpRes}, nil
}
