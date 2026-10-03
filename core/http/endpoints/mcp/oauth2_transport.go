package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/xlog"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

const (
	// oauth2ExpirySafetyMargin is how long before expiry a token that could
	// not be refreshed stops being used. Sending a token that expires in
	// flight only turns a clear local error into a confusing 401 upstream.
	oauth2ExpirySafetyMargin = 30 * time.Second
	// oauth2RetryInterval throttles refresh attempts while the token endpoint
	// is failing, so every MCP request does not hammer the IdP.
	oauth2RetryInterval = 5 * time.Second
	// oauth2DefaultLifetime applies when the IdP omits expires_in, which
	// RFC 6749 permits. Assuming "forever" would pin a revoked token.
	oauth2DefaultLifetime     = 5 * time.Minute
	oauth2TokenRequestTimeout = 30 * time.Second
)

// newRemoteServerTransport returns the RoundTripper that authenticates
// requests to one remote MCP server. Because it sits below the http.Client,
// every MCP transport built on that client (streamable HTTP, SSE) inherits it.
func newRemoteServerTransport(server config.MCPRemoteServer, base http.RoundTripper) (http.RoundTripper, error) {
	if err := server.Validate(); err != nil {
		return nil, err
	}
	if server.OAuth2 == nil {
		return newBearerTokenRoundTripper(server.Token, base), nil
	}
	return newOAuth2ClientCredentialsRoundTripper(*server.OAuth2, base, time.Now)
}

// oauth2ClientCredentialsRoundTripper injects an access token obtained with
// the client_credentials grant. It refreshes at half the token lifetime, so a
// temporarily unavailable IdP is absorbed by the remaining half; only when the
// old token is about to expire does it fail, and then with an error rather
// than an unauthenticated request.
type oauth2ClientCredentialsRoundTripper struct {
	conf        *clientcredentials.Config
	base        http.RoundTripper
	tokenClient *http.Client
	now         func() time.Time

	mu          sync.Mutex
	token       *oauth2.Token
	expiresAt   time.Time
	refreshAt   time.Time
	nextAttempt time.Time
}

func newOAuth2ClientCredentialsRoundTripper(cfg config.MCPOAuth2Config, base http.RoundTripper, now func() time.Time) (*oauth2ClientCredentialsRoundTripper, error) {
	clientID, err := resolveCredential("client_id", cfg.ClientID, cfg.ClientIDEnv)
	if err != nil {
		return nil, err
	}
	clientSecret, err := resolveCredential("client_secret", cfg.ClientSecret, cfg.ClientSecretEnv)
	if err != nil {
		return nil, err
	}
	if base == nil {
		base = httpclient.HardenedTransport()
	}
	var params url.Values
	if len(cfg.EndpointParams) > 0 {
		params = url.Values{}
		for k, v := range cfg.EndpointParams {
			params.Set(k, v)
		}
	}
	return &oauth2ClientCredentialsRoundTripper{
		conf: &clientcredentials.Config{
			ClientID:       clientID,
			ClientSecret:   clientSecret,
			TokenURL:       cfg.TokenURL,
			Scopes:         cfg.Scopes,
			EndpointParams: params,
		},
		base:        base,
		tokenClient: httpclient.NewWithTimeout(oauth2TokenRequestTimeout, httpclient.WithTransport(base)),
		now:         now,
	}, nil
}

func resolveCredential(field, literal, envName string) (string, error) {
	if envName == "" {
		return literal, nil
	}
	// The variable name comes from the model YAML, so this is operator-chosen
	// secret indirection (like stdio "env"), not LocalAI process config.
	v, ok := os.LookupEnv(envName) //nolint:forbidigo // secret indirection named in the model YAML
	if !ok || v == "" {
		return "", fmt.Errorf("oauth2.%s_env: environment variable %q is not set", field, envName)
	}
	return v, nil
}

func (rt *oauth2ClientCredentialsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	tok, err := rt.currentToken(req.Context())
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	// RoundTrippers must not modify the caller's request.
	out := req.Clone(req.Context())
	tok.SetAuthHeader(out)
	return rt.base.RoundTrip(out)
}

func (rt *oauth2ClientCredentialsRoundTripper) currentToken(ctx context.Context) (*oauth2.Token, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	now := rt.now()
	if rt.token != nil && now.Before(rt.refreshAt) {
		return rt.token, nil
	}
	if rt.token != nil && now.Before(rt.nextAttempt) {
		if rt.stillUsable(now) {
			return rt.token, nil
		}
		return nil, fmt.Errorf("oauth2: token for %s expired and refresh is failing", rt.conf.TokenURL)
	}

	fetchCtx, cancel := context.WithTimeout(context.WithValue(ctx, oauth2.HTTPClient, rt.tokenClient), oauth2TokenRequestTimeout)
	defer cancel()
	tok, err := rt.conf.Token(fetchCtx)
	if err != nil {
		rt.nextAttempt = now.Add(oauth2RetryInterval)
		if rt.stillUsable(now) {
			xlog.Warn("MCP OAuth2 token refresh failed, keeping current token until near expiry",
				"token_url", rt.conf.TokenURL, "expires_at", rt.expiresAt, "error", err)
			return rt.token, nil
		}
		return nil, fmt.Errorf("oauth2: fetching token from %s: %w", rt.conf.TokenURL, err)
	}
	if tok.AccessToken == "" {
		return nil, errors.New("oauth2: token endpoint returned an empty access_token")
	}

	lifetime := tokenLifetime(tok)
	rt.token = tok
	rt.expiresAt = now.Add(lifetime)
	rt.refreshAt = now.Add(lifetime / 2)
	rt.nextAttempt = time.Time{}
	return tok, nil
}

func (rt *oauth2ClientCredentialsRoundTripper) stillUsable(now time.Time) bool {
	return rt.token != nil && now.Before(rt.expiresAt.Add(-oauth2ExpirySafetyMargin))
}

// tokenLifetime reads the wire value of expires_in. clientcredentials only
// exposes an absolute Expiry computed from the wall clock, which would make the
// refresh schedule depend on time.Now instead of the injected clock.
func tokenLifetime(tok *oauth2.Token) time.Duration {
	var secs float64
	switch v := tok.Extra("expires_in").(type) {
	case float64:
		secs = v
	case string:
		secs, _ = strconv.ParseFloat(v, 64)
	}
	if secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	if !tok.Expiry.IsZero() {
		if d := time.Until(tok.Expiry); d > 0 {
			return d
		}
	}
	return oauth2DefaultLifetime
}
