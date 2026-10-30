// SPDX-License-Identifier: MIT

// Package tools is the runtime of the Web add-on executable. It serves
// search and fetch, which replace the built-in web_search and web_fetch of
// the chats (manifest shadows), and the settings tools of its Settings
// section.
//
// The two chat tools run the code of the built-in tools (pkg/web): the same
// argument checks, the same SSRF guard on every URL and redirect
// (pkg/web/safehttp, in this process: the add-on is trusted code with its own
// network access, and the guard is what keeps a model's URL away from
// internal addresses), the same text extraction and the same untrusted-content
// framing. What the add-on adds is the providers that need a key, tried in the
// administrator's order, and the text of PDF files.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/web/internal/providers"
	"github.com/mudler/LocalAI/addons/web/internal/settings"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/web/safehttp"
	"github.com/mudler/LocalAI/pkg/web/search"
	"github.com/mudler/LocalAI/pkg/web/webtools"
)

// The tools of the manifest.
const (
	ToolSearch      = "search"
	ToolFetch       = "fetch"
	ToolStatus      = "status"
	ToolSetSettings = "set-settings"
	ToolTestSearch  = "test-search"
)

// Declared is everything the manifest lists under capabilities.tools.
func Declared() []string {
	return []string{ToolSearch, ToolFetch, ToolStatus, ToolSetSettings, ToolTestSearch}
}

// MaxDownloadBytes is the download cap of fetch. It is larger than the 2 MiB
// of the built-in web_fetch so that common PDF files fit; the text returned
// has the same limits as the built-in.
const MaxDownloadBytes = 10 << 20

// settingsHint is where an administrator chooses the providers, for the error
// a user reads when none works.
const settingsHint = "an administrator can set the search providers of the Web add-on in Settings"

// Admin tells whether the user of a request is an administrator. The host
// answers; the add-on never decides it.
type Admin interface {
	IsAdmin(ctx context.Context, operationID string) bool
}

// Endpoints are the addresses of the providers. Tests point them at fakes.
type Endpoints struct {
	Brave, Tavily, Google, DuckDuckGo string
}

// DefaultEndpoints are the documented addresses.
func DefaultEndpoints() Endpoints {
	return Endpoints{Brave: providers.BraveEndpoint, Tavily: providers.TavilyEndpoint, Google: providers.GoogleEndpoint, DuckDuckGo: search.DuckDuckGoEndpoint}
}

// Options configure the runtime.
type Options struct {
	Settings *settings.Store
	// Admin may be nil: the settings tools then refuse every change.
	Admin Admin
	// Fetch reads a URL for fetch and for DuckDuckGo. Nil: the guarded
	// client of pkg/web/safehttp with the built-in's policy.
	Fetch webtools.Fetcher
	// Endpoints zero: DefaultEndpoints.
	Endpoints Endpoints
	// PDF reads the text of a PDF. Nil: PDFText.
	PDF func([]byte) (string, error)
	// Limits of the searches. Zero: the built-in's.
	Limits search.Limits
}

// NewFetcher returns the guarded client of fetch: public addresses only,
// ports 80 and 443, at most 5 redirects, each checked, 15 seconds.
func NewFetcher() *safehttp.Client {
	return safehttp.New(safehttp.Policy{
		MaxBodyBytes: MaxDownloadBytes,
		UserAgent:    "LocalAI (web add-on; +https://localai.io)",
	}, nil)
}

type runtime struct {
	o Options

	mu    sync.Mutex
	rev   int
	built bool
	tools webtools.Tools
	chain []search.Provider
}

// New returns the runtime of the executable.
func New(o Options) contract.Runtime {
	if o.Fetch == nil {
		o.Fetch = NewFetcher()
	}
	if o.Endpoints == (Endpoints{}) {
		o.Endpoints = DefaultEndpoints()
	}
	if o.PDF == nil {
		o.PDF = PDFText
	}
	return &runtime{o: o}
}

// build returns the providers of the saved order that are ready, in order,
// and the reasons of those that are in the order but cannot be built.
func (r *runtime) build(s settings.Settings) []search.Provider {
	var out []search.Provider
	for _, n := range s.Order {
		if ok, _ := s.Ready(n); !ok {
			continue
		}
		var p search.Provider
		var err error
		switch n {
		case providers.Brave:
			p, err = providers.NewBrave(r.o.Endpoints.Brave, s.Keys[n])
		case providers.Tavily:
			p, err = providers.NewTavily(r.o.Endpoints.Tavily, s.Keys[n])
		case providers.Google:
			p, err = providers.NewGoogle(r.o.Endpoints.Google, s.Keys[n], s.GoogleEngine)
		case providers.SearXNG:
			p, err = search.NewSearXNG(s.SearXNGURL)
		case providers.DuckDuckGo:
			// Reached through the guarded client: its address is fixed, the
			// redirects it answers with are not.
			p = search.NewDuckDuckGo(r.o.Fetch, r.o.Endpoints.DuckDuckGo)
		}
		if err == nil && p != nil {
			out = append(out, p)
		}
	}
	return out
}

