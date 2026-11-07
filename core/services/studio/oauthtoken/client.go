// SPDX-License-Identifier: MIT

// Package oauthtoken is the host's OAuth 2.0 token client: it exchanges an
// authorization code, refreshes an access token and revokes a token, at the
// endpoints of an add-on's OAuth descriptor.
//
// It is small on purpose. Its errors are fixed classes and never carry the
// response body, a token, the code, the verifier or the client secret, so a
// caller may log them. It accepts bearer tokens only.
package oauthtoken

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// Bounds of the client.
const (
	// Timeout bounds a code exchange or a refresh.
	Timeout = 30 * time.Second
	// revokeTimeout bounds a revocation.
	revokeTimeout = 10 * time.Second
	// maxBody is the largest answer read.
	maxBody = 64 << 10
	// minExpiry and maxExpiry bound the lifetime the service gives a token.
	minExpiry = time.Minute
	maxExpiry = 24 * time.Hour
	// maxRefreshBytes bounds a refresh token.
	maxRefreshBytes = 8192
	// maxScopes bounds the granted scopes kept from an answer: as many as a
	// request may carry.
	maxScopes = contract.MaxGrantedScopes
	// defaultRetryAfter is the wait of a 429 that says none.
	defaultRetryAfter = 30
)

// Failure classes. A *contract.ErrRateLimited is the fifth one.
var (
	// ErrUnavailable is a network error, a timeout or a 5xx answer. Nothing
	// changed at the service; a later call may work.
	ErrUnavailable = errors.New("oauth: the token endpoint is unavailable")
	// ErrInvalidGrant is invalid_grant: the code or the refresh token is
	// expired, used or revoked.
	ErrInvalidGrant = errors.New("oauth: the service refused the grant")
	// ErrInvalidClient is invalid_client or unauthorized_client.
	ErrInvalidClient = errors.New("oauth: the service refused the client")
	// ErrRefused is any other refusal of the service.
	ErrRefused = errors.New("oauth: the service refused the request")
	// ErrBadResponse is an answer the host cannot use: not JSON, too large,
	// no access token, a token that is not a bearer token or does not fit a
	// request.
	ErrBadResponse = errors.New("oauth: the token response is not usable")
	// ErrTokenTooLarge is a bad response whose access token does not fit a
	// request (contract.MaxCredentialBytes). errors.Is reports it as
	// ErrBadResponse too.
	ErrTokenTooLarge = fmt.Errorf("%w: the access token is larger than %d bytes", ErrBadResponse, contract.MaxCredentialBytes)
	// ErrDescriptor is a descriptor that is not valid. Nothing was sent.
	ErrDescriptor = errors.New("oauth: the descriptor is not valid")
	// ErrNoRevocation is a descriptor without a revocation endpoint.
	ErrNoRevocation = errors.New("oauth: the service has no revocation endpoint")
)

// Credentials are the client the administrator registered. Secret is empty for
// a public client.
type Credentials struct{ ID, Secret string }

// Tokens is a usable token answer.
type Tokens struct {
	Access, Refresh, TokenType string
	// ExpiresIn is the lifetime of the access token, clamped to minExpiry
	// through maxExpiry. Zero means the service did not say.
	ExpiresIn time.Duration
	// Scopes are the granted scopes when ScopeSet; otherwise the service did
	// not list them and the requested ones apply.
	Scopes   []string
	ScopeSet bool
}

// Client calls the token endpoints. The zero value is not usable: use New.
type Client struct {
	// HTTP must be a client of pkg/httpclient. New gives one that reaches
	// public addresses only and follows no redirect.
	HTTP *http.Client
}

// New returns a client that reaches public addresses only, follows no
// redirect and gives up after Timeout.
func New() *Client {
	return &Client{HTTP: httpclient.NewWithTimeout(Timeout, httpclient.WithExternalOnly())}
}

// Exchange trades an authorization code for tokens.
func (c *Client) Exchange(ctx context.Context, d contract.OAuthDescriptor, cred Credentials, code, verifier, redirectURI string) (Tokens, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirectURI}}
	return c.token(ctx, d, cred, form)
}

// Refresh trades a refresh token for a new access token. The answer may carry
// a new refresh token, which then replaces the old one.
func (c *Client) Refresh(ctx context.Context, d contract.OAuthDescriptor, cred Credentials, refresh string) (Tokens, error) {
	return c.token(ctx, d, cred, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
}

// Revoke asks the service to revoke a token (RFC 7009). hint is
// "refresh_token", "access_token" or empty.
func (c *Client) Revoke(ctx context.Context, d contract.OAuthDescriptor, cred Credentials, token, hint string) error {
	if d.Validate() != nil {
		return ErrDescriptor
	}
	if d.RevokeURL == "" {
		return ErrNoRevocation
	}
	ctx, cancel := context.WithTimeout(ctx, revokeTimeout)
	defer cancel()
	form := url.Values{"token": {token}}
	if hint != "" {
		form.Set("token_type_hint", hint)
	}
	resp, err := c.post(ctx, d.RevokeURL, d.ClientAuth, cred, form)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests:
		return contract.RateLimitedAfter(contract.ParseRetryAfter(resp.Header.Get("Retry-After"), defaultRetryAfter))
	case resp.StatusCode >= 500:
		return ErrUnavailable
	}
	return ErrRefused
}

