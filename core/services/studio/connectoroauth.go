// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"errors"
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

// GrantReason is the fixed code of why a grant needs a reconnect. The UI
// words it, and none carries text of the service.
type GrantReason string

//nolint:gosec // G101: failure reasons, not credentials
const (
	// ReasonRevoked: the service answered invalid_grant.
	ReasonRevoked GrantReason = "revoked"
	// ReasonClientRejected: the service refused the client of the add-on.
	ReasonClientRejected GrantReason = "client_rejected"
	// ReasonRefused: the service refused the refresh in another way, or gave
	// an answer the host cannot use.
	ReasonRefused GrantReason = "refused"
	// ReasonNeedsApproval: the descriptor of the add-on changed, or its
	// client was removed, so an administrator must approve it again. The
	// grant is held (connectors.GrantNeedsApproval) with its tokens.
	ReasonNeedsApproval GrantReason = "needs_approval"
	// ReasonCredentialRejected: the add-on rejected a fresh token too.
	ReasonCredentialRejected GrantReason = "credential_rejected" // #nosec G101 -- a reason label, not a credential
	// ReasonExpired: the access token ended and there is no refresh token.
	ReasonExpired GrantReason = "expired"
	// ReasonWriteFailed: the service rotated the refresh token and the host
	// could not store the new one.
	ReasonWriteFailed GrantReason = "write_failed"
	// ReasonUnreadable: the sealed tokens do not open.
	ReasonUnreadable GrantReason = "unreadable"
	// ReasonScopesMissing: a refresh granted fewer than the required scopes,
	// or a newly approved descriptor requires a scope the login lacks.
	ReasonScopesMissing GrantReason = "scopes_missing"
	// ReasonTokenTooLarge: a refresh gave an access token larger than a
	// request carries.
	ReasonTokenTooLarge GrantReason = "token_too_large"
	// ReasonEndpointChanged: the approved descriptor of the add-on sends the
	// refresh token to another token or revocation endpoint than the one the
	// login used. The token is never sent there.
	ReasonEndpointChanged GrantReason = "endpoint_changed"
)

// Errors of the OAuth credentials.
var (
	// ErrReconnectNeeded means the binding's login expired or was revoked and
	// the host cannot renew it. Nothing was sent; the person must log in
	// again. The gateway reports it as the reconnect_needed refusal.
	ErrReconnectNeeded = errors.New("studio: the connection must be made again")
	// ErrOwnerReconnectNeeded is ErrReconnectNeeded for a person who uses a
	// connection that another person shared: only the owner can log in again,
	// so the person is told that and is never offered a login. The gateway
	// reports it as the owner_reconnect_needed refusal.
	ErrOwnerReconnectNeeded = errors.New("studio: the owner of the shared connection must connect it again")
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
	ErrNeedsApproval = errors.New("studio: " + msgNeedsApproval)
	// ErrApprovalStale means the descriptor changed after the administrator
	// looked at it.
	ErrApprovalStale = errors.New("studio: " + msgApprovalStale)
)

// The texts of the errors above that the OAuth owner sends as they are.
const (
	msgNeedsApproval = "an administrator must approve this add-on first"
	msgApprovalStale = "the add-on's login changed; review it again"
)

// oauthAccess is a usable access token of a grant.
type oauthAccess struct {
	secret  string
	scopes  []string
	version int64
}

// Causes of an unusable credential that unavailable logs. They are fixed
// classes: the text of a store or sealer error is never logged.
const (
	causeStore  = "store"
	causeSealer = "sealer"
	// causeClient: the approved client could not be read (store or sealer).
	causeClient = "client"
)

