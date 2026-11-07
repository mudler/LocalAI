// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/gmail/internal/gmailapi"
	"github.com/mudler/LocalAI/addons/internal/argutil"
	"golang.org/x/sync/errgroup"
)

const messagesPath = "/gmail/v1/users/me/messages"

func pageSizeOr(n int) int {
	if n < 1 {
		return DefaultEntries
	}
	return min(n, MaxEntries)
}

// summaryHeaders are the headers a list or a thread shows.
var summaryHeaders = []string{"From", "To", "Cc", "Subject", "Date", "Message-ID"}

func headerOf(m *gMessage, name string) string { return m.Payload.header(name) }

// fetchMessage reads one message in a format and checks it lies inside the
// reach of the connection.
func (c *call) fetchMessage(id, format string, headers ...string) (*gMessage, error) {
	if err := checkID("messageId", id); err != nil {
		return nil, err
	}
	m, err := c.rawMessage(id, format, headers...)
	if err != nil {
		return nil, err
	}
	ok, err := c.inScope(m.LabelIDs)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, bad("the message is not in the label the host allowed")
	}
	return m, nil
}

func (c *call) rawMessage(id, format string, headers ...string) (*gMessage, error) {
	q := url.Values{"format": {format}}
	if len(headers) > 0 {
		q["metadataHeaders"] = headers
	}
	var m gMessage
	if err := c.r.API.GetJSON(c.ctx, c.token, messagesPath+"/"+argutil.IDPath(id), q, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

type searchArgs struct {
	Label         string `json:"label"`
	From          string `json:"from"`
	To            string `json:"to"`
	Subject       string `json:"subject"`
	Text          string `json:"text"`
	HasLabel      string `json:"hasLabel"`
	After         string `json:"after"`
	Before        string `json:"before"`
	HasAttachment bool   `json:"hasAttachment"`
	UnreadOnly    bool   `json:"unreadOnly"`
	Cursor        string `json:"cursor"`
	PageSize      int    `json:"pageSize"`
}

type listed struct {
	Messages      []struct{ ID, ThreadID string } `json:"messages"`
	NextPageToken string                          `json:"nextPageToken"`
}

type summary struct {
	ID       string   `json:"id"`
	ThreadID string   `json:"threadId"`
	From     string   `json:"from"`
	To       string   `json:"to,omitempty"`
	Subject  string   `json:"subject"`
	Date     string   `json:"date"`
	Snippet  string   `json:"snippet,omitempty"`
	Labels   []string `json:"labelIds,omitempty"`
	Unread   bool     `json:"unread,omitempty"`
}

func summarize(m *gMessage) summary {
	s := summary{ID: m.ID, ThreadID: m.ThreadID, From: headerText(headerOf(m, "From"), 200), To: headerText(headerOf(m, "To"), 300),
		Subject: headerText(headerOf(m, "Subject"), 200), Date: headerText(headerOf(m, "Date"), 64)}
	s.Snippet, _ = argutil.Runes(cleanLine(m.Snippet, 200), 200)
	for _, l := range m.LabelIDs {
		if l == "UNREAD" {
			s.Unread = true
		}
	}
	s.Labels = m.LabelIDs
	if len(s.Labels) > 20 {
		s.Labels = s.Labels[:20]
	}
	return s
}

func (c *call) search(raw map[string]json.RawMessage) (any, error) {
	var a searchArgs
	if err := argutil.DecodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.checkLabel(a.Label); err != nil {
		return nil, err
	}
	if err := argutil.CheckCursor(a.Cursor); err != nil {
		return nil, err
	}
	q, err := buildQuery(queryFilters{From: a.From, To: a.To, Subject: a.Subject, Text: a.Text, After: a.After, Before: a.Before,
		HasAttachment: a.HasAttachment, UnreadOnly: a.UnreadOnly})
	if err != nil {
		return nil, err
	}
	v := url.Values{"maxResults": {strconv.Itoa(pageSizeOr(a.PageSize))}}
	if q != "" {
		v.Set("q", q)
	}
	scope, err := c.scopeLabel()
	if err != nil {
		return nil, err
	}
	if scope != "" {
		v.Add("labelIds", scope)
	}
	if a.HasLabel != "" {
		if err := argutil.CheckLine("hasLabel", a.HasLabel, 100, true); err != nil {
			return nil, err
		}
		l, ok, err := c.find(strings.ReplaceAll(a.HasLabel, " ", "-"))
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, bad("hasLabel is not a label of this mailbox")
		}
		if l.ID != scope {
			v.Add("labelIds", l.ID)
		}
	}
	if a.Cursor != "" {
		v.Set("pageToken", a.Cursor)
	}
	var page listed
	if err := c.r.API.GetJSON(c.ctx, c.token, messagesPath, v, &page); err != nil {
		return nil, err
	}
	items, err := c.summaries(page.Messages)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"items": items}
	if page.NextPageToken != "" {
		out["nextCursor"] = page.NextPageToken
	}
	return out, nil
}

