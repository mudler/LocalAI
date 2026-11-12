// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"strings"
	"unicode"

	"github.com/mudler/LocalAI/addons/internal/argutil"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// The shapes of a message as the API returns it with format=full.
type gHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type gBody struct {
	Size         int64  `json:"size"`
	Data         string `json:"data"`
	AttachmentID string `json:"attachmentId"`
}

type gPart struct {
	PartID   string    `json:"partId"`
	MimeType string    `json:"mimeType"`
	Filename string    `json:"filename"`
	Headers  []gHeader `json:"headers"`
	Body     gBody     `json:"body"`
	Parts    []gPart   `json:"parts"`
}

type gMessage struct {
	ID           string   `json:"id"`
	ThreadID     string   `json:"threadId"`
	LabelIDs     []string `json:"labelIds"`
	Snippet      string   `json:"snippet"`
	InternalDate string   `json:"internalDate"`
	Payload      gPart    `json:"payload"`
}

// Limits of the walk over the MIME tree, so a hostile message cannot make the
// add-on do unbounded work.
const (
	maxDepth = 20
	maxParts = 200
	// maxBodyBytes bounds the bytes of text parts that one message decodes.
	maxBodyBytes = 4 << 20
)

func (p gPart) header(name string) string {
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// attachment is a part that holds a file. Its bytes are never in a result.
type attachment struct {
	PartID       string `json:"partId,omitempty"`
	Filename     string `json:"filename"`
	MimeType     string `json:"mimeType"`
	Bytes        int64  `json:"bytes"`
	AttachmentID string `json:"attachmentId,omitempty"`
}

// body is the text of a message.
type body struct {
	Text string
	// Kind is plain when the text is a text/plain part and html when it was
	// made from an HTML part. Empty when the message has no text.
	Kind        string
	Attachments []attachment
}

type walker struct {
	parts, budget int
	atts          []attachment
}

// parseBody reads the text and the attachment list of a message.
func parseBody(root gPart) body {
	w := &walker{budget: maxBodyBytes}
	text, kind := w.walk(root, 0)
	return body{Text: tidy(text), Kind: kind, Attachments: w.atts}
}

func isAttachment(p gPart) bool {
	return p.Filename != "" || p.Body.AttachmentID != ""
}

// walk returns the text of a subtree, preferring text/plain to text/html in
// an alternative.
func (w *walker) walk(p gPart, depth int) (string, string) {
	w.parts++
	if depth > maxDepth || w.parts > maxParts {
		return "", ""
	}
	mt := strings.ToLower(strings.TrimSpace(strings.SplitN(p.MimeType, ";", 2)[0]))
	switch {
	case strings.HasPrefix(mt, "multipart/"):
		type piece struct{ text, kind string }
		var pieces []piece
		for _, c := range p.Parts {
			t, k := w.walk(c, depth+1)
			if strings.TrimSpace(t) != "" {
				pieces = append(pieces, piece{t, k})
			}
		}
		if len(pieces) == 0 {
			return "", ""
		}
		if mt == "multipart/alternative" {
			for _, pc := range pieces {
				if pc.kind == "plain" {
					return pc.text, "plain"
				}
			}
			last := pieces[len(pieces)-1]
			return last.text, last.kind
		}
		var parts []string
		kind := "html"
		for _, pc := range pieces {
			parts = append(parts, pc.text)
			if pc.kind == "plain" {
				kind = "plain"
			}
		}
		return strings.Join(parts, "\n\n"), kind
	case isAttachment(p):
		w.atts = append(w.atts, attachment{PartID: p.PartID, Filename: cleanLine(p.Filename, MaxNameRunes), MimeType: cleanLine(mt, 100), Bytes: p.Body.Size, AttachmentID: p.Body.AttachmentID})
		return "", ""
	case mt == "text/plain" || mt == "text/html":
		data, ok := decodeData(p.Body.Data)
		if !ok || w.budget <= 0 {
			return "", ""
		}
		if len(data) > w.budget {
			data = data[:w.budget]
		}
		w.budget -= len(data)
		text := decodeCharset(data, p.header("Content-Type"))
		if mt == "text/html" {
			return htmlToText(text), "html"
		}
		return text, "plain"
	case mt == "message/rfc822":
		w.atts = append(w.atts, attachment{PartID: p.PartID, Filename: "(forwarded message)", MimeType: mt, Bytes: p.Body.Size})
		return "", ""
	}
	if p.Body.Size > 0 {
		// An inline part of another type, such as an image the HTML shows.
		w.atts = append(w.atts, attachment{PartID: p.PartID, Filename: "(inline " + cleanLine(mt, 60) + ")", MimeType: cleanLine(mt, 100), Bytes: p.Body.Size})
	}
	return "", ""
}

// decodeData reads the base64url body of a part, with or without padding.
func decodeData(s string) ([]byte, bool) {
	if s == "" {
		return nil, true
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, false
	}
	return b, true
}

// decodeCharset turns the bytes of a text part into UTF-8.
func decodeCharset(data []byte, contentType string) string {
	_, params, err := mime.ParseMediaType(contentType)
	label := ""
	if err == nil {
		label = params["charset"]
	}
	if label != "" && !strings.EqualFold(label, "utf-8") && !strings.EqualFold(label, "us-ascii") {
		if r, err := charset.NewReaderLabel(label, bytes.NewReader(data)); err == nil {
			if out, err := io.ReadAll(r); err == nil {
				return strings.ToValidUTF8(string(out), "�")
			}
		}
	}
	return strings.ToValidUTF8(string(data), "�")
}

// A rune that hides text or steers its direction is dropped: a control
// character other than the line break and the tab, and any character of the
// format class (zero width space and joiner, bidirectional marks, the tag
// characters).
func dropRune(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

// tidy cleans the text of a mail: no hidden character, \n line ends, no
// trailing space and no more than one empty line in a row.
func tidy(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	s = strings.Map(func(r rune) rune {
		if dropRune(r) {
			return -1
		}
		return r
	}, s)
	lines := strings.Split(s, "\n")
	var out []string
	blank := 0
	for _, l := range lines {
		l = strings.TrimRightFunc(l, unicode.IsSpace)
		if l == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// cleanLine makes a header value or a file name safe to show: one line, no
// hidden character, at most n characters.
func cleanLine(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if dropRune(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "�"))
	s = strings.Join(strings.Fields(s), " ")
	out, _ := argutil.Runes(s, n)
	return out
}

var wordDecoder = &mime.WordDecoder{CharsetReader: func(label string, in io.Reader) (io.Reader, error) {
	return charset.NewReaderLabel(label, in)
}}

// headerText decodes RFC 2047 words and cleans the value.
func headerText(v string, n int) string {
	if d, err := wordDecoder.DecodeHeader(v); err == nil {
		v = d
	}
	return cleanLine(v, n)
}

// skipTags are elements whose content is never text for the reader.
var skipTags = map[string]bool{"script": true, "style": true, "head": true, "title": true, "noscript": true, "template": true, "svg": true,
	"img": true, "iframe": true, "object": true, "embed": true, "video": true, "audio": true, "canvas": true, "picture": true, "map": true}

var blockTags = map[string]bool{"p": true, "div": true, "br": true, "tr": true, "table": true, "ul": true, "ol": true, "li": true, "h1": true,
	"h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "blockquote": true, "pre": true, "hr": true, "section": true, "article": true,
	"header": true, "footer": true, "address": true}

// hiddenStyle reports a style that hides its element, a common place to put
// text that only a model reads.
func hiddenStyle(style string) bool {
	s := strings.ToLower(strings.Join(strings.Fields(style), ""))
	for _, h := range []string{"display:none", "visibility:hidden", "font-size:0", "opacity:0", "mso-hide:all", "max-height:0", "height:0;overflow:hidden", "color:transparent"} {
		if strings.Contains(s, h) {
			// font-size:0 is also the prefix of font-size:0.9em; require an end.
			if h == "font-size:0" {
				i := strings.Index(s, h) + len(h)
				if i < len(s) && (s[i] == '.' || (s[i] >= '1' && s[i] <= '9')) {
					continue
				}
			}
			if h == "opacity:0" {
				i := strings.Index(s, h) + len(h)
				if i < len(s) && s[i] == '.' {
					continue
				}
			}
			return true
		}
	}
	return false
}

func hiddenNode(n *html.Node) bool {
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "hidden":
			return true
		case "aria-hidden":
			if strings.EqualFold(a.Val, "true") {
				return true
			}
		case "style":
			if hiddenStyle(a.Val) {
				return true
			}
		}
	}
	return false
}

// htmlToText makes plain text of an HTML document. It keeps text only: no
// link target, no image, no script, no hidden element. It never fetches
// anything.
func htmlToText(src string) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return ""
	}
	var b strings.Builder
	var visit func(n *html.Node, depth int)
	visit = func(n *html.Node, depth int) {
		if depth > 200 {
			return
		}
		switch n.Type {
		case html.TextNode:
			b.WriteString(collapse(n.Data))
			return
		case html.ElementNode:
			if skipTags[n.Data] || hiddenNode(n) {
				return
			}
			if n.Data == "li" {
				b.WriteString("\n- ")
			} else if blockTags[n.Data] {
				b.WriteString("\n")
			}
		case html.CommentNode, html.DoctypeNode:
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c, depth+1)
		}
		if n.Type == html.ElementNode && blockTags[n.Data] && n.Data != "li" {
			b.WriteString("\n")
		}
	}
	visit(doc, 0)
	return b.String()
}

// collapse folds runs of white space (which HTML ignores) to one space.
func collapse(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	if space {
		b.WriteByte(' ')
	}
	return b.String()
}
