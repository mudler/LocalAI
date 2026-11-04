// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/oauthtoken"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Timing of the refresh of an OAuth grant.
const (
	// oauthRefreshMargin is how long before the end of an access token the
	// host refreshes it, so a call never starts with a token that ends
	// during it.
	oauthRefreshMargin = 120 * time.Second
	// oauthLeaseTTL is how long a refresher holds the grant. A refresher that
	// dies frees it after this. It must be well over the whole refresh: the
	// reads and the unsealing before the token request, the request itself
	// (oauthtoken.Timeout) and the write-back. Otherwise a slow service can
	// still be answering the first refresher when a second one takes the
	// lease and sends the same refresh token, and a rotating service then
	// revokes the whole token family.
	//
	// Each process compares the lease with its own clock, so the hosts of a
	// cluster must keep their clocks in sync (NTP); a skew shortens the lease
	// on the host whose clock is ahead by as much.
	oauthLeaseTTL = 3 * oauthtoken.Timeout
	// oauthLeasePoll and oauthLeaseWait are how often, and how long, a caller
	// that lost the lease looks for the token the winner writes.
	oauthLeasePoll = 200 * time.Millisecond
	oauthLeaseWait = 10 * time.Second
	// oauthWriteTries is how often the write-back of a refreshed token is
	// tried before the grant is given up.
	oauthWriteTries = 3
)

// Reasons of a grant that needs a reconnect. They are fixed codes: the UI
// words them, and none carries text of the service.
const (
	// ReasonRevoked: the service answered invalid_grant.
	ReasonRevoked = "revoked"
	// ReasonClientRejected: the service refused the client of the add-on.
	ReasonClientRejected = "client_rejected"
	// ReasonRefused: the service refused the refresh in another way, or gave
	// an answer the host cannot use.
	ReasonRefused = "refused"
	// ReasonNeedsApproval: the descriptor of the add-on changed, or its
	// client was removed, so an administrator must approve it again.
	ReasonNeedsApproval = "needs_approval"
	// ReasonCredentialRejected: the add-on rejected a fresh token too.
	ReasonCredentialRejected = "credential_rejected"
	// ReasonExpired: the access token ended and there is no refresh token.
	ReasonExpired = "expired"
	// ReasonWriteFailed: the service rotated the refresh token and the host
	// could not store the new one.
	ReasonWriteFailed = "write_failed"
	// ReasonUnreadable: the sealed tokens do not open.
	ReasonUnreadable = "unreadable"
	// ReasonScopesMissing: a refresh granted fewer than the required scopes.
	ReasonScopesMissing = "scopes_missing"
)

// Errors of the OAuth credentials.
var (
	// ErrReconnectNeeded means the binding's login expired or was revoked and
	// the host cannot renew it. Nothing was sent; the person must log in
	// again. The gateway reports it as the reconnect_needed refusal.
	ErrReconnectNeeded = errors.New("studio: the connection must be made again")
	// ErrCredentialUnavailable means the token could not be renewed now (the
	// service or the store did not answer, or another refresher holds the
	// grant too long). The state of the grant did not change; a later call may
	// work.
	ErrCredentialUnavailable = errors.New("studio: the credential cannot be renewed now")
	// ErrOAuthUnavailable means this process cannot read the OAuth descriptor
	// of the add-on: the add-on does not run here, or the process does not
	// run add-ons.
	ErrOAuthUnavailable = errors.New("studio: the OAuth login of this add-on is not available here")
	// ErrNeedsApproval means no administrator approved the current OAuth
	// descriptor of the add-on.
	ErrNeedsApproval = errors.New("studio: an administrator must approve this add-on first")
	// ErrApprovalStale means the descriptor changed after the administrator
	// looked at it.
	ErrApprovalStale = errors.New("studio: the add-on's login changed; review it again")
)

// oauthAccess is a usable access token of a grant.
type oauthAccess struct {
	secret  string
	scopes  []string
	version int64
}

