// SPDX-License-Identifier: MIT

// Package providers holds the search providers of the Web add-on that need a
// key: Brave Search, Tavily and Google Programmable Search. Each implements
// search.Provider of pkg/web/search, next to the keyless DuckDuckGo and
// SearXNG providers that the built-in web_search uses.
//
// A key is a secret. It travels only in a request header to the provider's
// fixed endpoint, never in a URL, and no error text holds it: the errors are
// sentences built here from the status code, never from the transport error
// (which names the URL) or the answer body. The clients follow no redirect,
// so a provider cannot send the key header on to another host.
package providers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/web/search"
)

// The provider names. They are the values of the add-on's order setting.
const (
	Brave   = "brave"
	Tavily  = "tavily"
	Google  = "google"
	SearXNG = "searxng"
	// DuckDuckGo is the keyless entry. Its name stays for the saved
	// settings; it stands for the whole keyless chain of the built-in
	// search (DuckDuckGo, then the Brave Search and Bing pages).
	DuckDuckGo = "duckduckgo"
)

// Names lists every provider, in the default order: the keyed ones first,
// then SearXNG, then the keyless chain, which needs nothing.
var Names = []string{Brave, Tavily, Google, SearXNG, DuckDuckGo}

// Keyed reports whether the provider needs a key.
func Keyed(name string) bool { return name == Brave || name == Tavily || name == Google }

// maxBody bounds the answer of a provider.
const maxBody = 1 << 20

// newClient returns the client of the keyed providers: no proxy (the add-on
// has no proxy variables anyway), no redirect, bounded times.
func newClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			MaxIdleConns:          8,
			IdleConnTimeout:       30 * time.Second,
		},
	}
}

// keyed is what the three keyed providers share.
type keyed struct {
	name, label string
	endpoint    *url.URL
	hc          *http.Client
}

func newKeyed(name, label, endpoint string) (keyed, error) {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return keyed{}, fmt.Errorf("%s: the endpoint is not an http(s) URL", name)
	}
	return keyed{name: name, label: label, endpoint: u, hc: newClient()}, nil
}

func (k keyed) Name() string { return k.name }
func (k keyed) Host() string { return k.endpoint.Hostname() }

// do sends req and returns the body of a 2xx answer. Every error is a
// sentence of this package: it never holds the URL, a header or the body.
func (k keyed) do(req *http.Request) ([]byte, error) {
	resp, err := k.hc.Do(req)
	if err != nil {
		var ne net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
			return nil, search.ErrTimeout
		}
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, fmt.Errorf("%w: %s could not be reached", search.ErrProvider, k.label)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: %s refused the key (%d)", search.ErrProvider, k.label, resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w: %s says the key reached its limit (429)", search.ErrProvider, k.label)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Errorf("%w: %s answered %d", search.ErrProvider, k.label, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%w: the answer of %s could not be read", search.ErrProvider, k.label)
	}
	return b, nil
}

func (k keyed) badJSON() error {
	return fmt.Errorf("%w: the answer of %s is not the JSON it documents", search.ErrProvider, k.label)
}

func newRequest(ctx context.Context, method string, u *url.URL, body []byte) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), r)
	if err != nil {
		return nil, fmt.Errorf("%w: the request could not be built", search.ErrProvider)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "LocalAI (web add-on; +https://localai.io)")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

var tags = regexp.MustCompile(`<[^>]*>`)

// text turns a field that may hold markup (Brave marks the matched words with
// <strong>) into plain text.
func text(s string, max int) string {
	return search.Clean(html.UnescapeString(tags.ReplaceAllString(s, "")), max)
}

// results keeps the http(s) links once each and cuts the fields.
func results(n int, get func(i int) (title, link, snippet string)) []search.Result {
	out := make([]search.Result, 0, n)
	seen := map[string]bool{}
	for i := range n {
		title, link, snippet := get(i)
		u, err := url.Parse(strings.TrimSpace(link))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || seen[u.String()] {
			continue
		}
		seen[u.String()] = true
		t := text(title, search.MaxTitle)
		if t == "" {
			t = u.Host
		}
		out = append(out, search.Result{Title: t, URL: u.String(), Snippet: text(snippet, search.MaxSnippet)})
	}
	return out
}
