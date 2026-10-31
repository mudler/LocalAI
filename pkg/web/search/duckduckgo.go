package search

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/mudler/LocalAI/pkg/web/safehttp"
)

// DuckDuckGoEndpoint is the HTML endpoint of DuckDuckGo. It needs no key.
const DuckDuckGoEndpoint = "https://html.duckduckgo.com/html/"

// Getter fetches a page. *safehttp.Client implements it.
type Getter interface {
	Get(ctx context.Context, raw string, accept []string) (*safehttp.Response, error)
}

type duckDuckGo struct {
	get      Getter
	endpoint string
}

// NewDuckDuckGo returns the DuckDuckGo provider. endpoint is
// DuckDuckGoEndpoint outside tests.
func NewDuckDuckGo(g Getter, endpoint string) Provider {
	return &duckDuckGo{get: g, endpoint: endpoint}
}

func (*duckDuckGo) Name() string { return "duckduckgo" }
func (*duckDuckGo) Host() string { return "duckduckgo.com" }

var ddgFreshness = map[string]string{"day": "d", "week": "w", "month": "m", "year": "y"}

func (d *duckDuckGo) Search(ctx context.Context, q Query) ([]Result, error) {
	v := url.Values{"q": {textWithSite(q)}}
	if f := ddgFreshness[q.Freshness]; f != "" {
		v.Set("df", f)
	}
	resp, err := fetchPage(ctx, d.get, d.endpoint+"?"+v.Encode())
	if err != nil {
		return nil, err
	}
	res, err := ParseDuckDuckGo(resp.Body)
	if err != nil && resp.StatusCode == http.StatusAccepted {
		// DuckDuckGo answers its bot check with 202 instead of 200.
		return nil, challenged()
	}
	return res, err
}

// ParseDuckDuckGo reads the results of a DuckDuckGo HTML page. Ads are
// dropped and redirect links are unwrapped. A page with neither results nor
// the "no results" marker is ErrProvider, so a change on their side reads as
// "the provider does not work" and never as "nothing found"; when it is the
// bot check (the "anomaly" page) the error is also ErrChallenged.
func ParseDuckDuckGo(page []byte) ([]Result, error) {
	doc, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		return nil, fmt.Errorf("%w: the page could not be parsed", ErrProvider)
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
			if cls["no-results"] {
				noResults = true
			}
			if cls["result"] && !cls["result--ad"] && n.DataAtom != atom.A {
				if r, ok := ddgResult(n); ok && !seen[r.URL] {
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
	if len(out) == 0 && !noResults {
		if hasChallenge(doc) {
			return nil, challenged()
		}
		return nil, unreadable()
	}
	return out, nil
}

func ddgResult(n *html.Node) (Result, bool) {
	var r Result
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			cls := classes(n)
			switch {
			case cls["result__a"] && r.URL == "":
				r.Title = clean(text(n), MaxTitle)
				r.URL = unwrapDDG(attrOf(n, "href"))
				return
			case cls["result__snippet"] && r.Snippet == "":
				r.Snippet = clean(text(n), MaxSnippet)
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return r, r.URL != "" && r.Title != ""
}

// unwrapDDG turns "//duckduckgo.com/l/?uddg=<url>&rut=..." into <url>. Only
// http and https targets are kept.
func unwrapDDG(href string) string {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return ""
	}
	if strings.HasSuffix(u.Host, "duckduckgo.com") && strings.HasPrefix(u.Path, "/l/") {
		target := u.Query().Get("uddg")
		if u, err = url.Parse(target); err != nil {
			return ""
		}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	if strings.HasSuffix(u.Host, "duckduckgo.com") && strings.HasPrefix(u.Path, "/y.js") {
		return "" // an ad redirect
	}
	return u.String()
}

func classes(n *html.Node) map[string]bool {
	out := map[string]bool{}
	for f := range strings.FieldsSeq(attrOf(n, "class")) {
		out[f] = true
	}
	return out
}

func attrOf(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style) {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