// oauthDescriptor returns the current descriptor of an add-on.
func (c *Connectors) oauthDescriptor(ctx context.Context, addonID string) (contract.OAuthDescriptor, error) {
	if c.o.OAuthDescriptor == nil {
		return contract.OAuthDescriptor{}, ErrOAuthUnavailable
	}
	d, err := c.o.OAuthDescriptor(ctx, addonID)
	if err != nil {
		return contract.OAuthDescriptor{}, ErrOAuthUnavailable
	}
	return d, nil
}

func (c *Connectors) tokenClient() *oauthtoken.Client {
	if c.o.TokenClient != nil {
		return c.o.TokenClient
	}
	return c.defaultToken
}

// approvedClient returns the client of the add-on when its approval holds for
// the current descriptor d. ok is false when there is none or it is for
// another digest; err is an outage of the store.
func (c *Connectors) approvedClient(ctx context.Context, addonID string, d contract.OAuthDescriptor) (connectors.OAuthClient, oauthtoken.Credentials, bool, error) {
	cl, err := c.o.Store.GetOAuthClient(ctx, addonID)
	if errors.Is(err, connectors.ErrNotFound) {
		return cl, oauthtoken.Credentials{}, false, nil
	}
	if err != nil {
		return cl, oauthtoken.Credentials{}, false, err
	}
	if cl.DescriptorDigest != d.Digest() {
		return cl, oauthtoken.Credentials{}, false, nil
	}
	cred := oauthtoken.Credentials{ID: cl.ClientID}
	if len(cl.Secret) > 0 {
		s, err := c.sealerFor(ctx)
		if err != nil {
			return cl, cred, false, err
		}
		plain, err := s.Open(connectors.OAuthClientAAD(addonID), cl.Secret)
		if err != nil {
			// A secret that does not open is no approval: the administrator
			// saves it again.
			return cl, cred, false, nil
		}
		cred.Secret = string(plain)
	}
	return cl, cred, true, nil
}