// current returns the tools for the saved settings, building them again after
// a save. The cache and the limits of the searches start again with them.
func (r *runtime) current() (webtools.Tools, []search.Provider) {
	if r.o.Settings == nil {
		return webtools.Tools{Fetch: pdfGuard{r.o.Fetch}, PDF: r.o.PDF, SettingsHint: settingsHint}, nil
	}
	s, rev := r.o.Settings.Get()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.built && rev == r.rev {
		return r.tools, r.chain
	}
	chain := r.build(s)
	t := webtools.Tools{Fetch: pdfGuard{r.o.Fetch}, PDF: r.o.PDF, SettingsHint: settingsHint}
	if len(chain) > 0 {
		t.Search = search.NewSearcher(search.NewFallback(0, chain...), r.o.Limits)
	}
	r.tools, r.chain, r.rev, r.built = t, chain, rev, true
	return t, chain
}

// pdfGuard refuses a PDF that the download cap cut: the rest of the file is
// missing, so its text cannot be read.
type pdfGuard struct{ f webtools.Fetcher }

func (g pdfGuard) Get(ctx context.Context, raw string, accept []string) (*safehttp.Response, error) {
	resp, err := g.f.Get(ctx, raw, accept)
	if err == nil && resp.Truncated && resp.ContentType == "application/pdf" {
		return nil, fmt.Errorf("the PDF is larger than %d MiB, so it was not read", MaxDownloadBytes>>20)
	}
	return resp, err
}

var operationShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func answer(out string) contract.Result {
	return contract.Result{Payload: protocol.Payload(map[string]any{"ok": true, "output": out})}
}

func refuse(msg string) contract.Result {
	return contract.Result{Payload: protocol.Payload(map[string]any{"ok": false, "error": msg})}
}

// Read serves every tool: the two chat tools, which the host runs as reads
// without a resource, and the settings tools of the page.
func (r *runtime) Read(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	var head struct {
		Tool string `json:"tool"`
	}
	if json.Unmarshal(env.Request.Arguments, &head) != nil || head.Tool == "" {
		return contract.Result{}, protocol.ErrBadRequest
	}
	op := env.Request.OperationID
	if !operationShape.MatchString(op) {
		return contract.Result{}, protocol.ErrBadRequest
	}
	switch head.Tool {
	case ToolSearch:
		return r.search(ctx, env)
	case ToolFetch:
		return r.fetch(ctx, env)
	case ToolStatus, ToolSetSettings, ToolTestSearch:
		return r.settingsTool(ctx, env, head.Tool, op)
	}
	return contract.Result{}, protocol.ErrBadRequest
}

// chatError is the answer of a chat tool that failed in a way the model can
// read and tell the user. A stopped turn stays an error for the host.
func chatError(err error) (contract.Result, error) {
	if errors.Is(err, context.Canceled) {
		return contract.Result{}, err
	}
	return refuse(webtools.Sanitize(err.Error())), nil
}

func (r *runtime) search(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	var a struct {
		Tool       string `json:"tool"`
		Query      string `json:"query"`
		MaxResults int    `json:"maxResults,omitempty"`
		Freshness  string `json:"freshness,omitempty"`
		Site       string `json:"site,omitempty"`
	}
	if err := strict(env.Request.Arguments, &a); err != nil {
		return refuse(err.Error()), nil
	}
	// The manifest names the arguments in camel case (the contract allows no
	// underscore); the built-in code reads its own names.
	raw := protocol.Payload(map[string]any{"query": a.Query, "max_results": a.MaxResults, "freshness": a.Freshness, "site": a.Site})
	t, _ := r.current()
	out, err := t.WebSearch(ctx, env.Authority.Actor.UserID, omitZero(raw))
	if err != nil {
		return chatError(err)
	}
	return answer(out), nil
}

func (r *runtime) fetch(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	var a struct {
		Tool     string `json:"tool"`
		URL      string `json:"url"`
		MaxChars int    `json:"maxChars,omitempty"`
		Format   string `json:"format,omitempty"`
	}
	if err := strict(env.Request.Arguments, &a); err != nil {
		return refuse(err.Error()), nil
	}
	raw := protocol.Payload(map[string]any{"url": a.URL, "max_chars": a.MaxChars, "format": a.Format})
	t, _ := r.current()
	out, err := t.WebFetch(ctx, omitZero(raw))
	if err != nil {
		return chatError(err)
	}
	return answer(out), nil
}

