// SPDX-License-Identifier: MIT

// Package oauthtest is a fake OAuth 2.0 service for conformance tests. A
// Provider serves an authorize, a token and a revocation endpoint over TLS on
// a loopback address, under the host name of a descriptor. It checks what a
// real service checks (PKCE, the redirect address, the client authentication
// the descriptor names) and has knobs for the answers a host must handle:
// rotation of the refresh token, short tokens, invalid_grant, 429 with
// Retry-After, 5xx, fewer scopes than asked and a person who declines.
//
// Client gives a token client that reaches every provider under its own host
// name, so the guarded client of the code under test is only replaced at the
// dialer and the checks of the descriptor stay in place.
package oauthtest

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/oauthtoken"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// Sentinel is the error_description of every error answer. A host must never
// copy it into a URL, a page, a log or a tool result.
const Sentinel = "provider-description-sentinel"

// Client authentications as the provider saw them on the wire.
const (
	SeenBasic   = "basic"
	SeenPost    = "post"
	SeenNone    = "none"
	SeenInvalid = "invalid"
)

// Config is the client a provider knows and the authentication it requires.
type Config struct {
	// Host is the host name of the endpoints, for example
	// "login.provider-a.example.test".
	Host string
	// ClientID and ClientSecret are the registered client. ClientSecret is
	// empty for a public client.
	ClientID, ClientSecret string
	// ClientAuth is the authentication the token and revocation endpoints
	// require: contract.OAuthClientAuthBasic, Post or None. Any other form
	// is refused with invalid_client.
	ClientAuth string
	// AuthorizePath is the path of the authorize endpoint. Empty means
	// /authorize. A service whose descriptor puts it elsewhere sets it.
	AuthorizePath string
}

// Call is one call of the token or the revocation endpoint.
type Call struct {
	// Endpoint is "token" or "revoke"; Host is the host the request named.
	Endpoint, Host string
	// Auth is how the client authenticated: SeenBasic, SeenPost, SeenNone or
	// SeenInvalid.
	Auth string
	Form url.Values
}

// Provider is one fake service. Set its knobs with Set.
type Provider struct {
	*httptest.Server
	cfg Config

	mu sync.Mutex
	// Rotate issues a new refresh token on every refresh and refuses a used
	// one with invalid_grant.
	Rotate bool
	// ExpiresIn is the expires_in of a token answer; zero leaves it out.
	ExpiresIn int
	// Grant, when set, is the scope of a token answer, so the service can
	// grant fewer scopes than asked. Empty answers with the scopes asked.
	Grant []string
	// OmitScope leaves the scope out of a token answer.
	OmitScope bool
	// Deny makes the authorize endpoint send the person back with
	// error=access_denied, as when the person declines.
	Deny bool
	// Fail, when set, is the OAuth error code of every token answer, with
	// FailStatus (400 when zero) and, for a 429, RetryAfter.
	Fail       string
	FailStatus int
	RetryAfter string
	// Gate, when set, holds every token answer until it is closed or the
	// request ends. A test closes it when it has seen what it wanted to see.
	Gate chan struct{}

	// Tokens counts the calls of the token endpoint.
	Tokens atomic.Int32
	calls  []Call
	n      int
	codes  map[string]pending
	live   map[string]bool // refresh tokens that work
	issued []string        // every access, refresh and code issued
}

type pending struct {
	challenge, redirect, clientID string
	scopes                        []string
}

// New starts a provider. It stops when the spec ends.
func New(cfg Config) *Provider {
	p := &Provider{cfg: cfg, codes: map[string]pending{}, live: map[string]bool{}}
	mux := http.NewServeMux()
	authorizePath := cfg.AuthorizePath
	if authorizePath == "" {
		authorizePath = "/authorize"
	}
	mux.HandleFunc(authorizePath, p.authorize)
	mux.HandleFunc("/token", p.token)
	mux.HandleFunc("/revoke", p.revoke)
	p.Server = httptest.NewTLSServer(mux)
	return p
}

// Config returns the configuration of the provider.
func (p *Provider) Config() Config { return p.cfg }