// hasGrant reports whether an OAuth binding has a grant in any state: a
// grant that needs a reconnect is still the person's connection, so a call
// gets the reconnect_needed refusal and not no_credential.
func (c *Connectors) hasGrant(ctx context.Context, b connectors.Binding) (bool, error) {
	g, err := c.o.Store.GetGrant(ctx, b.ID)
	if errors.Is(err, connectors.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return g.Owner == b.Owner && g.AddonID == b.AddonID, nil
}

// accessTokenFor returns the secret a call of the binding's add-on carries.
// A token binding gets its token, as always. An OAuth binding gets the access
// token of its grant, refreshed first when it ends within oauthRefreshMargin.
func (c *Connectors) accessTokenFor(ctx context.Context, b connectors.Binding) (oauthAccess, error) {
	if !b.UsesOAuth() {
		s, err := c.openToken(ctx, b)
		return oauthAccess{secret: s}, err
	}
	g, err := c.readGrant(ctx, b)
	if err != nil {
		return oauthAccess{}, err
	}
	if c.grantFresh(g) {
		return c.openAccess(ctx, b, g)
	}
	return c.refresh(ctx, b, g.Version, false)
}

// grantFresh reports whether the access token of g may be used now. A token
// whose end the service did not give is used until the add-on rejects it.
func (c *Connectors) grantFresh(g connectors.Grant) bool {
	return g.ExpiresAt.IsZero() || c.o.Clock().Before(g.ExpiresAt.Add(-refreshMargin(g)))
}

// refreshMargin is how long before its end the token of g is renewed:
// oauthRefreshMargin, or half the lifetime of a shorter token. A fixed margin
// longer than the lifetime would renew the token on every call, and with a
// rotating service rotate the refresh token each time. The token was issued
// when the grant was last written.
func refreshMargin(g connectors.Grant) time.Duration {
	life := g.ExpiresAt.Sub(g.Updated)
	if g.Updated.IsZero() || life <= 0 {
		return oauthRefreshMargin
	}
	return min(oauthRefreshMargin, life/2)
}

// readGrant reads the grant of an OAuth binding and checks that it may be
// used: it is the binding's, it is connected, and the approval of the
// add-on still holds for the descriptor the login used. A broken approval
// ends the grant, so its refresh token never goes to a changed endpoint.
func (c *Connectors) readGrant(ctx context.Context, b connectors.Binding) (connectors.Grant, error) {
	g, err := c.o.Store.GetGrant(ctx, b.ID)
	if errors.Is(err, connectors.ErrNotFound) {
		return g, connectors.ErrNoCredential
	}
	if err != nil {
		return g, ErrCredentialUnavailable
	}
	if g.Owner != b.Owner || g.AddonID != b.AddonID {
		return g, connectors.ErrNoCredential
	}
	if g.State != connectors.GrantConnected {
		return g, ErrReconnectNeeded
	}
	d, err := c.oauthDescriptor(ctx, b.AddonID)
	if err != nil {
		return g, ErrCredentialUnavailable
	}
	_, _, ok, err := c.approvedClient(ctx, b.AddonID, d)
	if err != nil {
		return g, ErrCredentialUnavailable
	}
	if !ok || g.DescriptorDigest != d.Digest() {
		return g, c.endGrant(ctx, b, g, ReasonNeedsApproval)
	}
	return g, nil
}

func (c *Connectors) openAccess(ctx context.Context, b connectors.Binding, g connectors.Grant) (oauthAccess, error) {
	sec, err := c.openGrant(ctx, b, g)
	if err != nil {
		return oauthAccess{}, err
	}
	return oauthAccess{secret: sec.Access, scopes: g.Scopes, version: g.Version}, nil
}

func (c *Connectors) openGrant(ctx context.Context, b connectors.Binding, g connectors.Grant) (connectors.GrantSecrets, error) {
	s, err := c.sealerFor(ctx)
	if err != nil {
		return connectors.GrantSecrets{}, ErrCredentialUnavailable
	}
	sec, err := s.OpenGrant(connectors.GrantAAD(b.ID, b.Owner, b.AddonID), g.Sealed)
	if err != nil || sec.Access == "" {
		return connectors.GrantSecrets{}, c.endGrant(ctx, b, g, ReasonUnreadable)
	}
	return sec, nil
}

// endGrant wipes the secrets of the grant and marks it reconnect_needed with
// the reason. It returns ErrReconnectNeeded, which is the answer whether or
// not the write went through: the grant cannot be used either way.
func (c *Connectors) endGrant(ctx context.Context, b connectors.Binding, g connectors.Grant, reason string) error {
	next := g
	next.Sealed, next.State, next.Reason, next.Updated = nil, connectors.GrantReconnectNeeded, reason, c.o.Clock().UTC()
	// A write that loses the compare-and-set leaves the newer grant alone:
	// it is a refresh or a new login, and the next call reads it.
	_, err := c.o.Store.UpdateGrant(ctx, next, g.Version)
	// The line names the binding, the add-on and the fixed reason, never a
	// token.
	c.log.Warn("studio: an OAuth connection needs a reconnect", "binding", b.ID, "addon", b.AddonID, "reason", reason, "stored", err == nil)
	return ErrReconnectNeeded
}

// refresh renews the access token of the grant that the caller read at
// version seen. In one process one refresh per binding and version runs at a
// time; across processes the lease in the grant row decides, and a caller
// that loses it waits for the token the winner writes. With force the token
// is renewed even before its end, because the add-on rejected it.
func (c *Connectors) refresh(ctx context.Context, b connectors.Binding, seen int64, force bool) (oauthAccess, error) {
	key := b.ID + "\x00" + strconv.FormatInt(seen, 10)
	if force {
		key += "\x00force"
	}
	ch := c.refreshes.DoChan(key, func() (any, error) {
		// The refresh outlives a caller that gives up: the write-back of a
		// rotated token must not be cut off half way.
		return c.refreshShared(context.WithoutCancel(ctx), b, seen, force)
	})
	select {
	case r := <-ch:
		if r.Err != nil {
			return oauthAccess{}, r.Err
		}
		return r.Val.(oauthAccess), nil
	case <-ctx.Done():
		return oauthAccess{}, ErrCredentialUnavailable
	}
}

func (c *Connectors) refreshShared(ctx context.Context, b connectors.Binding, seen int64, force bool) (oauthAccess, error) {
	deadline := time.Now().Add(oauthLeaseWait)
	for {
		g, err := c.readGrant(ctx, b)
		if err != nil {
			return oauthAccess{}, err
		}
		if g.Version != seen && (force || c.grantFresh(g)) {
			// Someone renewed the grant since the caller read it.
			return c.openAccess(ctx, b, g)
		}
		won, err := c.o.Store.LeaseGrant(ctx, b.ID, g.Version, c.instance, c.o.Clock(), oauthLeaseTTL)
		if err != nil {
			return oauthAccess{}, ErrCredentialUnavailable
		}
		if won {
			return c.refreshHeld(ctx, b, g)
		}
		if time.Now().After(deadline) {
			return oauthAccess{}, ErrCredentialUnavailable
		}
		seen = g.Version
		select {
		case <-time.After(oauthLeasePoll):
		case <-ctx.Done():
			return oauthAccess{}, ErrCredentialUnavailable
		}
	}
}

// refreshHeld refreshes the grant g, whose lease this process holds. The new
// token is written back before it is returned.
func (c *Connectors) refreshHeld(ctx context.Context, b connectors.Binding, g connectors.Grant) (oauthAccess, error) {
	release := func() { _ = c.o.Store.ReleaseGrant(ctx, b.ID, g.Version, c.instance) }
	sec, err := c.openGrant(ctx, b, g)
	if err != nil {
		return oauthAccess{}, err
	}
	if sec.Refresh == "" {
		return oauthAccess{}, c.endGrant(ctx, b, g, ReasonExpired)
	}
	d, err := c.oauthDescriptor(ctx, b.AddonID)
	if err != nil {
		release()
		return oauthAccess{}, ErrCredentialUnavailable
	}
	_, cred, ok, err := c.approvedClient(ctx, b.AddonID, d)
	if err != nil {
		release()
		return oauthAccess{}, ErrCredentialUnavailable
	}
	if !ok || g.DescriptorDigest != d.Digest() {
		return oauthAccess{}, c.endGrant(ctx, b, g, ReasonNeedsApproval)
	}
	tok, err := c.tokenClient().Refresh(ctx, d, cred, sec.Refresh)
	var limited *contract.ErrRateLimited
	switch {
	case err == nil:
	case errors.As(err, &limited):
		release()
		return oauthAccess{}, limited
	case errors.Is(err, oauthtoken.ErrInvalidGrant):
		return oauthAccess{}, c.endGrant(ctx, b, g, ReasonRevoked)
	case errors.Is(err, oauthtoken.ErrInvalidClient):
		return oauthAccess{}, c.endGrant(ctx, b, g, ReasonClientRejected)
	case errors.Is(err, oauthtoken.ErrRefused), errors.Is(err, oauthtoken.ErrBadResponse):
		return oauthAccess{}, c.endGrant(ctx, b, g, ReasonRefused)
	default:
		release()
		return oauthAccess{}, ErrCredentialUnavailable
	}
	rotated := tok.Refresh != "" && tok.Refresh != sec.Refresh
	next := g
	next.Updated = c.o.Clock().UTC()
	next.ExpiresAt = time.Time{}
	if tok.ExpiresIn > 0 {
		next.ExpiresAt = next.Updated.Add(tok.ExpiresIn)
	}
	if tok.ScopeSet {
		// A refresh may narrow the login, never widen it: a scope the person
		// did not grant at the login is not passed to the add-on. Without a
		// required scope the add-on cannot work, so the person logs in again.
		for _, req := range d.Scopes.Required {
			if !slices.Contains(tok.Scopes, req) {
				return oauthAccess{}, c.endGrant(ctx, b, g, ReasonScopesMissing)
			}
		}
		next.Scopes = slices.DeleteFunc(slices.Clone(g.Scopes), func(s string) bool { return !slices.Contains(tok.Scopes, s) })
	}
	keep := connectors.GrantSecrets{Access: tok.Access, Refresh: sec.Refresh, TokenType: tok.TokenType}
	if rotated {
		keep.Refresh = tok.Refresh
	}
	s, err := c.sealerFor(ctx)
	if err == nil {
		next.Sealed, err = s.SealGrant(connectors.GrantAAD(b.ID, b.Owner, b.AddonID), keep)
	}
	if err == nil {
		for range oauthWriteTries {
			var wrote bool
			wrote, err = c.o.Store.UpdateGrant(ctx, next, g.Version)
			if err == nil && wrote {
				return oauthAccess{secret: tok.Access, scopes: next.Scopes, version: g.Version + 1}, nil
			}
			if err == nil {
				// The grant moved under the lease: retrying cannot win.
				break
			}
		}
	}
	if rotated {
		// The old refresh token may be dead now and the new one is lost.
		return oauthAccess{}, c.endGrant(ctx, b, g, ReasonWriteFailed)
	}
	release()
	return oauthAccess{}, ErrCredentialUnavailable
}

// RenewCredential is the host's answer to an add-on that rejected the
// credential of a call (contract.ErrCredentialRejected). version is the
// version of the grant the call carried (CredentialGrant.Version).
//
// The first time, the grant is refreshed once, unless it changed since the
// call began; then its current token is used. The caller retries the call
// once with the grant it gets. When the add-on rejects that one too, the
// caller calls again with final set, which ends the grant with
// ReasonCredentialRejected and returns ErrReconnectNeeded, unless the grant
// changed meanwhile (ErrCredentialUnavailable then). A token binding cannot
// be renewed: ErrNoCredential.
func (c *Connectors) RenewCredential(ctx context.Context, user, addonID, bindingID, key string, version int64, final bool) (CredentialGrant, error) {
	b, err := c.o.Store.GetBinding(ctx, bindingID)
	if err != nil || b.Owner != principal(user) || b.AddonID != addonID || !b.UsesOAuth() {
		return CredentialGrant{}, connectors.ErrNoCredential
	}
	entry, err := connectors.EntryWith(b, true, key)
	if err != nil {
		return CredentialGrant{}, err
	}
	if final {
		g, err := c.readGrant(ctx, b)
		if err != nil {
			return CredentialGrant{}, err
		}
		if g.Version != version {
			return CredentialGrant{}, ErrCredentialUnavailable
		}
		return CredentialGrant{}, c.endGrant(ctx, b, g, ReasonCredentialRejected)
	}
	acc, err := c.refresh(ctx, b, version, true)
	if err != nil {
		return CredentialGrant{}, err
	}
	c.log.Info("studio: connector credential audit", "binding", b.ID, "kind", b.Kind, "addon", b.AddonID, "resource", entry.Key, "renewed", true)
	return CredentialGrant{Secret: acc.secret, Scopes: acc.scopes, Version: acc.version, Resource: entry, WriteMode: b.Writes(), BindingID: b.ID, BindingName: b.Name}, nil
}

// OAuthConnection is the state of the login of an OAuth binding, without any
// secret.
type OAuthConnection struct {
	// State is "" when the binding has no login, else connectors.GrantConnected
	// or connectors.GrantReconnectNeeded.
	State        string
	Reason       string
	AccountLabel string
	Scopes       []string
	ExpiresAt    time.Time
}

// ConnectionState reports the login of a binding of the user.
func (c *Connectors) ConnectionState(ctx context.Context, user, bindingID string) (OAuthConnection, error) {
	b, err := c.owned(ctx, user, bindingID)
	if err != nil {
		return OAuthConnection{}, err
	}
	if !b.UsesOAuth() {
		return OAuthConnection{}, connectorBad("this connection does not use a login")
	}
	g, err := c.o.Store.GetGrant(ctx, b.ID)
	if errors.Is(err, connectors.ErrNotFound) {
		return OAuthConnection{}, nil
	}
	if err != nil {
		return OAuthConnection{}, err
	}
	return OAuthConnection{State: g.State, Reason: g.Reason, AccountLabel: g.AccountLabel, Scopes: g.Scopes, ExpiresAt: g.ExpiresAt}, nil
}

func oauthBad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", connectors.ErrInvalid, fmt.Sprintf(format, a...))
}
