// Package extract turns a fetched web response into text a model can read:
// HTML becomes Markdown-like text with the page chrome removed, text formats
// stay as they are, and the result is cut at a character budget with a marker
// that says how much is left.
package extract

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"
)

// Formats of the HTML output.
const (
	FormatMarkdown = "markdown"
	FormatText     = "text"
)

// Accept lists the media types FromResponse reads. PDF is read only when
// Options.PDF is set.
var Accept = []string{
	"text/html", "application/xhtml+xml",
	"text/plain", "text/markdown", "text/x-markdown", "text/csv",
	"application/json", "application/*+json",
	"application/xml", "text/xml", "application/*+xml",
	"application/pdf",
}

// ErrUnsupported is returned for a media type this package cannot read.
var ErrUnsupported = errors.New("unsupported type")

// Options are the choices of the caller.
type Options struct {
	// MaxChars is the character budget of Body. Zero means no limit.
	MaxChars int
	// Format is FormatMarkdown (default) or FormatText, for HTML.
	Format string
	// PDF extracts the text of a PDF document. Nil means PDF is unsupported.
	PDF func([]byte) (string, error)
}

// Text is the readable content of a response.
type Text struct {
	Title string
	Body  string
	// Omitted is the number of characters cut from Body.
	Omitted int
}

// FromResponse reads body, of media type contentType and charset cs (may be
// empty), fetched from base.
func FromResponse(contentType, cs string, body []byte, base *url.URL, o Options) (Text, error) {
	var t Text
	switch {
	case contentType == "text/html" || contentType == "application/xhtml+xml":
		r, err := decode(body, cs, contentType)
		if err != nil {
			return t, err
		}
		t.Title, t.Body, err = HTML(r, base, o.Format)
		if err != nil {
			return t, err
		}
	case contentType == "application/pdf":
		if o.PDF == nil {
			return t, fmt.Errorf("%w: %s", ErrUnsupported, contentType)
		}
		s, err := o.PDF(body)
		if err != nil {
			return t, fmt.Errorf("the PDF could not be read: %w", err)
		}
		t.Body = strings.TrimSpace(s)
	case isText(contentType):
		r, err := decode(body, cs, contentType)
		if err != nil {
			return t, err
		}
		b, err := io.ReadAll(r)
		if err != nil {
			return t, err
		}
		t.Body = strings.TrimSpace(strings.ToValidUTF8(string(b), "�"))
	default:
		return t, fmt.Errorf("%w: %s", ErrUnsupported, contentType)
	}
	t.Body, t.Omitted = Truncate(t.Body, o.MaxChars)
	return t, nil
}

func isText(ct string) bool {
	switch {
	case strings.HasPrefix(ct, "text/"):
		return true
	case ct == "application/json", ct == "application/xml":
		return true
	case strings.HasPrefix(ct, "application/") && (strings.HasSuffix(ct, "+json") || strings.HasSuffix(ct, "+xml")):
		return true
	}
	return false
}

// decode returns a UTF-8 reader of body. A charset the decoder does not know
// is read as UTF-8, with invalid bytes replaced.
func decode(body []byte, cs, contentType string) (io.Reader, error) {
	ct := contentType
	if cs != "" {
		ct += "; charset=" + cs
	}
	r, err := charset.NewReader(bytes.NewReader(body), ct)
	if err != nil {
		return bytes.NewReader(body), nil
	}
	return r, nil
}

// Truncate cuts s at max characters and appends the marker
// "[truncated, N chars more]". It returns the number of characters cut.
func Truncate(s string, max int) (string, int) {
	if max <= 0 {
		return s, 0
	}
	n := utf8.RuneCountInString(s)
	if n <= max {
		return s, 0
	}
	i, c := 0, 0
	for i = range s {
		if c == max {
			break
		}
		c++
	}
	omitted := n - max
	return strings.TrimRight(s[:i], " \n") + fmt.Sprintf("\n\n[truncated, %d chars more]", omitted), omitted
}

// skipped are elements that carry no readable content or are page chrome.
var skipped = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true,
	atom.Svg: true, atom.Iframe: true, atom.Nav: true, atom.Header: true,
	atom.Footer: true, atom.Aside: true, atom.Form: true, atom.Button: true,
	atom.Select: true, atom.Input: true, atom.Textarea: true, atom.Head: true,
	atom.Canvas: true, atom.Object: true, atom.Embed: true, atom.Link: true,
	atom.Meta: true, atom.Dialog: true, atom.Math: true,
}

