// SPDX-License-Identifier: MIT
package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

// Auth kinds of a binding: how its credential is obtained.
const (
	// AuthToken is a token the person gave the host. It is sealed in
	// Binding.Token. The empty kind means the same, so a binding stored
	// before the kind existed is a token binding.
	AuthToken = "token"
	// AuthOAuth2 is an OAuth 2.0 login the host ran. The tokens are in the
	// grant of the binding (see Grant) and Binding.Token stays empty.
	AuthOAuth2 = "oauth2"
)

// UsesOAuth reports whether the credential of the binding is an OAuth grant.
func (b Binding) UsesOAuth() bool { return b.AuthKind == AuthOAuth2 }

// HasTokenCredential reports whether a token binding holds its token. An OAuth
// binding never does: its credential is the grant.
func (b Binding) HasTokenCredential() bool { return !b.UsesOAuth() && len(b.Token) > 0 }

func validAuthKind(b Binding) error {
	switch b.AuthKind {
	case "", AuthToken:
		return nil
	case AuthOAuth2:
		if len(b.Token) > 0 {
			return errors.New("an OAuth binding holds no token of its own")
		}
		return nil
	}
	return errors.New("the auth kind must be token or oauth2")
}

// States of a grant.
const (
	// GrantConnected is a grant whose tokens the host can use or refresh.
	GrantConnected = "connected"
	// GrantReconnectNeeded is a grant that the service refused, or whose
	// add-on now sends its refresh token to another endpoint. Its secrets are
	// wiped; Reason says why. The person must log in again.
	GrantReconnectNeeded = "reconnect_needed"
	// GrantNeedsApproval is a grant whose add-on declares a descriptor that no
	// administrator approved yet, for example after an upgrade. Its secrets
	// stay, but the host neither uses nor refreshes them until an approval
	// carries the grant over (see TokenURL).
	GrantNeedsApproval = "needs_approval"
)

// LoginStateTTL is how long a login may take, from start to callback.
const LoginStateTTL = 10 * time.Minute

// Bounds of the logins that wait for their callback. A person needs one or
// two at a time; the bounds keep a loop of starts from filling the store.
const (
	MaxLoginsPerBinding = 5
	MaxLoginsPerOwner   = 20
)

// ErrTooManyLogins means a binding or its owner already has the most logins
// that may wait for their callback. One ends when its callback comes or
// after LoginStateTTL.
var ErrTooManyLogins = errors.New("connectors: too many logins are waiting for their callback")

// Grant is the OAuth token set of one binding. The secrets are sealed in
// Sealed (see GrantSecrets and GrantAAD); the other fields are not secret.
//
// Version changes on every write. A refresher reads a version, leases the
// grant at that version (LeaseGrant) and writes back at it (UpdateGrant), so
// two refreshers on any two frontends never use one refresh token twice.
type Grant struct {
	Binding, Owner, AddonID string
	// Sealed is the sealed GrantSecrets. A store returns what it was given.
	// It is empty when the state is GrantReconnectNeeded.
	Sealed []byte
	// ExpiresAt is when the access token ends. The zero value means the
	// service did not say: the host refreshes only after a rejection.
	ExpiresAt time.Time
	// Scopes are the scopes the service granted.
	Scopes []string
	// AccountLabel is who the person is at the service, for people.
	AccountLabel string
	State        string
	// Reason is a fixed code that says why the state is GrantReconnectNeeded.
	Reason string
	// DescriptorDigest is the digest of the OAuth descriptor the grant was
	// last approved for: the one the login used, or a later one an
	// administrator approved.
	DescriptorDigest string
	// TokenURL and RevokeURL are the endpoints of that descriptor, the only
	// ones the refresh token goes to. A newer descriptor carries the grant
	// over only when they did not change.
	TokenURL, RevokeURL string
	Version             int64
	// LeaseOwner holds the refresh of the grant until LeaseUntil.
	LeaseOwner string
	LeaseUntil time.Time
	Updated    time.Time
}

// BelongsTo reports whether g is the grant of b: a grant row of another owner
// or add-on under the same binding ID is not used.
func (g Grant) BelongsTo(b Binding) bool { return g.Owner == b.Owner && g.AddonID == b.AddonID }

func (g Grant) clone() Grant {
	g.Sealed = slices.Clone(g.Sealed)
	g.Scopes = slices.Clone(g.Scopes)
	return g
}

// GrantSecrets is what Grant.Sealed holds.
type GrantSecrets struct {
	Access    string `json:"access"`
	Refresh   string `json:"refresh,omitempty"`
	TokenType string `json:"tokenType,omitempty"`
}

