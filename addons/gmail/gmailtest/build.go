// SPDX-License-Identifier: MIT
package gmailtest

import "strings"

// Mail is the short form of a message for a test.
type Mail struct {
	ID, ThreadID, From, To, Cc, Subject, Date, MessageID, Body string
	Labels                                                     []string
	Snippet                                                    string
}

func (m Mail) headers() []Header {
	hs := []Header{{"From", m.From}, {"To", m.To}, {"Subject", m.Subject}}
	if m.Cc != "" {
		hs = append(hs, Header{"Cc", m.Cc})
	}
	date := m.Date
	if date == "" {
		date = "Mon, 05 Jan 2026 10:00:00 +0000"
	}
	hs = append(hs, Header{"Date", date})
	if m.MessageID != "" {
		hs = append(hs, Header{"Message-ID", m.MessageID})
	}
	return hs
}

func (m Mail) message(p *Part) Message {
	p.Headers = append(m.headers(), p.Headers...)
	sn := m.Snippet
	if sn == "" {
		sn = strings.TrimSpace(m.Body)
	}
	return Message{ID: m.ID, ThreadID: m.ThreadID, Labels: m.Labels, Snippet: sn, Payload: p}
}

// Plain is a message with one text/plain body.
func Plain(m Mail) Message {
	return m.message(&Part{MimeType: "text/plain", Headers: []Header{{"Content-Type", "text/plain; charset=UTF-8"}}, Data: []byte(m.Body)})
}

// HTMLOnly is a message whose only body is text/html.
func HTMLOnly(m Mail, html string) Message {
	return m.message(&Part{MimeType: "text/html", Headers: []Header{{"Content-Type", "text/html; charset=UTF-8"}}, Data: []byte(html)})
}

// Alternative is multipart/alternative with a plain and an HTML part.
func Alternative(m Mail, html string) Message {
	return m.message(&Part{MimeType: "multipart/alternative", Parts: []*Part{
		{MimeType: "text/plain", Headers: []Header{{"Content-Type", "text/plain; charset=UTF-8"}}, Data: []byte(m.Body)},
		{MimeType: "text/html", Headers: []Header{{"Content-Type", "text/html; charset=UTF-8"}}, Data: []byte(html)},
	}})
}

// Attachment is a file part with an attachment ID.
func Attachment(name, mime, id string, data []byte) *Part {
	return &Part{MimeType: mime, Filename: name, AttachmentID: id, Data: data,
		Headers: []Header{{"Content-Disposition", `attachment; filename="` + name + `"`}}}
}

// WithAttachments is multipart/mixed: a multipart/alternative body (plain and
// HTML, nested one level down) and the attachments.
func WithAttachments(m Mail, html string, atts ...*Part) Message {
	alt := &Part{MimeType: "multipart/alternative", Parts: []*Part{
		{MimeType: "text/plain", Headers: []Header{{"Content-Type", "text/plain; charset=UTF-8"}}, Data: []byte(m.Body)},
		{MimeType: "text/html", Headers: []Header{{"Content-Type", "text/html; charset=UTF-8"}}, Data: []byte(html)},
	}}
	return m.message(&Part{MimeType: "multipart/mixed", Parts: append([]*Part{alt}, atts...)})
}

// Raw is a message with a text part in another charset (the bytes are given
// as they are on the wire).
func Charset(m Mail, charset string, data []byte) Message {
	return m.message(&Part{MimeType: "text/plain", Headers: []Header{{"Content-Type", "text/plain; charset=" + charset}}, Data: data})
}