// unavailable logs why the credential of b cannot be used now and returns
// ErrCredentialUnavailable, which is what the gateway is told. Without the
// line an outage of the store or the sealer would look like any slow refresh.
func (c *Connectors) unavailable(b connectors.Binding, cause string) error {
	c.log.Warn("studio: the credential of an OAuth connection is not available now", "binding", b.ID, "addon", b.AddonID, "cause", cause)
	return ErrCredentialUnavailable
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

// oauthApproval is the current descriptor of an add-on and, when an
// administrator approved it, the client to use with it.
type oauthApproval struct {
	d contract.OAuthDescriptor
	// digest is d.Digest(), computed once.
	digest string
	client connectors.OAuthClient
	cred   oauthtoken.Credentials
	// ok is true when the stored approval holds for d and its secret opens.
	ok bool
}

// approval reads the current descriptor of an add-on and its approved
// client. The error is the one of oauthDescriptor, or an outage of the store
// or the sealer; each caller maps it to its own outcome.
func (c *Connectors) approval(ctx context.Context, addonID string) (oauthApproval, error) {
	d, err := c.oauthDescriptor(ctx, addonID)
	if err != nil {
		return oauthApproval{}, err
	}
	a := oauthApproval{d: d, digest: d.Digest()}
	cl, err := c.o.Store.GetOAuthClient(ctx, addonID)
	if errors.Is(err, connectors.ErrNotFound) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	a.client = cl
	if cl.DescriptorDigest != a.digest {
		return a, nil
	}
	a.cred = oauthtoken.Credentials{ID: cl.ClientID}
	if len(cl.Secret) > 0 {
		s, err := c.sealerFor(ctx)
		if err != nil {
			return a, err
		}
		plain, err := s.Open(connectors.OAuthClientAAD(addonID), cl.Secret)
		if err != nil {
			// A secret that does not open is no approval: the administrator
			// saves it again.
			return a, nil
		}
		a.cred.Secret = string(plain)
	}
	a.ok = true
	return a, nil
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
	return g.BelongsTo(b), nil
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
// add-on holds for the descriptor of the grant. A grant whose add-on now
// declares a descriptor that no administrator approved is held
// (GrantNeedsApproval): its tokens stay but are not used. Once the new
// descriptor is approved, the grant is carried over to it (carryOver).
func (c *Connectors) readGrant(ctx context.Context, b connectors.Binding) (connectors.Grant, error) {
	g, err := c.o.Store.GetGrant(ctx, b.ID)
	if errors.Is(err, connectors.ErrNotFound) {
		return g, connectors.ErrNoCredential
	}
	if err != nil {
		return g, c.unavailable(b, causeStore)
	}
	if !g.BelongsTo(b) {
		return g, connectors.ErrNoCredential
	}
	if g.State != connectors.GrantConnected && g.State != connectors.GrantNeedsApproval {
		return g, ErrReconnectNeeded
	}
	a, err := c.approval(ctx, b.AddonID)
	if errors.Is(err, ErrOAuthUnavailable) {
		return g, ErrCredentialUnavailable
	}
	if err != nil {
		return g, c.unavailable(b, causeClient)
	}
	if !a.ok {
		return g, c.holdGrant(ctx, b, g)
	}
	if g.State != connectors.GrantConnected || g.DescriptorDigest != a.digest {
		return c.carryOver(ctx, b, g, a.d, a.digest)
	}
	return g, nil
}

// holdGrant marks a grant needs_approval and keeps its secrets: an upgrade
// of the add-on must not log every person out before an administrator had
// the chance to approve the new descriptor. It returns ErrReconnectNeeded,
// since the grant cannot be used either way.
func (c *Connectors) holdGrant(ctx context.Context, b connectors.Binding, g connectors.Grant) error {
	if g.State == connectors.GrantNeedsApproval {
		return ErrReconnectNeeded
	}
	next := g
	next.State, next.Reason, next.Updated = connectors.GrantNeedsApproval, string(ReasonNeedsApproval), c.o.Clock().UTC()
	_, err := c.o.Store.UpdateGrant(ctx, next, g.Version)
	c.log.Info("studio: an OAuth connection waits for an approval of its add-on", "binding", b.ID, "addon", b.AddonID, "stored", err == nil)
	return ErrReconnectNeeded
}

// carryOver moves a grant to the descriptor d, which an administrator
// approved. The refresh token was issued for the endpoints of the grant's
// descriptor, so the grant is carried over only when d sends it to the same
// token and revocation endpoints; otherwise it ends (ReasonEndpointChanged)
// and the person logs in again. A grant without a scope that d requires ends
// too (ReasonScopesMissing), and the scopes that d no longer names are
// dropped. digest is d.Digest().
func (c *Connectors) carryOver(ctx context.Context, b connectors.Binding, g connectors.Grant, d contract.OAuthDescriptor, digest string) (connectors.Grant, error) {
	if g.TokenURL != d.TokenURL || g.RevokeURL != d.RevokeURL {
		return g, c.endGrant(ctx, b, g, ReasonEndpointChanged)
	}
	for _, req := range d.Scopes.Required {
		if !slices.Contains(g.Scopes, req) {
			return g, c.endGrant(ctx, b, g, ReasonScopesMissing)
		}
	}
	next := g
	next.DescriptorDigest, next.State, next.Reason = digest, connectors.GrantConnected, ""
	next.Scopes = inDescriptor(d, g.Scopes)
	version, err := c.o.Store.UpdateGrant(ctx, next, g.Version)
	if err != nil {
		return g, c.unavailable(b, causeStore)
	}
	if version == 0 {
		// Another call wrote the grant first; the next call reads it.
		return g, ErrCredentialUnavailable
	}
	c.log.Info("studio: an OAuth connection moved to the approved login of its add-on", "binding", b.ID, "addon", b.AddonID)
	next.Version = version
	return next, nil
}

// inDescriptor keeps the scopes that the descriptor d names, required or
// optional. A scope it does not name is not passed to the add-on.
func inDescriptor(d contract.OAuthDescriptor, scopes []string) []string {
	return slices.DeleteFunc(slices.Clone(scopes), func(s string) bool {
		return !slices.Contains(d.Scopes.Required, s) && !slices.Contains(d.Scopes.Optional, s)
	})
}

// approveGrants carries the held grants of an add-on over to its approved
// descriptor d, or ends them, right after an administrator approved it, so
// the Connectors page shows the result without waiting for a call.
func (c *Connectors) approveGrants(ctx context.Context, addonID string, d contract.OAuthDescriptor) {
	all, err := c.o.Store.AllBindings(ctx)
	if err != nil {
		return
	}
	digest := d.Digest()
	for _, b := range all {
		if b.AddonID != addonID || !b.UsesOAuth() {
			continue
		}
		g, err := c.o.Store.GetGrant(ctx, b.ID)
		if err != nil || !g.BelongsTo(b) {
			continue
		}
		if g.State == connectors.GrantNeedsApproval || (g.State == connectors.GrantConnected && g.DescriptorDigest != digest) {
			_, _ = c.carryOver(ctx, b, g, d, digest)
		}
	}
	c.Notify()
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
		return connectors.GrantSecrets{}, c.unavailable(b, causeSealer)
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
func (c *Connectors) endGrant(ctx context.Context, b connectors.Binding, g connectors.Grant, reason GrantReason) error {
	next := g
	next.Sealed, next.State, next.Reason, next.Updated = nil, connectors.GrantReconnectNeeded, string(reason), c.o.Clock().UTC()
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
	// The wait is counted in polls, not read from a clock: the lease uses
	// c.o.Clock, and a test clock that does not move must not make a caller
	// wait forever.
	var waited time.Duration
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
			return oauthAccess{}, c.unavailable(b, causeStore)
		}
		if won {
			return c.refreshHeld(ctx, b, g)
		}
		if waited >= oauthLeaseWait {
			return oauthAccess{}, ErrCredentialUnavailable
		}
		seen = g.Version
		if err := c.o.LeaseWait(ctx, oauthLeasePoll); err != nil {
			return oauthAccess{}, ErrCredentialUnavailable
		}
		waited += oauthLeasePoll
	}
}

// refreshHeld refreshes the grant g, whose lease this process holds. The new
// token is written back before it is returned.
//
// The lease is freed on every way out, failed writes of the store or the
// sealer included, so the other callers do not wait for its end and then
// answer unavailable. It is kept in one case only: the service rotated the
// refresh token and the new one was not written back. The stored token may
// be dead then, and no frontend may send it again before the lease ends.
func (c *Connectors) refreshHeld(ctx context.Context, b connectors.Binding, g connectors.Grant) (oauthAccess, error) {
	keepLease := false
	defer func() {
		if !keepLease {
			// After a write of the grant this does nothing: the write moved
			// the grant to a new version, which has no lease.
			_ = c.o.Store.ReleaseGrant(ctx, b.ID, g.Version, c.instance)
		}
	}()
	sec, err := c.openGrant(ctx, b, g)
	if err != nil {
		return oauthAccess{}, err
	}
	if sec.Refresh == "" {
		return oauthAccess{}, c.endGrant(ctx, b, g, ReasonExpired)
	}
	a, err := c.approval(ctx, b.AddonID)
	if err != nil {
		if errors.Is(err, ErrOAuthUnavailable) {
			return oauthAccess{}, ErrCredentialUnavailable
		}
		return oauthAccess{}, c.unavailable(b, causeClient)
	}
	if !a.ok {
		return oauthAccess{}, c.holdGrant(ctx, b, g)
	}
	if g.DescriptorDigest != a.digest || g.TokenURL != a.d.TokenURL {
		// The approval changed since the grant was read; the next call
		// carries it over. The refresh token goes only to the endpoint of
		// the grant.
		return oauthAccess{}, ErrCredentialUnavailable
	}
	tok, err := c.callRefresh(ctx, b, g, a, sec.Refresh)
	if err != nil {
		return oauthAccess{}, err
	}
	rotated := tok.Refresh != "" && tok.Refresh != sec.Refresh
	// From here a failure loses the rotated refresh token.
	keepLease = rotated
	next := g
	next.Updated = c.o.Clock().UTC()
	next.ExpiresAt = time.Time{}
	if tok.ExpiresIn > 0 {
		next.ExpiresAt = next.Updated.Add(tok.ExpiresIn)
	}
	scopes, ok := narrowScopes(a.d, g.Scopes, tok)
	if !ok {
		return oauthAccess{}, c.endGrant(ctx, b, g, ReasonScopesMissing)
	}
	next.Scopes = scopes
	keep := connectors.GrantSecrets{Access: tok.Access, Refresh: sec.Refresh, TokenType: tok.TokenType}
	if rotated {
		keep.Refresh = tok.Refresh
	}
	return c.writeBack(ctx, b, g, next, keep, rotated)
}

// callRefresh trades the refresh token for a new access token and maps what
// the token endpoint answers to the way the grant ends. The error it returns
// is the one for the caller.
func (c *Connectors) callRefresh(ctx context.Context, b connectors.Binding, g connectors.Grant, a oauthApproval, refresh string) (oauthtoken.Tokens, error) {
	tok, err := c.tokenClient().Refresh(ctx, a.d, a.cred, refresh)
	var limited *contract.ErrRateLimited
	switch {
	case err == nil:
		return tok, nil
	case errors.As(err, &limited):
		return tok, limited
	case errors.Is(err, oauthtoken.ErrInvalidGrant):
		return tok, c.endGrant(ctx, b, g, ReasonRevoked)
	case errors.Is(err, oauthtoken.ErrInvalidClient):
		return tok, c.endGrant(ctx, b, g, ReasonClientRejected)
	case errors.Is(err, oauthtoken.ErrTokenTooLarge):
		return tok, c.endGrant(ctx, b, g, ReasonTokenTooLarge)
	case errors.Is(err, oauthtoken.ErrRefused), errors.Is(err, oauthtoken.ErrBadResponse):
		return tok, c.endGrant(ctx, b, g, ReasonRefused)
	}
	return tok, ErrCredentialUnavailable
}

// narrowScopes returns the scopes a login keeps after a refresh. A refresh
// may narrow the login, never widen it: a scope the person did not grant at
// the login is not passed to the add-on. It reports false when a required
// scope is gone, because the add-on cannot work then and the person logs in
// again.
func narrowScopes(d contract.OAuthDescriptor, held []string, tok oauthtoken.Tokens) ([]string, bool) {
	if !tok.ScopeSet {
		return held, true
	}
	for _, req := range d.Scopes.Required {
		if !slices.Contains(tok.Scopes, req) {
			return nil, false
		}
	}
	return slices.DeleteFunc(slices.Clone(held), func(s string) bool { return !slices.Contains(tok.Scopes, s) }), true
}

// writeBack seals the new secrets into next and stores it at the version of
// g, trying again when the store fails. When the service rotated the refresh
// token and nothing could be written, the grant ends: the old token may be
// dead and the new one is lost.
func (c *Connectors) writeBack(ctx context.Context, b connectors.Binding, g, next connectors.Grant, keep connectors.GrantSecrets, rotated bool) (oauthAccess, error) {
	cause := causeSealer
	s, err := c.sealerFor(ctx)
	if err == nil {
		next.Sealed, err = s.SealGrant(connectors.GrantAAD(b.ID, b.Owner, b.AddonID), keep)
	}
	if err == nil {
		cause = causeStore
		for range oauthWriteTries {
			var version int64
			version, err = c.o.Store.UpdateGrant(ctx, next, g.Version)
			if err == nil && version != 0 {
				return oauthAccess{secret: keep.Access, scopes: next.Scopes, version: version}, nil
			}
			if err == nil {
				// The grant moved under the lease: retrying cannot win.
				break
			}
		}
	}
	if rotated {
		return oauthAccess{}, c.endGrant(ctx, b, g, ReasonWriteFailed)
	}
	// err is nil when the grant moved under the lease; only an outage is
	// logged.
	if err != nil {
		return oauthAccess{}, c.unavailable(b, cause)
	}
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
	// The renewal is the host's work on the login of the owner, not an act of
	// the person: it is open to a person the binding is shared with, and to
	// nobody else. The tokens stay on the host.
	b, err := c.bindingFor(ctx, user, bindingID)
	if err != nil || b.AddonID != addonID || !b.UsesOAuth() {
		return CredentialGrant{}, connectors.ErrNoCredential
	}
	entry, err := connectors.EntryUsable(b, true, key, c.wildcardsOf(ctx, b))
	if err != nil {
		return CredentialGrant{}, err
	}
	if final {
		g, err := c.readGrant(ctx, b)
		if err != nil {
			return CredentialGrant{}, c.forGrantee(user, b, err)
		}
		if g.Version != version {
			return CredentialGrant{}, ErrCredentialUnavailable
		}
		return CredentialGrant{}, c.forGrantee(user, b, c.endGrant(ctx, b, g, ReasonCredentialRejected))
	}
	acc, err := c.refresh(ctx, b, version, true)
	if err != nil {
		return CredentialGrant{}, c.forGrantee(user, b, err)
	}
	c.log.Info("studio: connector credential audit", "binding", b.ID, "kind", b.Kind, "addon", b.AddonID, "resource", entry.Key, "renewed", true)
	return CredentialGrant{Secret: acc.secret, Scopes: acc.scopes, Version: acc.version, Resource: entry, WriteMode: b.Writes(), BindingID: b.ID, BindingName: b.Name, Hold: c.holdFor(user, b)}, nil
}