// OAuthClient is the client an administrator registered with the service of
// an add-on, and the approval of the descriptor it was registered for.
type OAuthClient struct {
	AddonID string
	// DescriptorDigest is the digest of the descriptor the administrator
	// approved. The host uses the client only while the add-on declares a
	// descriptor with this digest.
	DescriptorDigest string
	ClientID         string
	// Secret is the sealed client secret (see OAuthClientAAD), or empty for a
	// public client.
	Secret     []byte
	ApprovedBy string
	ApprovedAt time.Time
}

func (c OAuthClient) clone() OAuthClient {
	c.Secret = slices.Clone(c.Secret)
	return c
}

// LoginState is a login that was started and waits for its callback. The
// store keeps the hash of the state parameter, never the state itself.
type LoginState struct {
	Hash                    string
	Binding, Owner, AddonID string
	DescriptorDigest        string
	// Verifier is the sealed PKCE verifier (see LoginStateAAD).
	Verifier    []byte
	RedirectURI string
	Scopes      []string
	ReturnTo    string
	// NonceHash is the hash of the nonce cookie of the browser that started
	// the login.
	NonceHash string
	Expires   time.Time
}

func (s LoginState) clone() LoginState {
	s.Verifier = slices.Clone(s.Verifier)
	s.Scopes = slices.Clone(s.Scopes)
	return s
}

// OAuthStore keeps the OAuth records. It is part of Store.
type OAuthStore interface {
	// GetGrant returns the grant of a binding, or ErrNotFound.
	GetGrant(ctx context.Context, binding string) (Grant, error)
	// PutGrant stores the grant of a new login and replaces any earlier one.
	// The version becomes one more than the one it replaced (1 for a new
	// grant) and the lease ends. It returns the new version. It refuses with
	// ErrNotFound, and stores nothing, when the binding does not exist: the
	// check and the write are one step, so a login that finishes while its
	// binding is deleted leaves no grant behind.
	PutGrant(ctx context.Context, g Grant) (int64, error)
	// UpdateGrant writes g only while the stored grant has the given version,
	// as one step. The version becomes version+1 and the lease ends. It
	// returns the new version, or 0 when it wrote nothing because the stored
	// grant has another version or none. The binding, owner and add-on never
	// change.
	UpdateGrant(ctx context.Context, g Grant, version int64) (int64, error)
	// LeaseGrant gives the refresh of a grant to owner until now+ttl, as one
	// step, only while the grant has the given version and no live lease. It
	// reports whether owner holds the lease.
	LeaseGrant(ctx context.Context, binding string, version int64, owner string, now time.Time, ttl time.Duration) (bool, error)
	// ReleaseGrant ends the lease that owner holds on the grant at version,
	// so another refresher need not wait for it to run out.
	ReleaseGrant(ctx context.Context, binding string, version int64, owner string) error
	// DeleteGrant removes the grant of a binding. A missing one is no error.
	DeleteGrant(ctx context.Context, binding string) error

	GetOAuthClient(ctx context.Context, addonID string) (OAuthClient, error)
	// PutOAuthClient stores the client of an add-on and replaces an earlier
	// one.
	PutOAuthClient(ctx context.Context, c OAuthClient) error
	DeleteOAuthClient(ctx context.Context, addonID string) error

	// PutLoginState drops the logins that ended before now and stores a
	// started login. It refuses with ErrTooManyLogins, and stores nothing,
	// when the binding already has MaxLoginsPerBinding live logins or the
	// owner MaxLoginsPerOwner.
	PutLoginState(ctx context.Context, s LoginState, now time.Time) error
	// ConsumeLoginState removes the login with this hash and returns it when
	// it had not expired at now. The removal and the read are one step, so a
	// state works once on any frontend, even when the exchange that follows
	// fails. An expired state is removed and not returned.
	ConsumeLoginState(ctx context.Context, hash string, now time.Time) (LoginState, bool, error)
}

// GrantAAD is the additional data of the secrets of a grant.
func GrantAAD(binding, owner, addonID string) string {
	return "grant|" + binding + "|" + owner + "|" + addonID
}

// OAuthClientAAD is the additional data of the client secret of an add-on.
func OAuthClientAAD(addonID string) string { return "oauth-client|" + addonID }

// LoginStateAAD is the additional data of the PKCE verifier of a login.
func LoginStateAAD(hash, binding, owner string) string {
	return "oauth-state|" + hash + "|" + binding + "|" + owner
}

// SealGrant seals the secrets of a grant.
func (s *Sealer) SealGrant(aad string, g GrantSecrets) ([]byte, error) {
	raw, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	return s.Seal(aad, raw)
}

// OpenGrant reverses SealGrant.
func (s *Sealer) OpenGrant(aad string, sealed []byte) (GrantSecrets, error) {
	raw, err := s.Open(aad, sealed)
	if err != nil {
		return GrantSecrets{}, err
	}
	var g GrantSecrets
	if err := json.Unmarshal(raw, &g); err != nil {
		return GrantSecrets{}, errors.New("connectors: the sealed grant is not readable")
	}
	return g, nil
}