// URL is the address of an endpoint ("authorize", "token", "revoke") under
// the host of the provider.
func (p *Provider) URL(endpoint string) string { return "https://" + p.cfg.Host + "/" + endpoint }

// Set changes knobs under the provider's lock.
func (p *Provider) Set(f func(p *Provider)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f(p)
}

// Calls returns the calls of the token and revocation endpoints so far.
func (p *Provider) Calls() []Call {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

// Issued returns every code, access token and refresh token the provider
// issued, so a test can check that none reached a place it must not.
func (p *Provider) Issued() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.issued)
}

// LastAccess is the last access token issued, or "".
func (p *Provider) LastAccess() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.issued) - 1; i >= 0; i-- {
		if strings.HasPrefix(p.issued[i], p.prefix("access")) {
			return p.issued[i]
		}
	}
	return ""
}

// Live reports whether a refresh token still works at the provider.
func (p *Provider) Live(refresh string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.live[refresh]
}

// RevokeAll makes every refresh token stop working, as when the person
// revokes the access at the service.
func (p *Provider) RevokeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	clear(p.live)
}

func (p *Provider) prefix(kind string) string {
	return strings.SplitN(p.cfg.Host, ".", 2)[0] + "-" + kind + "-"
}

func (p *Provider) next(kind string) string {
	p.n++
	v := fmt.Sprintf("%s%d", p.prefix(kind), p.n)
	p.issued = append(p.issued, v)
	return v
}

// authorize plays the person: it approves at once, or declines with Deny,
// and redirects to the redirect address with the code or the error.
func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.Scheme == "" || q.Get("state") == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	back := redirect.Query()
	back.Set("state", q.Get("state"))
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case q.Get("client_id") != p.cfg.ClientID:
		back.Set("error", "unauthorized_client")
		back.Set("error_description", Sentinel)
	case q.Get("response_type") != "code" || q.Get("code_challenge_method") != contract.OAuthPKCES256 || q.Get("code_challenge") == "":
		back.Set("error", "invalid_request")
		back.Set("error_description", Sentinel)
	case p.Deny:
		back.Set("error", "access_denied")
		back.Set("error_description", Sentinel)
	default:
		code := p.next("code")
		p.codes[code] = pending{challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), clientID: q.Get("client_id"), scopes: strings.Fields(q.Get("scope"))}
		back.Set("code", code)
	}
	redirect.RawQuery = back.Encode()
	// #nosec G710 -- a fake provider for tests: it sends the browser back to the redirect address of the request, as a real provider does after it checks the registered one
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

// clientAuth reports how the request authenticated and whether that is the
// authentication and the client the provider requires.
func (p *Provider) clientAuth(r *http.Request) (string, bool) {
	id, secret, basic := r.BasicAuth()
	form := r.PostForm
	_, hasSecret := form["client_secret"]
	seen := SeenInvalid
	switch {
	case basic && !hasSecret:
		seen = SeenBasic
		// RFC 6749 section 2.3.1: both parts are form-encoded.
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	case !basic && r.Header.Get("Authorization") == "" && hasSecret:
		seen = SeenPost
		id, secret = form.Get("client_id"), form.Get("client_secret")
	case !basic && r.Header.Get("Authorization") == "" && !hasSecret:
		seen = SeenNone
		id = form.Get("client_id")
	}
	ok := seen == p.cfg.ClientAuth && id == p.cfg.ClientID && secret == p.cfg.ClientSecret
	return seen, ok
}

