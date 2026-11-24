// SPDX-License-Identifier: MIT
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/pkg/web/search"
)

// The documented endpoints. Tests pass the address of a fake.
const (
	BraveEndpoint  = "https://api.search.brave.com/res/v1/web/search"
	TavilyEndpoint = "https://api.tavily.com/search"
	GoogleEndpoint = "https://www.googleapis.com/customsearch/v1"
	// KeenableEndpoint is the search with a key; the keyless search is the
	// same address with /public appended.
	KeenableEndpoint = "https://api.keenable.ai/v1/search"
)

// ErrNoKey is returned by a constructor given an empty or malformed key.
var ErrNoKey = errors.New("the key is missing or not valid")

var keyShape = regexp.MustCompile(`^[\x21-\x7e]{8,256}$`)

// ValidKey reports whether key has the shape of an API key: 8 to 256
// printable ASCII characters, no space.
func ValidKey(key string) bool { return keyShape.MatchString(key) }

var cxShape = regexp.MustCompile(`^[A-Za-z0-9:_-]{1,128}$`)

// ValidEngineID reports whether id has the shape of a Programmable Search
// Engine ID (cx).
func ValidEngineID(id string) bool { return cxShape.MatchString(id) }

// --- Brave Search -------------------------------------------------------

type brave struct {
	keyed
	key string
}

// NewBrave returns the Brave Search provider (the Web Search API). The key is
// sent in the X-Subscription-Token header.
func NewBrave(endpoint, key string) (search.Provider, error) {
	if !ValidKey(key) {
		return nil, ErrNoKey
	}
	k, err := newKeyed(Brave, "Brave Search", endpoint)
	if err != nil {
		return nil, err
	}
	return &brave{keyed: k, key: key}, nil
}

var braveFreshness = map[string]string{"day": "pd", "week": "pw", "month": "pm", "year": "py"}

func (b *brave) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	u := *b.endpoint
	v := u.Query()
	text := q.Text
	if q.Site != "" {
		text += " site:" + q.Site
	}
	v.Set("q", text)
	v.Set("count", strconv.Itoa(q.MaxResults))
	v.Set("safesearch", "moderate")
	if f := braveFreshness[q.Freshness]; f != "" {
		v.Set("freshness", f)
	}
	u.RawQuery = v.Encode()
	req, err := newRequest(ctx, http.MethodGet, &u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Subscription-Token", b.key)
	body, err := b.do(req)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return nil, b.badJSON()
	}
	r := doc.Web.Results
	return results(len(r), func(i int) (string, string, string) { return r[i].Title, r[i].URL, r[i].Description }), nil
}

// --- Tavily ---------------------------------------------------------------

type tavily struct {
	keyed
	key string
}

// NewTavily returns the Tavily search provider. The key is sent as a bearer
// token in the Authorization header.
func NewTavily(endpoint, key string) (search.Provider, error) {
	if !ValidKey(key) {
		return nil, ErrNoKey
	}
	k, err := newKeyed(Tavily, "Tavily", endpoint)
	if err != nil {
		return nil, err
	}
	return &tavily{keyed: k, key: key}, nil
}

func (t *tavily) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	in := map[string]any{"query": q.Text, "max_results": q.MaxResults, "search_depth": "basic"}
	if q.Freshness != "" {
		in["time_range"] = q.Freshness
	}
	if q.Site != "" {
		in["include_domains"] = []string{q.Site}
	}
	raw, _ := json.Marshal(in)
	req, err := newRequest(ctx, http.MethodPost, t.endpoint, raw)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.key)
	body, err := t.do(req)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return nil, t.badJSON()
	}
	r := doc.Results
	return results(len(r), func(i int) (string, string, string) { return r[i].Title, r[i].URL, r[i].Content }), nil
}

// --- Google Programmable Search -------------------------------------------

type google struct {
	keyed
	key, cx string
}

// NewGoogle returns the Google Programmable Search provider (the Custom
// Search JSON API) for the search engine cx. The key is sent in the
// X-Goog-Api-Key header, not in the URL.
func NewGoogle(endpoint, key, cx string) (search.Provider, error) {
	if !ValidKey(key) {
		return nil, ErrNoKey
	}
	if !ValidEngineID(cx) {
		return nil, fmt.Errorf("the search engine ID is missing or not valid")
	}
	k, err := newKeyed(Google, "Google Programmable Search", endpoint)
	if err != nil {
		return nil, err
	}
	return &google{keyed: k, key: key, cx: cx}, nil
}

var googleFreshness = map[string]string{"day": "d1", "week": "w1", "month": "m1", "year": "y1"}

func (g *google) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	u := *g.endpoint
	v := u.Query()
	v.Set("cx", g.cx)
	v.Set("q", q.Text)
	v.Set("num", strconv.Itoa(q.MaxResults))
	v.Set("safe", "active")
	if f := googleFreshness[q.Freshness]; f != "" {
		v.Set("dateRestrict", f)
	}
	if q.Site != "" {
		v.Set("siteSearch", q.Site)
		v.Set("siteSearchFilter", "i")
	}
	u.RawQuery = v.Encode()
	req, err := newRequest(ctx, http.MethodGet, &u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Goog-Api-Key", g.key)
	body, err := g.do(req)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Items []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"items"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return nil, g.badJSON()
	}
	r := doc.Items
	return results(len(r), func(i int) (string, string, string) { return r[i].Title, r[i].Link, r[i].Snippet }), nil
}

// --- Keenable -------------------------------------------------------------

type keenable struct {
	keyed
	key string
}

// KeenableTitle names LocalAI to Keenable's keyless endpoint, which refuses a
// request without an application name.
const KeenableTitle = "LocalAI"

// NewKeenable returns the Keenable search provider. With a key, the key is
// sent in the X-API-Key header. Without one, the provider calls the keyless
// endpoint (endpoint + "/public"), limited per IP, with the X-Keenable-Title
// header. A key that is set must have the shape of a key.
func NewKeenable(endpoint, key string) (search.Provider, error) {
	if key != "" && !ValidKey(key) {
		return nil, ErrNoKey
	}
	if key == "" {
		endpoint = strings.TrimSuffix(endpoint, "/") + "/public"
	}
	k, err := newKeyed(Keenable, "Keenable", endpoint)
	if err != nil {
		return nil, err
	}
	return &keenable{keyed: k, key: key}, nil
}

// Keenable reads a relative delta as "published at most this long ago".
var keenableFreshness = map[string]string{"day": "1d", "week": "7d", "month": "1mo", "year": "1y"}

func (k *keenable) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	in := map[string]any{"query": q.Text, "max_results": q.MaxResults}
	if f := keenableFreshness[q.Freshness]; f != "" {
		in["published_after"] = f
	}
	if q.Site != "" {
		in["site"] = q.Site
	}
	raw, _ := json.Marshal(in)
	req, err := newRequest(ctx, http.MethodPost, k.endpoint, raw)
	if err != nil {
		return nil, err
	}
	if k.key != "" {
		req.Header.Set("X-API-Key", k.key)
	} else {
		req.Header.Set("X-Keenable-Title", KeenableTitle)
	}
	body, err := k.do(req)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return nil, k.badJSON()
	}
	r := doc.Results
	return results(len(r), func(i int) (string, string, string) { return r[i].Title, r[i].URL, r[i].Snippet }), nil
}
