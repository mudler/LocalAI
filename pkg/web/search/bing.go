package search

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// BingEndpoint is the result page of Bing. It needs no key.
const BingEndpoint = "https://www.bing.com/search"

type bingHTML struct {
	get      Getter
	endpoint string
	now      func() time.Time
}

// NewBing returns the provider that reads the result page of Bing. endpoint
// is BingEndpoint outside tests.
func NewBing(g Getter, endpoint string) Provider {
	return &bingHTML{get: g, endpoint: endpoint, now: time.Now}
}

func (*bingHTML) Name() string { return KeylessBing }
func (*bingHTML) Host() string { return "www.bing.com" }

func (b *bingHTML) Search(ctx context.Context, q Query) ([]Result, error) {
	v := url.Values{"q": {textWithSite(q)}}
	if f := bingFreshness(q.Freshness, b.now()); f != "" {
		v.Set("filters", f)
	}
	resp, err := fetchPage(ctx, b.get, b.endpoint+"?"+v.Encode())
	if err != nil {
		return nil, err
	}
	return ParseBing(resp.Body)
}

// bingFreshness is the filters parameter of a period. Bing has day, week and
// month; a year is a range of days since 1970.
func bingFreshness(f string, now time.Time) string {
	switch f {
	case "day":
		return `ex1:"ez1"`
	case "week":
		return `ex1:"ez2"`
	case "month":
		return `ex1:"ez3"`
	case "year":
		end := now.Unix() / 86400
		return fmt.Sprintf(`ex1:"ez5_%d_%d"`, end-365, end)
	}
	return ""
}

// ParseBing reads the organic results of a Bing page (li.b_algo) and unwraps
// the /ck/a redirect links. Ads are in other blocks and are not read. A page
// with the "no results" block (li.b_no) and no results is an empty answer; a
// page with neither is a bot check when it has the marks of one, and
// otherwise ErrProvider.
func ParseBing(page []byte) ([]Result, error) {
	doc, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		return nil, unreadable()
	}
	var (
		out       []Result
		noResults bool
		seen      = map[string]bool{}
	)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			cls := classes(n)
			if cls["b_no"] {
				noResults = true
			}
			if cls["b_algo"] {
				if r, ok := bingResult(n); ok && !seen[r.URL] {
					seen[r.URL] = true
					out = append(out, r)
				}
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	switch {
	case len(out) > 0:
		return out, nil
	case noResults:
		return []Result{}, nil
	case hasChallenge(doc):
		return nil, challenged()
	}
	return nil, unreadable()
}

func bingResult(n *html.Node) (Result, bool) {
	h2 := firstElement(n, func(e *html.Node) bool { return e.DataAtom == atom.H2 })
	if h2 == nil {
		return Result{}, false
	}
	a := firstElement(h2, func(e *html.Node) bool { return e.DataAtom == atom.A })
	if a == nil {
		return Result{}, false
	}
	r := Result{Title: clean(text(a), MaxTitle), URL: unwrapBing(attrOf(a, "href"))}
	if c := firstByClass(n, "b_caption"); c != nil {
		if p := firstElement(c, func(e *html.Node) bool { return e.DataAtom == atom.P }); p != nil {
			c = p
		}
		r.Snippet = clean(text(c), MaxSnippet)
	} else if p := firstElement(n, func(e *html.Node) bool { return e.DataAtom == atom.P }); p != nil {
		r.Snippet = clean(text(p), MaxSnippet)
	}
	return r, r.URL != "" && r.Title != ""
}

// unwrapBing turns "https://www.bing.com/ck/a?...&u=a1<base64url>" into the
// target. Only http and https targets off Bing are kept.
func unwrapBing(href string) string {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return ""
	}
	if isBingHost(u.Hostname()) && u.Path == "/ck/a" {
		enc := u.Query().Get("u")
		if !strings.HasPrefix(enc, "a1") {
			return ""
		}
		enc = strings.TrimRight(enc[2:], "=")
		raw, err := base64.RawURLEncoding.DecodeString(enc)
		if err != nil {
			if raw, err = base64.RawStdEncoding.DecodeString(enc); err != nil {
				return ""
			}
		}
		if u, err = url.Parse(string(raw)); err != nil {
			return ""
		}
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || isBingHost(u.Hostname()) {
		return ""
	}
	return u.String()
}

func isBingHost(h string) bool {
	h = strings.ToLower(h)
	return h == "bing.com" || strings.HasSuffix(h, ".bing.com")
}