// summaries reads the headers of each listed message, a few at a time, in the
// order of the list. A message that is gone by now is left out.
func (c *call) summaries(ids []struct{ ID, ThreadID string }) ([]summary, error) {
	got := make([]*gMessage, len(ids))
	var group errgroup.Group
	group.SetLimit(fetchWorkers)
	for i := range ids {
		if !idShape.MatchString(ids[i].ID) {
			continue
		}
		group.Go(func() error {
			m, err := c.rawMessage(ids[i].ID, "metadata", summaryHeaders...)
			if errors.Is(err, gmailapi.ErrNotFound) {
				return nil
			}
			got[i] = m
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	items := []summary{}
	for _, m := range got {
		if m != nil {
			items = append(items, summarize(m))
		}
	}
	return items, nil
}

type headers struct {
	From       string `json:"from"`
	To         string `json:"to,omitempty"`
	Cc         string `json:"cc,omitempty"`
	ReplyTo    string `json:"replyTo,omitempty"`
	Subject    string `json:"subject"`
	Date       string `json:"date"`
	MessageID  string `json:"messageId,omitempty"`
	InReplyTo  string `json:"inReplyTo,omitempty"`
	References string `json:"references,omitempty"`
}

func headersOf(m *gMessage) headers {
	p := m.Payload
	return headers{From: headerText(p.header("From"), 300), To: headerText(p.header("To"), 1000), Cc: headerText(p.header("Cc"), 1000),
		ReplyTo: headerText(p.header("Reply-To"), 300), Subject: headerText(p.header("Subject"), 300), Date: headerText(p.header("Date"), 64),
		MessageID: cleanLine(p.header("Message-ID"), 300), InReplyTo: cleanLine(p.header("In-Reply-To"), 300), References: cleanLine(p.header("References"), 600)}
}

// window returns the part of text that starts at byte offset, at a character
// boundary, with at most limit bytes; next is the offset of the rest, or -1.
func window(text string, offset, limit int) (out string, start, next int) {
	if offset > len(text) {
		offset = len(text)
	}
	for offset < len(text) && !utf8.RuneStart(text[offset]) {
		offset++
	}
	rest := text[offset:]
	piece, left := argutil.Cut(rest, limit)
	if left == 0 {
		return piece, offset, -1
	}
	return piece, offset, offset + len(piece)
}

func attachmentsOf(b body) ([]attachment, int) {
	atts := b.Attachments
	if len(atts) > MaxAttachmentListed {
		return atts[:MaxAttachmentListed], len(atts) - MaxAttachmentListed
	}
	return atts, 0
}

func (c *call) getMessage(raw map[string]json.RawMessage) (any, error) {
	var a struct {
		Label     string `json:"label"`
		MessageID string `json:"messageId"`
		Offset    int    `json:"offset"`
	}
	if err := argutil.DecodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.checkLabel(a.Label); err != nil {
		return nil, err
	}
	if a.Offset < 0 {
		return nil, bad("offset is 0 or more")
	}
	m, err := c.fetchMessage(a.MessageID, "full")
	if err != nil {
		return nil, err
	}
	b := parseBody(m.Payload)
	if a.Offset > 0 && a.Offset >= len(b.Text) {
		return nil, bad("offset is past the end at %d bytes", len(b.Text))
	}
	text, start, next := window(b.Text, a.Offset, MaxBodyBytes)
	bodyOut := map[string]any{"text": text, "bytes": len(b.Text), "offset": start}
	switch b.Kind {
	case "html":
		bodyOut["format"] = "converted from HTML: images and link targets are left out"
	case "plain":
		bodyOut["format"] = "plain text"
	default:
		bodyOut["format"] = "none"
	}
	if next >= 0 {
		bodyOut["truncated"] = true
		bodyOut["nextOffset"] = next
		bodyOut["note"] = "Only part of the text is shown. Read the rest with offset set to nextOffset."
	}
	atts, more := attachmentsOf(b)
	out := map[string]any{"id": m.ID, "threadId": m.ThreadID, "labelIds": m.LabelIDs, "headers": headersOf(m), "body": bodyOut, "attachments": atts}
	if more > 0 {
		out["attachmentsOmitted"] = more
	}
	return out, nil
}

type threadMessage struct {
	ID          string       `json:"id"`
	Labels      []string     `json:"labelIds,omitempty"`
	Headers     headers      `json:"headers"`
	Text        string       `json:"text"`
	CutBytes    int          `json:"cutBytes,omitempty"`
	Attachments []attachment `json:"attachments,omitempty"`
}

func (c *call) getThread(raw map[string]json.RawMessage) (any, error) {
	var a struct {
		Label    string `json:"label"`
		ThreadID string `json:"threadId"`
	}
	if err := argutil.DecodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.checkLabel(a.Label); err != nil {
		return nil, err
	}
	if err := checkID("threadId", a.ThreadID); err != nil {
		return nil, err
	}
	var th struct {
		ID       string     `json:"id"`
		Messages []gMessage `json:"messages"`
	}
	if err := c.r.API.GetJSON(c.ctx, c.token, "/gmail/v1/users/me/threads/"+argutil.IDPath(a.ThreadID), url.Values{"format": {"full"}}, &th); err != nil {
		return nil, err
	}
	var kept []gMessage
	left := 0
	for _, m := range th.Messages {
		ok, err := c.inScope(m.LabelIDs)
		if err != nil {
			return nil, err
		}
		if ok {
			kept = append(kept, m)
		} else {
			left++
		}
	}
	if len(kept) == 0 {
		return nil, bad("the thread has no message in the allowed label")
	}
	earlier := 0
	if len(kept) > MaxThreadMessages {
		earlier = len(kept) - MaxThreadMessages
		kept = kept[earlier:]
	}
	bodies := make([]body, len(kept))
	for i := range kept {
		bodies[i] = parseBody(kept[i].Payload)
	}
	limit := max(1<<10, (MaxPayload-16<<10)/len(kept))
	for {
		msgs := make([]threadMessage, 0, len(kept))
		for i := range kept {
			text, left := argutil.Cut(bodies[i].Text, limit)
			atts := bodies[i].Attachments
			if len(atts) > 10 {
				atts = atts[:10]
			}
			msgs = append(msgs, threadMessage{ID: kept[i].ID, Labels: kept[i].LabelIDs, Headers: headersOf(&kept[i]), Text: text, CutBytes: left, Attachments: atts})
		}
		out := map[string]any{"threadId": th.ID, "messages": msgs}
		note := "Messages are in order, oldest first."
		cutAny := false
		for _, m := range msgs {
			cutAny = cutAny || m.CutBytes > 0
		}
		if cutAny {
			note += " Some texts are cut (cutBytes says how much). Read one whole with get-message."
		}
		if earlier > 0 {
			out["earlierMessagesOmitted"] = earlier
		}
		if left > 0 {
			out["messagesOutsideLabel"] = left
			note += " Messages outside the allowed label are left out."
		}
		out["note"] = note
		payload, err := c.r.Encode(out)
		if err != nil {
			return nil, err
		}
		if len(payload) <= MaxPayload-(4<<10) || limit <= 1<<10 {
			return out, nil
		}
		limit = limit * 3 / 4
	}
}

func (c *call) listLabels(raw map[string]json.RawMessage) (any, error) {
	var a struct {
		Label string `json:"label"`
	}
	if err := argutil.DecodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.checkLabel(a.Label); err != nil {
		return nil, err
	}
	ls, err := c.labels()
	if err != nil {
		return nil, err
	}
	scope, err := c.scopeLabel()
	if err != nil {
		return nil, err
	}
	out := []label{}
	for _, l := range ls {
		if scope != "" && l.ID != scope {
			continue
		}
		l.Name, _ = argutil.Runes(cleanLine(l.Name, 100), 100)
		out = append(out, l)
		if len(out) >= 200 {
			break
		}
	}
	res := map[string]any{"labels": out}
	if scope != "" {
		res["note"] = "This connection reaches one label, so only that label is listed."
	}
	return res, nil
}

// textTypes are the media types besides text/* whose content is text.
var textTypes = map[string]bool{
	"application/json": true, "application/xml": true, "application/yaml": true, "application/x-yaml": true,
	"application/x-ndjson": true, "application/toml": true, "application/sql": true, "application/javascript": true,
}

var textExts = map[string]bool{".txt": true, ".md": true, ".csv": true, ".tsv": true, ".json": true, ".log": true, ".xml": true,
	".yaml": true, ".yml": true, ".ics": true, ".eml": false, ".html": false}

func isText(mime, name string) bool {
	mime = strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	if strings.HasPrefix(mime, "text/") && mime != "text/html" || textTypes[mime] || strings.HasSuffix(mime, "+json") {
		return true
	}
	return (mime == "application/octet-stream" || mime == "") && textExts[strings.ToLower(path.Ext(name))]
}

func (c *call) readAttachment(raw map[string]json.RawMessage) (any, error) {
	var a struct {
		Label        string `json:"label"`
		MessageID    string `json:"messageId"`
		AttachmentID string `json:"attachmentId"`
		Offset       int    `json:"offset"`
	}
	if err := argutil.DecodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if err := c.checkLabel(a.Label); err != nil {
		return nil, err
	}
	if err := checkID("attachmentId", a.AttachmentID); err != nil {
		return nil, err
	}
	if a.Offset < 0 {
		return nil, bad("offset is 0 or more")
	}
	m, err := c.fetchMessage(a.MessageID, "full")
	if err != nil {
		return nil, err
	}
	var att *attachment
	for _, x := range parseBody(m.Payload).Attachments {
		if x.AttachmentID == a.AttachmentID {
			x := x
			att = &x
			break
		}
	}
	if att == nil {
		return nil, bad("no attachment with this ID in the message")
	}
	out := map[string]any{"messageId": m.ID, "attachment": att}
	if !isText(att.MimeType, att.Filename) {
		out["content"] = nil
		out["note"] = "This attachment is not text. Only the details are given."
		return out, nil
	}
	if att.Bytes > MaxReadAttachment {
		out["content"] = nil
		out["note"] = "This attachment is too large to read here. Only the details are given."
		return out, nil
	}
	var res struct {
		Data string `json:"data"`
	}
	p := messagesPath + "/" + argutil.IDPath(m.ID) + "/attachments/" + argutil.IDPath(a.AttachmentID)
	if err := c.r.API.GetJSON(c.ctx, c.token, p, nil, &res); err != nil {
		return nil, err
	}
	data, ok := decodeData(res.Data)
	if !ok {
		return nil, bad("the attachment could not be decoded")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		out["content"] = nil
		out["note"] = "This attachment is not text. Only the details are given."
		return out, nil
	}
	total := len(data)
	if a.Offset > 0 && a.Offset >= total {
		return nil, bad("offset is past the end at %d bytes", total)
	}
	text := tidyKeepLines(strings.ToValidUTF8(string(data), "�"))
	// The offset counts bytes of the text as shown, so a page and the next one
	// join without a gap even after the clean-up.
	total = len(text)
	if a.Offset > 0 && a.Offset >= total {
		return nil, bad("offset is past the end at %d bytes", total)
	}
	limit := MaxFileBytes
	for {
		piece, start, next := window(text, a.Offset, limit)
		res := map[string]any{"messageId": m.ID, "attachment": att, "content": piece, "offset": start, "totalBytes": total}
		if next >= 0 {
			res["truncated"] = true
			res["nextOffset"] = next
			res["note"] = "Only part of the text is shown. Read the rest with offset set to nextOffset."
		}
		payload, err := c.r.Encode(res)
		if err != nil {
			return nil, err
		}
		if len(payload) <= MaxPayload-(4<<10) || limit <= 1<<10 {
			return res, nil
		}
		limit = limit * 3 / 4
	}
}

// tidyKeepLines drops hidden characters but keeps the layout of a file.
func tidyKeepLines(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	return strings.Map(func(r rune) rune {
		if dropRune(r) {
			return -1
		}
		return r
	}, s)
}