// omitZero drops the zero values of an argument object, which the built-in
// code reads as "not given".
func omitZero(raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	for k, v := range m {
		if v == "" || v == float64(0) {
			delete(m, k)
		}
	}
	return protocol.Payload(m)
}

func strict(raw json.RawMessage, v any) error {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", webtools.ErrArguments, err)
	}
	return nil
}

// TestQuery is the search of test-search when the page gives none.
const TestQuery = "LocalAI"

// ProviderTest is the result of one provider in test-search.
type ProviderTest struct {
	Name    string `json:"name"`
	Ready   bool   `json:"ready"`
	OK      bool   `json:"ok"`
	Results int    `json:"results"`
	// Host is the host that answered.
	Host string `json:"host,omitempty"`
	// Error is why it failed or, for a provider that is not ready, what it
	// needs. It never holds a key.
	Error string `json:"error,omitempty"`
}

func (r *runtime) settingsTool(ctx context.Context, env contract.Envelope, tool, op string) (contract.Result, error) {
	admin := r.o.Admin != nil && r.o.Admin.IsAdmin(ctx, op)
	if r.o.Settings == nil {
		return refuse("the Web add-on cannot read its settings"), nil
	}
	switch tool {
	case ToolStatus:
		_, chain := r.current()
		var serving []string
		for _, p := range chain {
			serving = append(serving, p.Name())
		}
		out := map[string]any{"ok": true, "admin": admin, "serving": serving}
		if admin {
			out["settings"] = r.o.Settings.View()
		}
		return contract.Result{Payload: protocol.Payload(out)}, nil
	case ToolSetSettings:
		if !admin {
			return refuse("only an administrator can change this setting"), nil
		}
		var a struct {
			Tool string `json:"tool"`
			settings.Change
		}
		if err := strict(env.Request.Arguments, &a); err != nil {
			return refuse("the settings are not valid"), nil
		}
		if err := r.o.Settings.Apply(a.Change); err != nil {
			return refuse(err.Error()), nil
		}
		return contract.Result{Payload: protocol.Payload(map[string]any{"ok": true, "settings": r.o.Settings.View()})}, nil
	case ToolTestSearch:
		if !admin {
			return refuse("only an administrator can test the providers"), nil
		}
		var a struct {
			Tool  string `json:"tool"`
			Query string `json:"query,omitempty"`
		}
		if err := strict(env.Request.Arguments, &a); err != nil {
			return refuse("the test is not valid"), nil
		}
		return contract.Result{Payload: protocol.Payload(map[string]any{"ok": true, "providers": r.test(ctx, a.Query)})}, nil
	}
	return contract.Result{}, protocol.ErrBadRequest
}

// test runs one search with each provider of the order, one after the other,
// outside the cache and the limits: it is the administrator's check of each
// key, not a chat's search.
func (r *runtime) test(ctx context.Context, query string) []ProviderTest {
	if strings.TrimSpace(query) == "" {
		query = TestQuery
	}
	s, _ := r.o.Settings.Get()
	q, err := search.Normalize(search.Query{Text: query, MaxResults: 3})
	var out []ProviderTest
	for _, n := range s.Order {
		pt := ProviderTest{Name: n}
		ready, missing := s.Ready(n)
		pt.Ready = ready
		if !ready {
			pt.Error = missing
			out = append(out, pt)
			continue
		}
		if err != nil {
			pt.Error = webtools.Sanitize(err.Error())
			out = append(out, pt)
			continue
		}
		built := r.build(settings.Settings{Order: []string{n}, Keys: s.Keys, GoogleEngine: s.GoogleEngine, SearXNGURL: s.SearXNGURL})
		if len(built) == 0 {
			pt.Error = "it could not be set up"
			out = append(out, pt)
			continue
		}
		res, from, serr := search.NewFallback(0, built[0]).SearchFrom(ctx, q)
		if serr != nil {
			pt.Error = webtools.Sanitize(strings.TrimPrefix(serr.Error(), search.ErrProvider.Error()+": "+n+": "))
		} else {
			pt.OK, pt.Results, pt.Host = true, len(res), from.Host()
		}
		out = append(out, pt)
	}
	return out
}

func (*runtime) Preview(context.Context, contract.Envelope) (contract.Preview, error) {
	return contract.Preview{}, errNoTool
}

func (*runtime) Apply(context.Context, contract.Envelope, contract.Revision) (contract.Receipt, error) {
	return contract.Receipt{}, errNoTool
}

func (*runtime) Receipt(context.Context, string) (contract.Receipt, error) {
	return contract.Receipt{}, errNoTool
}

func (*runtime) OpenOutput(context.Context, contract.Envelope, string) (io.ReadCloser, error) {
	return nil, errNoTool
}

var errNoTool = errors.New("this operation is not served by the web add-on")
