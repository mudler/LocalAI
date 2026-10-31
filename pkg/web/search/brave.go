package search

import (
	"bytes"
	"context"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// BraveEndpoint is the result page of Brave Search. It needs no key; the API
// with a key is a provider of the Web add-on.
const BraveEndpoint = "https://search.brave.com/search"

type braveHTML struct {
	get      Getter
	endpoint string
}

// NewBrave returns the provider that reads the result page of Brave Search.
// endpoint is BraveEndpoint outside tests.
func NewBrave(g Getter, endpoint string) Provider {
	return &braveHTML{get: g, endpoint: endpoint}
}

func (*braveHTML) Name() string { return KeylessBrave }
func (*braveHTML) Host() string { return "search.brave.com" }

var braveFreshness = map[string]string{"day": "pd", "week": "pw", "month": "pm", "year": "py"}

func (b *braveHTML) Search(ctx context.Context, q Query) ([]Result, error) {
	v := url.Values{"q": {textWithSite(q)}, "source": {"web"}}
	if f := braveFreshness[q.Freshness]; f != "" {
		v.Set("tf", f)
	}
	resp, err := fetchPage(ctx, b.get, b.endpoint+"?"+v.Encode())
	if err != nil {
		return nil, err
	}
	if resp.FinalURL != nil && strings.Contains(strings.ToLower(resp.FinalURL.Path), "captcha") {
		return nil, challenged()
	}
	return ParseBrave(resp.Body)
}

// ParseBrave reads the web results of a Brave Search page: the blocks with
// class "snippet" and data-type "web". Other blocks (news clusters, videos,
// the AI answer) are dropped. A page with no results is a bot check when it
// has the marks of one, and otherwise ErrProvider: Brave answers almost every
// query with something, so an empty page is more likely a new layout than
// nothing found.
func ParseBrave(page []byte) ([]Result, error) {
	doc, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		return nil, unreadable()
	}
	var out []Result
	seen := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && classes(n)["snippet"] && attrOf(n, "data-type") == "web" {
			if r, ok := braveResult(n); ok && !seen[r.URL] {
				seen[r.URL] = true
				out = append(out, r)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(out) == 0 {
		if hasChallenge(doc) {
			return nil, challenged()
		}
		return nil, unreadable()
	}
	return out, nil
}

func braveResult(n *html.Node) (Result, bool) {
	a := firstElement(n, func(e *html.Node) bool { return e.DataAtom == atom.A && braveLink(attrOf(e, "href")) != "" })
	if a == nil {
		return Result{}, false
	}
	r := Result{URL: braveLink(attrOf(a, "href"))}
	if t := firstByClass(a, "title", "snippet-title", "search-snippet-title"); t != nil {
		r.Title = clean(text(t), MaxTitle)
		if r.Title == "" {
			r.Title = clean(attrOf(t, "title"), MaxTitle)
		}
	}
	if r.Title == "" {
		r.Title = clean(text(a), MaxTitle)
	}
	if d := firstByClass(n, "generic-snippet", "snippet-description", "snippet-content"); d != nil {
		if c := firstByClass(d, "content"); c != nil {
			d = c
		}
		r.Snippet = clean(text(d), MaxSnippet)
	}
	return r, r.Title != ""
}

// braveLink keeps an http(s) link off the pages of Brave itself.
func braveLink(href string) string {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	if h := strings.ToLower(u.Hostname()); h == "brave.com" || strings.HasSuffix(h, ".brave.com") {
		return ""
	}
	return u.String()
}
