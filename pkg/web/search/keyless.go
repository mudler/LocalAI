package search

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/net/html"

	"github.com/mudler/LocalAI/pkg/web/safehttp"
)

// The names of the keyless providers, as the settings list them.
const (
	KeylessDuckDuckGo = "duckduckgo"
	KeylessBrave      = "brave_html"
	KeylessBing       = "bing_html"
)

// KeylessOrder is the default order of the keyless providers. No engine
// answers every server every time (each may answer a bot check to an address
// it does not trust), so the built-in search tries them in turn.
var KeylessOrder = []string{KeylessDuckDuckGo, KeylessBrave, KeylessBing}

// KeylessEndpoints are the pages the keyless providers read. Tests point them
// at a local server.
type KeylessEndpoints struct {
	DuckDuckGo, Brave, Bing string
}

// DefaultKeylessEndpoints are the public pages.
func DefaultKeylessEndpoints() KeylessEndpoints {
	return KeylessEndpoints{DuckDuckGo: DuckDuckGoEndpoint, Brave: BraveEndpoint, Bing: BingEndpoint}
}

// IsKeyless says whether name is a keyless provider.
func IsKeyless(name string) bool {
	for _, n := range KeylessOrder {
		if n == name {
			return true
		}
	}
	return false
}

// NewKeyless returns the keyless provider of that name, reached through g.
func NewKeyless(name string, g Getter, e KeylessEndpoints) (Provider, error) {
	switch name {
	case KeylessDuckDuckGo:
		return NewDuckDuckGo(g, e.DuckDuckGo), nil
	case KeylessBrave:
		return NewBrave(g, e.Brave), nil
	case KeylessBing:
		return NewBing(g, e.Bing), nil
	}
	return nil, fmt.Errorf("%w: %q is not a keyless search provider", ErrProvider, name)
}

// fetchPage gets a result page. A refusal (403, 429) is ErrBlocked, so the
// chain can put the provider in its cool-down.
func fetchPage(ctx context.Context, g Getter, raw string) (*safehttp.Response, error) {
	resp, err := g.Get(ctx, raw, []string{"text/html"})
	if err == nil {
		return resp, nil
	}
	if errors.Is(err, safehttp.ErrTimeout) {
		return nil, ErrTimeout
	}
	var se *safehttp.StatusError
	if errors.As(err, &se) && (se.Code == http.StatusForbidden || se.Code == http.StatusTooManyRequests) {
		return nil, fmt.Errorf("%w: %w (%s)", ErrProvider, ErrBlocked, se.Status)
	}
	return nil, fmt.Errorf("%w: %v", ErrProvider, err)
}

// challenged is the error of a bot check.
func challenged() error { return fmt.Errorf("%w: %w", ErrProvider, ErrChallenged) }

// unreadable is the error of a page with neither results nor a marker of an
// empty search: a new layout, most likely.
func unreadable() error {
	return fmt.Errorf("%w: the page had no results it could read", ErrProvider)
}

// challengeMarkers are words that the bot checks of the engines put in an id,
// a class or a form action. They are looked for only on a page without
// results, so a result about captchas is not a challenge.
var challengeMarkers = []string{"captcha", "anomaly", "challenge"}

// hasChallenge says whether doc looks like a bot check.
func hasChallenge(doc *html.Node) bool {
	found := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found {
			return
		}
		if n.Type == html.ElementNode {
			v := strings.ToLower(attrOf(n, "id") + " " + attrOf(n, "class") + " " + attrOf(n, "action") + " " + attrOf(n, "src"))
			for _, m := range challengeMarkers {
				if strings.Contains(v, m) {
					found = true
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return found
}

// firstByClass returns the first element under n that has one of the
// classes, depth first.
func firstByClass(n *html.Node, names ...string) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode {
			cls := classes(n)
			for _, c := range names {
				if cls[c] {
					found = n
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}

// firstElement returns the first element under n for which match holds.
func firstElement(n *html.Node, match func(*html.Node) bool) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && match(n) {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}
