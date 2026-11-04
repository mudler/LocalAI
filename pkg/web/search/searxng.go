package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxSearXNGBody bounds the answer of a SearXNG instance.
const maxSearXNGBody = 1 << 20

type searXNG struct {
	base *url.URL
	hc   *http.Client
}

// NewSearXNG returns the provider of the SearXNG instance at baseURL. The URL
// is an administrator's setting, never a model's argument, so the instance
// may be on the local network; the client follows no redirect and reads at
// most 1 MiB.
func NewSearXNG(baseURL string) (Provider, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, errors.New("the SearXNG URL must be an http or https address such as https://searx.example.org")
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	hc := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
		},
	}
	return &searXNG{base: u, hc: hc}, nil
}

func (*searXNG) Name() string   { return "searxng" }
func (s *searXNG) Host() string { return s.base.Hostname() }

var searxFreshness = map[string]string{"day": "day", "week": "week", "month": "month", "year": "year"}

func (s *searXNG) Search(ctx context.Context, q Query) ([]Result, error) {
	v := url.Values{"q": {textWithSite(q)}, "format": {"json"}, "safesearch": {"1"}}
	if f := searxFreshness[q.Freshness]; f != "" {
		v.Set("time_range", f)
	}
	u := *s.base
	u.Path += "/search"
	u.RawQuery = v.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProvider, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.hc.Do(req)
	if err != nil {
		var ne net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
			return nil, ErrTimeout
		}
		return nil, fmt.Errorf("%w: the SearXNG instance could not be reached", ErrProvider)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%w: the SearXNG instance refuses the JSON format; add json to search.formats in its settings.yml", ErrProvider)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w: the SearXNG instance limits this server (429)", ErrProvider)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, fmt.Errorf("%w: the SearXNG instance answered %s", ErrProvider, resp.Status)
	}
	return ParseSearXNG(io.LimitReader(resp.Body, maxSearXNGBody))
}

// ParseSearXNG reads the JSON answer of SearXNG.
func ParseSearXNG(r io.Reader) ([]Result, error) {
	var doc struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: the SearXNG answer is not JSON", ErrProvider)
	}
	out := make([]Result, 0, len(doc.Results))
	seen := map[string]bool{}
	for _, d := range doc.Results {
		u, err := url.Parse(strings.TrimSpace(d.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || seen[u.String()] {
			continue
		}
		seen[u.String()] = true
		title := clean(d.Title, MaxTitle)
		if title == "" {
			title = u.Host
		}
		out = append(out, Result{Title: title, URL: u.String(), Snippet: clean(d.Content, MaxSnippet)})
	}
	return out, nil
}