// blocks start a new paragraph.
var blocks = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Section: true, atom.Article: true,
	atom.Main: true, atom.Blockquote: true, atom.Figure: true, atom.Figcaption: true,
	atom.Dl: true, atom.Dt: true, atom.Dd: true, atom.Address: true,
	atom.Details: true, atom.Summary: true, atom.Body: true, atom.Html: true,
	atom.Center: true, atom.Hgroup: true, atom.Caption: true,
}

var displayNone = regexp.MustCompile(`(?i)display\s*:\s*none|visibility\s*:\s*hidden`)

func hidden(n *html.Node) bool {
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "hidden":
			return true
		case "aria-hidden":
			if strings.EqualFold(strings.TrimSpace(a.Val), "true") {
				return true
			}
		case "style":
			if displayNone.MatchString(a.Val) {
				return true
			}
		}
	}
	return false
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// HTML converts a page to text. format is FormatMarkdown or FormatText. The
// content of <main>, else of a single <article>, else of <body> is used.
func HTML(r io.Reader, base *url.URL, format string) (title, body string, err error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", "", fmt.Errorf("the page could not be parsed: %w", err)
	}
	title = strings.Join(strings.Fields(textOf(find(doc, atom.Title))), " ")
	root := find(doc, atom.Main)
	if root == nil {
		if arts := findAll(doc, atom.Article); len(arts) == 1 {
			root = arts[0]
		}
	}
	if root == nil {
		root = find(doc, atom.Body)
	}
	if root == nil {
		root = doc
	}
	rd := &renderer{md: format != FormatText, base: base}
	rd.walk(root)
	rd.flush()
	out := strings.TrimSpace(rd.out.String())
	out = manyBlankLines.ReplaceAllString(out, "\n\n")
	return title, out, nil
}

var manyBlankLines = regexp.MustCompile(`\n{3,}`)

func find(n *html.Node, a atom.Atom) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := find(c, a); f != nil {
			return f
		}
	}
	return nil
}

