// SPDX-License-Identifier: MIT

// Package oauthtokentest is a fake OAuth 2.0 token and revocation endpoint for
// tests. It serves TLS on a loopback address and gives a client that reaches it
// under the host names of a descriptor, so the guarded client and the
// descriptor checks of the code under test stay in place.
package oauthtokentest

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/services/studio/oauthtoken"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// Host is the host name the descriptor of Descriptor uses.
const Host = "auth.example.test"

// Server is the fake endpoint. Set its knobs before or between calls.
type Server struct {
	*httptest.Server

	mu sync.Mutex
	// Rotate issues a new refresh token on every refresh and refuses a used
	// one with invalid_grant, the way a rotating service does.
	Rotate bool
	// ExpiresIn is the expires_in of a token response; zero leaves it out.
	ExpiresIn int
	// Scope is the scope of a token response; empty leaves it out.
	Scope string
	// TokenType is the token_type; empty means "Bearer".
	TokenType string
	// Fail, when set, is the OAuth error code of every token answer, with
	// FailStatus (400 when zero).
	Fail       string
	FailStatus int
	// RetryAfter is the Retry-After header of a 429 answer.
	RetryAfter string
	// Gate, when set, holds every token answer until it is closed or the
	// request ends. A test closes it when it has seen what it wanted to see.
	Gate chan struct{}
	// Body, when set, replaces the body of a successful token answer.
	Body string
	// RevokeStatus is the status of a revocation answer; zero means 200.
	RevokeStatus int

	// Tokens counts the calls of the token endpoint; Revokes those of the
	// revocation endpoint.
	Tokens, Revokes atomic.Int32
	forms           []url.Values
	auths           []string
	n               int
	live            map[string]bool // refresh tokens that work
}

// New starts a server. It stops when the spec ends: pass DeferCleanup's
// registration function, or call Close.
func New() *Server {
	s := &Server{live: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", s.token)
	mux.HandleFunc("/revoke", s.revoke)
	s.Server = httptest.NewTLSServer(mux)
	return s
}

// Descriptor is a valid descriptor whose URLs point at the server.
func (s *Server) Descriptor(clientAuth string) contract.OAuthDescriptor {
	return contract.OAuthDescriptor{
		AuthorizeURL: "https://" + Host + "/authorize", TokenURL: "https://" + Host + "/token", RevokeURL: "https://" + Host + "/revoke",
		Hosts: []string{Host}, Scopes: contract.OAuthScopes{Required: []string{"read"}, Optional: []string{"write"}},
		PKCE: contract.OAuthPKCES256, ClientAuth: clientAuth,
	}
}

// Client is a token client whose connections all go to the server, whatever
// host they name. It trusts the server's certificate for that host only.
func (s *Server) Client() *oauthtoken.Client {
	base := s.Server.Client().Transport.(*http.Transport)
	t := httpclient.HardenedTransport()
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: base.TLSClientConfig.RootCAs, ServerName: "example.com"}
	addr := s.Listener.Addr().String()
	t.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	return &oauthtoken.Client{HTTP: httpclient.NewWithTimeout(oauthtoken.Timeout, httpclient.WithTransport(t))}
}

// Forms returns the forms of the token calls so far.
func (s *Server) Forms() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.forms...)
}

// Auths returns the Authorization headers of the calls so far.
func (s *Server) Auths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auths...)
}

// Set changes knobs under the server's lock.
func (s *Server) Set(f func(s *Server)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// Issue makes a refresh token that the server accepts, as a login would.
func (s *Server) Issue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	r := fmt.Sprintf("refresh-%d", s.n)
	s.live[r] = true
	return r
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	s.Tokens.Add(1)
	_ = r.ParseForm()
	s.mu.Lock()
	s.forms = append(s.forms, r.PostForm)
	s.auths = append(s.auths, r.Header.Get("Authorization"))
	gate := s.Gate
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail != "" {
		status := s.FailStatus
		if status == 0 {
			status = http.StatusBadRequest
		}
		if s.RetryAfter != "" {
			w.Header().Set("Retry-After", s.RetryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": s.Fail, "error_description": "description-sentinel"})
		return
	}
	refresh := ""
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		if r.PostForm.Get("code") == "" || r.PostForm.Get("code_verifier") == "" {
			s.oauthError(w, "invalid_grant")
			return
		}
		s.n++
		refresh = fmt.Sprintf("refresh-%d", s.n)
		s.live[refresh] = true
	case "refresh_token":
		old := r.PostForm.Get("refresh_token")
		if !s.live[old] {
			s.oauthError(w, "invalid_grant")
			return
		}
		if s.Rotate {
			delete(s.live, old)
			s.n++
			refresh = fmt.Sprintf("refresh-%d", s.n)
			s.live[refresh] = true
		}
	default:
		s.oauthError(w, "unsupported_grant_type")
		return
	}
	s.n++
	if s.Body != "" {
		_, _ = w.Write([]byte(s.Body))
		return
	}
	tt := s.TokenType
	if tt == "" {
		tt = "Bearer"
	}
	body := map[string]any{"access_token": fmt.Sprintf("access-%d", s.n), "token_type": tt}
	if refresh != "" {
		body["refresh_token"] = refresh
	}
	if s.ExpiresIn != 0 {
		body["expires_in"] = s.ExpiresIn
	}
	if s.Scope != "" {
		body["scope"] = s.Scope
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Server) oauthError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": "description-sentinel"})
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	s.Revokes.Add(1)
	_ = r.ParseForm()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forms = append(s.forms, r.PostForm)
	s.auths = append(s.auths, r.Header.Get("Authorization"))
	delete(s.live, r.PostForm.Get("token"))
	if s.RevokeStatus != 0 {
		w.WriteHeader(s.RevokeStatus)
	}
}

// BasicAuth is the Authorization header of basic client authentication.
func BasicAuth(id, secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(id)+":"+url.QueryEscape(secret)))
}

// HasNoSentinel reports whether text holds none of the secrets the server or
// the caller used.
func HasNoSentinel(text string, secrets ...string) bool {
	for _, s := range append(secrets, "description-sentinel") {
		if s != "" && strings.Contains(text, s) {
			return false
		}
	}
	return true
}