func (c *Client) post(ctx context.Context, endpoint, clientAuth string, cred Credentials, form url.Values) (*http.Response, error) {
	switch clientAuth {
	case contract.OAuthClientAuthPost:
		form.Set("client_id", cred.ID)
		form.Set("client_secret", cred.Secret)
	case contract.OAuthClientAuthNone:
		form.Set("client_id", cred.ID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, ErrDescriptor
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if clientAuth == contract.OAuthClientAuthBasic {
		// RFC 6749 section 2.3.1: both parts are form-encoded first.
		raw := url.QueryEscape(cred.ID) + ":" + url.QueryEscape(cred.Secret)
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(raw)))
	}
	client := c.HTTP
	if client == nil {
		client = New().HTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		// A redirect is the service's answer, not an outage; it is not
		// followed, because the request carries the client's secret.
		if errors.Is(err, httpclient.ErrRedirectBlocked) {
			return nil, ErrRefused
		}
		return nil, ErrUnavailable
	}
	return resp, nil
}

type tokenAnswer struct {
	AccessToken  string          `json:"access_token"`
	TokenType    string          `json:"token_type"`
	RefreshToken string          `json:"refresh_token"`
	ExpiresIn    json.RawMessage `json:"expires_in"`
	Scope        *string         `json:"scope"`
	Error        string          `json:"error"`
}

func (c *Client) token(ctx context.Context, d contract.OAuthDescriptor, cred Credentials, form url.Values) (Tokens, error) {
	if d.Validate() != nil {
		return Tokens{}, ErrDescriptor
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	resp, err := c.post(ctx, d.TokenURL, d.ClientAuth, cred, form)
	if err != nil {
		return Tokens{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return Tokens{}, ErrUnavailable
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return Tokens{}, contract.RateLimitedAfter(contract.ParseRetryAfter(resp.Header.Get("Retry-After"), defaultRetryAfter))
	case resp.StatusCode >= 500:
		return Tokens{}, ErrUnavailable
	case resp.StatusCode != http.StatusOK:
		var a tokenAnswer
		_ = json.Unmarshal(body, &a)
		switch a.Error {
		case "invalid_grant":
			return Tokens{}, ErrInvalidGrant
		case "invalid_client", "unauthorized_client":
			return Tokens{}, ErrInvalidClient
		}
		return Tokens{}, ErrRefused
	}
	if len(body) > maxBody {
		return Tokens{}, ErrBadResponse
	}
	var a tokenAnswer
	if json.Unmarshal(bytes.TrimSpace(body), &a) != nil {
		return Tokens{}, ErrBadResponse
	}
	if len(a.AccessToken) > contract.MaxCredentialBytes {
		return Tokens{}, ErrTokenTooLarge
	}
	if a.AccessToken == "" || !contract.GraphicASCII(a.AccessToken) ||
		!strings.EqualFold(a.TokenType, "bearer") || len(a.RefreshToken) > maxRefreshBytes {
		return Tokens{}, ErrBadResponse
	}
	out := Tokens{Access: a.AccessToken, Refresh: a.RefreshToken, TokenType: "Bearer"}
	if len(a.ExpiresIn) > 0 && string(a.ExpiresIn) != "null" {
		secs, err := strconv.ParseInt(strings.Trim(string(a.ExpiresIn), `"`), 10, 64)
		if err != nil {
			return Tokens{}, ErrBadResponse
		}
		out.ExpiresIn = clampExpiry(secs)
	}
	if a.Scope != nil {
		out.ScopeSet = true
		seen := map[string]bool{}
		for _, s := range strings.Fields(*a.Scope) {
			// A scope that Request.Validate refuses would fail every call of
			// the grant as an outage; refusing the answer here makes it a
			// failed login or refresh instead.
			if !contract.ValidScopeToken(s) {
				return Tokens{}, ErrBadResponse
			}
			if seen[s] {
				continue
			}
			seen[s] = true
			out.Scopes = append(out.Scopes, s)
		}
		if len(out.Scopes) > maxScopes {
			return Tokens{}, ErrBadResponse
		}
	}
	return out, nil
}

func clampExpiry(secs int64) time.Duration {
	switch {
	case secs < int64(minExpiry/time.Second):
		return minExpiry
	case secs > int64(maxExpiry/time.Second):
		return maxExpiry
	}
	return time.Duration(secs) * time.Second
}