func (p *Provider) record(endpoint string, r *http.Request) (string, bool) {
	_ = r.ParseForm()
	seen, ok := p.clientAuth(r)
	p.calls = append(p.calls, Call{Endpoint: endpoint, Host: r.Host, Auth: seen, Form: r.PostForm})
	return seen, ok
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	p.Tokens.Add(1)
	p.mu.Lock()
	gate := p.Gate
	p.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, authOK := p.record("token", r)
	if r.Method != http.MethodPost {
		oauthError(w, http.StatusMethodNotAllowed, "invalid_request")
		return
	}
	if p.Fail != "" {
		status := p.FailStatus
		if status == 0 {
			status = http.StatusBadRequest
		}
		if p.RetryAfter != "" {
			w.Header().Set("Retry-After", p.RetryAfter)
		}
		oauthError(w, status, p.Fail)
		return
	}
	if !authOK {
		oauthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	form := r.PostForm
	var scopes []string
	refresh := ""
	switch form.Get("grant_type") {
	case "authorization_code":
		code := form.Get("code")
		pend, ok := p.codes[code]
		delete(p.codes, code)
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		if !ok || pend.redirect != form.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(sum[:]) != pend.challenge {
			oauthError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		scopes = pend.scopes
		refresh = p.next("refresh")
		p.live[refresh] = true
	case "refresh_token":
		old := form.Get("refresh_token")
		if !p.live[old] {
			oauthError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		if p.Rotate {
			delete(p.live, old)
			refresh = p.next("refresh")
			p.live[refresh] = true
		}
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	if len(p.Grant) > 0 {
		scopes = p.Grant
	}
	body := map[string]any{"access_token": p.next("access"), "token_type": "Bearer"}
	if refresh != "" {
		body["refresh_token"] = refresh
	}
	if p.ExpiresIn != 0 {
		body["expires_in"] = p.ExpiresIn
	}
	if !p.OmitScope && len(scopes) > 0 {
		body["scope"] = strings.Join(scopes, " ")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(body)
}

func (p *Provider) revoke(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.record("revoke", r); !ok {
		oauthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	delete(p.live, r.PostForm.Get("token"))
}

func oauthError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": Sentinel})
}

// Network routes the host names of providers to their loopback listeners.
// It stands in for DNS and for the public internet, nothing else: the TLS
// certificate is still verified for the host each request names.
type Network struct {
	mu    sync.Mutex
	addrs map[string]string
	roots *x509.CertPool
}

// NewNetwork returns a network that reaches the providers.
func NewNetwork(ps ...*Provider) *Network {
	n := &Network{addrs: map[string]string{}, roots: x509.NewCertPool()}
	for _, p := range ps {
		n.Add(p.cfg.Host, p)
	}
	return n
}

// Add makes host reach the provider p; a provider can answer under several
// host names.
func (n *Network) Add(host string, p *Provider) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.addrs[host] = p.Listener.Addr().String()
	n.roots.AddCert(p.Certificate())
}

func (n *Network) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	target, ok := n.addrs[host]
	n.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("oauthtest: no provider answers for %s", host)
	}
	var d net.Dialer
	return d.DialContext(ctx, network, target)
}

// transport is a hardened transport whose dialer reaches the providers. The
// test certificate of httptest is issued for example.com and its subdomains,
// so the name checked is the one the certificate carries.
func (n *Network) transport() *http.Transport {
	t := httpclient.HardenedTransport()
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: n.roots, ServerName: "example.com"}
	t.DialContext = n.dial
	return t
}

// TokenClient is a token client of the host that reaches the providers.
func (n *Network) TokenClient() *oauthtoken.Client {
	return &oauthtoken.Client{HTTP: httpclient.NewWithTimeout(oauthtoken.Timeout, httpclient.WithTransport(n.transport()))}
}

// Browser is a client that plays the person's browser at the providers: it
// follows no redirect, so the caller sees where the provider sends it.
func (n *Network) Browser() *http.Client {
	return httpclient.NewWithTimeout(10*time.Second, httpclient.WithTransport(n.transport()))
}

// Authorize opens an authorize address in the browser and returns the
// address the provider sends the person back to.
func (n *Network) Authorize(ctx context.Context, authorizeURL string) (*url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authorizeURL, nil)
	if err != nil {
		return nil, err
	}
	// The client refuses to follow the redirect and hands back the answer
	// that carries it.
	resp, err := n.Browser().Do(req)
	if err != nil && (resp == nil || !errors.Is(err, httpclient.ErrRedirectBlocked)) {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		return nil, fmt.Errorf("oauthtest: authorize answered %d", resp.StatusCode)
	}
	return url.Parse(resp.Header.Get("Location"))
}