func findAll(n *html.Node, a atom.Atom) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == a {
			out = append(out, n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

// textOf is the raw text of a subtree, without skipped elements.
func textOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
		case html.ElementNode:
			if n.DataAtom != atom.Title && (skipped[n.DataAtom] || hidden(n)) {
				return
			}
			if n.DataAtom == atom.Br {
				b.WriteString("\n")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

type renderer struct {
	md     bool
	base   *url.URL
	out    strings.Builder
	inline strings.Builder
	// lists holds, for each open list, its kind and the next number.
	lists []listState
	quote int
	// item is the bullet of the list item being written. The first block of
	// the item gets it; later blocks of the item get as many spaces.
	item string
	// lastItem says that the last block written was a list item.
	lastItem bool
}

type listState struct {
	ordered bool
	next    int
}

var spaces = regexp.MustCompile(`\s+`)

// flush ends the current paragraph.
func (r *renderer) flush() {
	text := strings.TrimSpace(spaces.ReplaceAllString(r.inline.String(), " "))
	r.inline.Reset()
	if text == "" {
		return
	}
	if r.item != "" {
		text = r.item + text
		r.item = strings.Repeat(" ", len(r.item))
	}
	r.emit(text, len(r.lists) > 0)
}

// emit writes one finished block. tight blocks (list items) follow each other
// on the next line; other blocks are separated by a blank line.
func (r *renderer) emit(text string, tight bool) {
	if r.out.Len() > 0 {
		if tight && r.lastItem {
			r.out.WriteString("\n")
		} else {
			r.out.WriteString("\n\n")
		}
	}
	if r.quote > 0 && r.md {
		q := strings.Repeat("> ", r.quote)
		text = q + strings.ReplaceAll(text, "\n", "\n"+q)
	}
	r.out.WriteString(text)
	r.lastItem = tight
}

func (r *renderer) walk(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		r.inline.WriteString(n.Data)
		return
	case html.ElementNode:
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			r.walk(c)
		}
		return
	}
	if skipped[n.DataAtom] || hidden(n) {
		return
	}
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		r.flush()
		level := int(n.Data[1] - '0')
		text := r.inlineOf(n)
		if text == "" {
			return
		}
		if r.md {
			text = strings.Repeat("#", level) + " " + text
		}
		r.emit(text, false)
	case atom.Br:
		r.inline.WriteString("\n")
		r.flush()
	case atom.Hr:
		r.flush()
		if r.md {
			r.emit("---", false)
		}
	case atom.Pre:
		r.flush()
		code := strings.Trim(textOf(n), "\n")
		if strings.TrimSpace(code) == "" {
			return
		}
		if r.md {
			lang := ""
			if c := find(n, atom.Code); c != nil {
				for cl := range strings.FieldsSeq(attr(c, "class")) {
					if l, ok := strings.CutPrefix(cl, "language-"); ok {
						lang = l
					}
				}
			}
			code = "```" + lang + "\n" + code + "\n```"
		}
		r.emit(code, false)
	case atom.Code, atom.Kbd, atom.Samp:
		text := strings.Join(strings.Fields(textOf(n)), " ")
		if text == "" {
			return
		}
		if r.md {
			text = "`" + text + "`"
		}
		r.inline.WriteString(text)
	case atom.A:
		text := r.inlineOf(n)
		href := r.resolve(attr(n, "href"))
		switch {
		case text == "" && href == "":
		case !r.md || href == "" || href == text:
			if text == "" {
				text = href
			}
			r.inline.WriteString(text)
		default:
			r.inline.WriteString("[" + strings.ReplaceAll(text, "]", "\\]") + "](" + href + ")")
		}
	case atom.Img:
		if alt := strings.TrimSpace(attr(n, "alt")); alt != "" {
			r.inline.WriteString("[image: " + alt + "]")
		}
	case atom.Ul, atom.Ol:
		r.flush()
		r.lists = append(r.lists, listState{ordered: n.DataAtom == atom.Ol, next: 1})
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			r.walk(c)
		}
		r.flush()
		r.lists = r.lists[:len(r.lists)-1]
	case atom.Li:
		r.flush()
		depth := len(r.lists)
		bullet := "- "
		if depth > 0 && r.lists[depth-1].ordered {
			bullet = fmt.Sprintf("%d. ", r.lists[depth-1].next)
			r.lists[depth-1].next++
		}
		if depth == 0 {
			depth = 1
		}
		saved := r.item
		r.item = strings.Repeat("  ", depth-1) + bullet
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			r.walk(c)
		}
		r.flush()
		r.item = saved
	case atom.Table:
		r.flush()
		r.table(n)
	case atom.Blockquote:
		r.flush()
		r.quote++
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			r.walk(c)
		}
		r.flush()
		r.quote--
	default:
		block := blocks[n.DataAtom]
		if block {
			r.flush()
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			r.walk(c)
		}
		if block {
			r.flush()
		}
	}
}

// inlineOf renders the children of n as one line.
func (r *renderer) inlineOf(n *html.Node) string {
	sub := &renderer{md: r.md, base: r.base}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sub.walk(c)
	}
	sub.flush()
	return strings.TrimSpace(spaces.ReplaceAllString(sub.out.String(), " "))
}

func (r *renderer) table(n *html.Node) {
	var rows [][]string
	header := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if skipped[n.DataAtom] || hidden(n) {
			return
		}
		if n.DataAtom == atom.Tr {
			var cells []string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && (c.DataAtom == atom.Td || c.DataAtom == atom.Th) {
					if c.DataAtom == atom.Th && len(rows) == 0 {
						header = true
					}
					cells = append(cells, strings.ReplaceAll(r.inlineOf(c), "|", "\\|"))
				}
			}
			if len(cells) > 0 {
				rows = append(rows, cells)
			}
			return
		}
		if n.DataAtom == atom.Table && len(rows) > 0 {
			return // a nested table is not unpacked
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c)
	}
	if len(rows) == 0 {
		return
	}
	var b strings.Builder
	for i, row := range rows {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("| " + strings.Join(row, " | ") + " |")
		if i == 0 && header && r.md {
			b.WriteString("\n|" + strings.Repeat(" --- |", len(row)))
		}
	}
	r.emit(b.String(), false)
}

// resolve makes href absolute against the page. Only http and https links
// are kept: a javascript: or data: link is noise or worse.
func (r *renderer) resolve(href string) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if r.base != nil {
		u = r.base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	u.User = nil
	return u.String()
}
