// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/oauthtoken"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/randtoken"
)

// Outcomes of a login callback. They are a closed list: the callback puts one
// in the URL it redirects to, and nothing the service said is copied there.
const (
	LoginOK = "ok"
	// LoginDenied: the person declined at the service. The binding stays as
	// it was.
	LoginDenied = "denied"
	// LoginInvalidState: the state is unknown, used, expired, or belongs to
	// another person or browser.
	LoginInvalidState = "invalid_state"
	// LoginProviderError: the service sent an error other than a denial, or
	// no code.
	LoginProviderError = "provider_error"
	// LoginExchangeFailed: the code could not be traded for a usable token,
	// the approval of the add-on is gone, or the service refused the new
	// token when the host asked who the person is.
	LoginExchangeFailed = "exchange_failed"
	// LoginScopesMissing: the service granted fewer than the required scopes.
	LoginScopesMissing = "scopes_missing"
	// LoginUnavailable: the service or the add-on did not answer. The person
	// may try again.
	LoginUnavailable = "unavailable"
	// LoginTokenTooLarge: the service issued an access token larger than a
	// request carries (contract.MaxCredentialBytes).
	LoginTokenTooLarge = "token_too_large"
)

// MaxLoginStartsPerMinute bounds the login starts of one person. The store
// bounds the logins that wait for their callback too
// (connectors.MaxLoginsPerBinding, connectors.MaxLoginsPerOwner).
const MaxLoginStartsPerMinute = 10

// ErrTooManyLogins means the person started too many logins in the last
// minute, or the binding or the person has too many logins that wait for
// their callback. The person finishes one or waits.
var ErrTooManyLogins = connectors.ErrTooManyLogins

// loginWindow counts the starts of one person in the minute that began at
// start.
type loginWindow struct {
	start time.Time
	n     int
}

// allowLoginStart counts a start of the person and reports whether it is
// within MaxLoginStartsPerMinute. The count lives in this process: the starts
// are served by the add-on owner, which is one process.
func (c *Connectors) allowLoginStart(owner string) bool {
	now := c.o.Clock()
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	w := c.logins[owner]
	if now.Sub(w.start) >= time.Minute || now.Before(w.start) {
		w = loginWindow{start: now}
	}
	if w.n >= MaxLoginStartsPerMinute {
		return false
	}
	w.n++
	c.logins[owner] = w
	if len(c.logins) > 1024 {
		for k, old := range c.logins {
			if now.Sub(old.start) >= time.Minute {
				delete(c.logins, k)
			}
		}
	}
	return true
}

const (
	// MaxReturnToBytes bounds the path a login returns to.
	MaxReturnToBytes = 2048
	maxClientIDBytes = 512
	maxSecretBytes   = 4096
)

// LoginStart is what the caller of StartLogin needs to send the person to the
// service. The state is in AuthorizeURL only; the host keeps its hash.
type LoginStart struct {
	AuthorizeURL string
	// CookieSuffix ends the name of the nonce cookie of this login
	// (LoginCookieSuffix of its state), so the caller need not read the state
	// out of AuthorizeURL.
	CookieSuffix string
	// Nonce goes in an HttpOnly cookie of the browser that started the login;
	// the callback must bring it back.
	Nonce   string
	Expires time.Time
}

// LoginResult is the outcome of a callback.
type LoginResult struct {
	// Outcome is one of the Login* constants.
	Outcome string
	// ReturnTo is the app path the login was started from, or "" when the
	// state was not usable.
	ReturnTo  string
	BindingID string
}

func randomToken() (string, error) { return randtoken.Base64URL(32) }

func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// LoginCookieSuffix names the nonce cookie of the login with this state, so
// each login in a browser has its own cookie and the callback finds it by the
// state in its query. It is the first eight hex digits of the hash of the
// state, which is no secret: the state itself is in the callback address.
func LoginCookieSuffix(state string) string { return hashHex(state)[:8] }

// ValidReturnTo is an absolute path on this host: it starts with one slash and
// names no other host. The HTTP layer adds the rules of the web app to it.
func ValidReturnTo(p string) bool {
	return p != "" && len(p) <= MaxReturnToBytes && strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") &&
		!strings.ContainsAny(p, "\\\r\n\t ") && utf8.ValidString(p)
}

func validRedirectURI(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && u.Fragment == "" && len(s) <= MaxReturnToBytes
}

// oauthBinding returns an OAuth binding of the user.
func (c *Connectors) oauthBinding(ctx context.Context, user, id string) (connectors.Binding, error) {
	b, err := c.owned(ctx, user, id)
	if err != nil {
		return b, err
	}
	if !b.UsesOAuth() {
		return b, connectorBad("this connection does not use a login")
	}
	return b, nil
}

// StartLogin begins the OAuth login of a binding of the user. optional are the
// optional scopes the person ticked; the required ones are always asked for.
// redirectURI is the callback address of this host, which the caller builds
// and which is used again for the exchange; returnTo is the app path the
// person comes back to. The add-on must be approved for its current
// descriptor (ErrNeedsApproval). The state is stored by its hash for
// connectors.LoginStateTTL, in the shared store, so any frontend can serve
// the callback. Too many starts, or too many logins that wait, are refused
// with ErrTooManyLogins.
func (c *Connectors) StartLogin(ctx context.Context, user, bindingID string, optional []string, redirectURI, returnTo string) (LoginStart, error) {
	b, err := c.oauthBinding(ctx, user, bindingID)
	if err != nil {
		return LoginStart{}, err
	}
	if !c.allowLoginStart(b.Owner) {
		return LoginStart{}, ErrTooManyLogins
	}
	if !validRedirectURI(redirectURI) {
		return LoginStart{}, connectorBad("the redirect address is not valid")
	}
	if !ValidReturnTo(returnTo) {
		return LoginStart{}, connectorBad("the return path must be a path on this host")
	}
	a, err := c.approval(ctx, b.AddonID)
	if err != nil {
		return LoginStart{}, err
	}
	if !a.ok {
		return LoginStart{}, ErrNeedsApproval
	}
	d := a.d
	scopes := slices.Clone(d.Scopes.Required)
	for _, s := range optional {
		if !slices.Contains(d.Scopes.Optional, s) {
			return LoginStart{}, connectorBad("the scope %q is not an optional scope of this add-on", s)
		}
		if !slices.Contains(scopes, s) {
			scopes = append(scopes, s)
		}
	}
	state, err := randomToken()
	if err != nil {
		return LoginStart{}, err
	}
	verifier, err := randomToken()
	if err != nil {
		return LoginStart{}, err
	}
	nonce, err := randomToken()
	if err != nil {
		return LoginStart{}, err
	}
	sealer, err := c.sealerFor(ctx)
	if err != nil {
		return LoginStart{}, err
	}
	hash := hashHex(state)
	sealed, err := sealer.Seal(connectors.LoginStateAAD(hash, b.ID, b.Owner), []byte(verifier))
	if err != nil {
		return LoginStart{}, err
	}
	now := c.o.Clock().UTC()
	expires := now.Add(connectors.LoginStateTTL)
	st := connectors.LoginState{Hash: hash, Binding: b.ID, Owner: b.Owner, AddonID: b.AddonID, DescriptorDigest: a.digest, Verifier: sealed,
		RedirectURI: redirectURI, Scopes: scopes, ReturnTo: returnTo, NonceHash: hashHex(nonce), Expires: expires}
	if err := c.o.Store.PutLoginState(ctx, st, now); err != nil {
		return LoginStart{}, err
	}
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{}
	for _, p := range d.AuthorizeParams {
		q.Set(p.Name, p.Value)
	}
	q.Set("response_type", "code")
	q.Set("client_id", a.client.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", strings.Join(scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	q.Set("code_challenge_method", contract.OAuthPKCES256)
	return LoginStart{AuthorizeURL: d.AuthorizeURL + "?" + q.Encode(), CookieSuffix: LoginCookieSuffix(state), Nonce: nonce, Expires: expires}, nil
}

// CompleteLogin finishes a login at its callback. user is the person of the
// session (empty with auth off), nonce the value of the cookie StartLogin
// asked for, and state, code and providerError the query of the callback.
// The state is used up first, so it works once even when the rest fails.
// The outcome is a fixed class; an error is returned only when the store
// failed, and then the outcome is LoginUnavailable.
func (c *Connectors) CompleteLogin(ctx context.Context, user, nonce, state, code, providerError string) (LoginResult, error) {
	if state == "" {
		return LoginResult{Outcome: LoginInvalidState}, nil
	}
	hash := hashHex(state)
	st, ok, err := c.o.Store.ConsumeLoginState(ctx, hash, c.o.Clock())
	if err != nil {
		return LoginResult{Outcome: LoginUnavailable}, err
	}
	if !ok || st.Owner != principal(user) || subtle.ConstantTimeCompare([]byte(st.NonceHash), []byte(hashHex(nonce))) != 1 {
		return LoginResult{Outcome: LoginInvalidState}, nil
	}
	res := LoginResult{ReturnTo: st.ReturnTo, BindingID: st.Binding}
	fail := func(outcome string) (LoginResult, error) {
		res.Outcome = outcome
		c.log.Info("studio: an OAuth login did not finish", "binding", st.Binding, "addon", st.AddonID, "outcome", outcome)
		return res, nil
	}
	b, err := c.o.Store.GetBinding(ctx, st.Binding)
	if err != nil || b.Owner != st.Owner || b.AddonID != st.AddonID || !b.UsesOAuth() {
		return fail(LoginInvalidState)
	}
	if providerError != "" {
		if providerError == "access_denied" {
			res.Outcome = LoginDenied
			return res, nil
		}
		return fail(LoginProviderError)
	}
	if code == "" {
		return fail(LoginProviderError)
	}
	a, err := c.approval(ctx, b.AddonID)
	if err != nil {
		return fail(LoginUnavailable)
	}
	if !a.ok || a.digest != st.DescriptorDigest {
		return fail(LoginExchangeFailed)
	}
	sealer, err := c.sealerFor(ctx)
	if err != nil {
		return fail(LoginUnavailable)
	}
	verifier, err := sealer.Open(connectors.LoginStateAAD(hash, st.Binding, st.Owner), st.Verifier)
	if err != nil {
		return fail(LoginInvalidState)
	}
	tok, outcome := c.exchange(ctx, a, st, string(verifier), code)
	if outcome != "" {
		return fail(outcome)
	}
	scopes, ok := checkScopes(a.d, st, tok)
	if !ok {
		return fail(LoginScopesMissing)
	}
	label, outcome := c.connectedAs(ctx, b, tok.Access)
	if outcome != "" {
		return fail(outcome)
	}
	switch err := c.storeGrant(ctx, b, st, a.d, sealer, tok, scopes, label); {
	case err == nil:
	case errors.Is(err, connectors.ErrNotFound):
		// The binding was deleted while the code was traded. The tokens are
		// dropped; nothing can reach or revoke them here.
		return fail(LoginInvalidState)
	case errors.Is(err, errSealGrant):
		return fail(LoginUnavailable)
	default:
		res.Outcome = LoginUnavailable
		return res, err
	}
	c.Notify()
	res.Outcome = LoginOK
	return res, nil
}

// exchange trades the code for tokens. It returns the outcome of a failed
// trade, or "" for a trade that worked.
func (c *Connectors) exchange(ctx context.Context, a oauthApproval, st connectors.LoginState, verifier, code string) (oauthtoken.Tokens, string) {
	tok, err := c.tokenClient().Exchange(ctx, a.d, a.cred, code, verifier, st.RedirectURI)
	var limited *contract.ErrRateLimited
	switch {
	case err == nil:
		return tok, ""
	case errors.Is(err, oauthtoken.ErrUnavailable), errors.As(err, &limited):
		return tok, LoginUnavailable
	case errors.Is(err, oauthtoken.ErrTokenTooLarge):
		return tok, LoginTokenTooLarge
	}
	return tok, LoginExchangeFailed
}

// checkScopes returns the scopes the login keeps. A service that lists the
// granted scopes decides, and one that does not leaves the requested ones. A
// provider may grant more than was asked (incremental authorization), and
// only the scopes the descriptor names are kept, as when a grant is carried
// over to a new descriptor. It reports false when a required scope is
// missing.
func checkScopes(d contract.OAuthDescriptor, st connectors.LoginState, tok oauthtoken.Tokens) ([]string, bool) {
	scopes := st.Scopes
	if tok.ScopeSet {
		scopes = tok.Scopes
	}
	scopes = inDescriptor(d, scopes)
	for _, r := range d.Scopes.Required {
		if !slices.Contains(scopes, r) {
			return nil, false
		}
	}
	return scopes, true
}

// errSealGrant marks a grant whose tokens could not be sealed.
var errSealGrant = errors.New("studio: the tokens of the login could not be sealed")

// storeGrant seals the tokens and stores the grant of the login.
func (c *Connectors) storeGrant(ctx context.Context, b connectors.Binding, st connectors.LoginState, d contract.OAuthDescriptor, sealer *connectors.Sealer, tok oauthtoken.Tokens, scopes []string, label string) error {
	now := c.o.Clock().UTC()
	g := connectors.Grant{Binding: b.ID, Owner: b.Owner, AddonID: b.AddonID, Scopes: scopes, AccountLabel: label,
		State: connectors.GrantConnected, DescriptorDigest: st.DescriptorDigest, TokenURL: d.TokenURL, RevokeURL: d.RevokeURL, Updated: now}
	if tok.ExpiresIn > 0 {
		g.ExpiresAt = now.Add(tok.ExpiresIn)
	}
	sealed, err := sealer.SealGrant(connectors.GrantAAD(b.ID, b.Owner, b.AddonID), connectors.GrantSecrets{Access: tok.Access, Refresh: tok.Refresh, TokenType: tok.TokenType})
	if err != nil {
		return errSealGrant
	}
	g.Sealed = sealed
	_, err = c.o.Store.PutGrant(ctx, g)
	return err
}

// connectedAs asks the add-on, with the new token, who the person is at the
// service (the connector op "me"). An unauthorized answer fails the login;
// an add-on that does not answer leaves the label empty, as a token check
// does.
func (c *Connectors) connectedAs(ctx context.Context, b connectors.Binding, access string) (label, outcome string) {
	if c.o.Transport == nil {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(ctx, connectedAsTimeout)
	defer cancel()
	scope := BindingConnectorScope(b)
	scope.Purpose = ConnectorPurposeVerifyToken
	resp, err := c.o.Transport.Call(ctx, scope, contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: b.Kind, Token: access})
	switch {
	case errors.Is(err, connectors.ErrForbidden):
		return "", LoginExchangeFailed
	case err != nil:
		return "", ""
	case resp.Error != nil && resp.Error.Code == contract.ConnectorUnauthorized:
		return "", LoginExchangeFailed
	case resp.Error != nil:
		return "", ""
	}
	label = resp.BotUsername
	if label == "" {
		label = resp.BotID
	}
	return cleanAccountLabel(label), ""
}

// connectedAsTimeout bounds the question to the add-on who the person is.
const connectedAsTimeout = 20 * time.Second

// MaxAccountLabelRunes bounds the account name of a login.
const MaxAccountLabelRunes = 128

// cleanAccountLabel makes the account name the service gave safe to show as
// "Connected as ...": control and format characters (bidirectional overrides
// and isolates, zero-width characters) are removed, so the text cannot show
// another account than the one linked, runs of white space become one space,
// and the name is bounded.
func cleanAccountLabel(s string) string {
	var b strings.Builder
	space := false
	n := 0
	for _, r := range s {
		if n == MaxAccountLabelRunes {
			break
		}
		switch {
		case r == utf8.RuneError, unicode.Is(unicode.Cf, r):
			continue
		case unicode.IsSpace(r):
			space = b.Len() > 0
			continue
		case unicode.IsControl(r):
			continue
		}
		if space {
			b.WriteByte(' ')
			n++
			space = false
			if n == MaxAccountLabelRunes {
				break
			}
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// Disconnect ends the login of a binding of the user. When the descriptor has
// a revocation endpoint and its approval holds, the host revokes the refresh
// token (or the access token when there is none) first. The grant is deleted
// in any case. told reports whether the service confirmed the revocation.
func (c *Connectors) Disconnect(ctx context.Context, user, bindingID string) (told bool, err error) {
	b, err := c.oauthBinding(ctx, user, bindingID)
	if err != nil {
		return false, err
	}
	g, err := c.o.Store.GetGrant(ctx, b.ID)
	if errors.Is(err, connectors.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	told = c.revoke(ctx, b, g)
	if err := c.o.Store.DeleteGrant(ctx, b.ID); err != nil {
		return told, err
	}
	c.Notify()
	return told, nil
}

func (c *Connectors) revoke(ctx context.Context, b connectors.Binding, g connectors.Grant) bool {
	if len(g.Sealed) == 0 || !g.BelongsTo(b) {
		return false
	}
	a, err := c.approval(ctx, b.AddonID)
	if err != nil || !a.ok || a.d.RevokeURL == "" || a.digest != g.DescriptorDigest {
		return false
	}
	d := a.d
	s, err := c.sealerFor(ctx)
	if err != nil {
		return false
	}
	sec, err := s.OpenGrant(connectors.GrantAAD(b.ID, b.Owner, b.AddonID), g.Sealed)
	if err != nil {
		return false
	}
	token, hint := sec.Refresh, "refresh_token"
	if token == "" {
		token, hint = sec.Access, "access_token"
	}
	return c.tokenClient().Revoke(ctx, d, a.cred, token, hint) == nil
}

// OAuthClientInput is what an administrator saves for an add-on. Saving it is
// the approval of the descriptor whose digest the administrator saw.
type OAuthClientInput struct {
	// Digest is the descriptor digest the form showed.
	Digest   string
	ClientID string
	// Secret is the client secret. Nil keeps the stored one; a public client
	// (clientAuth none) has none.
	Secret *string
	// ApprovedBy names the administrator.
	ApprovedBy string
}

// OAuthClientStatus is the client of an add-on without its secret.
type OAuthClientStatus struct {
	// Configured is true when a client is stored.
	Configured bool
	ClientID   string
	HasSecret  bool
	// Approved is true when the stored approval is for the current
	// descriptor.
	Approved                      bool
	ApprovedDigest, CurrentDigest string
	ApprovedBy                    string
	ApprovedAt                    time.Time
	// Descriptor is the current descriptor, for the form to show.
	Descriptor contract.OAuthDescriptor
}

// SetClient stores the OAuth client of an add-on and approves its current
// descriptor. The caller must have checked that the actor is an
// administrator. It refuses with ErrApprovalStale when the descriptor changed
// since the administrator saw in.Digest.
func (c *Connectors) SetClient(ctx context.Context, addonID string, in OAuthClientInput) (OAuthClientStatus, error) {
	d, err := c.oauthDescriptor(ctx, addonID)
	if err != nil {
		return OAuthClientStatus{}, err
	}
	digest := d.Digest()
	if in.Digest != digest {
		return OAuthClientStatus{}, ErrApprovalStale
	}
	id := strings.TrimSpace(in.ClientID)
	if id == "" || len(id) > maxClientIDBytes || !visibleASCII(id) {
		return OAuthClientStatus{}, connectorBad("the client ID must have 1 to %d visible ASCII characters or spaces", maxClientIDBytes)
	}
	in.ApprovedBy = strings.TrimSpace(in.ApprovedBy)
	if in.ApprovedBy == "" || len(in.ApprovedBy) > 255 {
		return OAuthClientStatus{}, connectorBad("the approval must name the administrator")
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	old, err := c.o.Store.GetOAuthClient(ctx, addonID)
	if err != nil && !errors.Is(err, connectors.ErrNotFound) {
		return OAuthClientStatus{}, err
	}
	next := connectors.OAuthClient{AddonID: addonID, DescriptorDigest: digest, ClientID: id, ApprovedBy: in.ApprovedBy, ApprovedAt: c.o.Clock().UTC()}
	switch {
	case d.ClientAuth == contract.OAuthClientAuthNone:
		if in.Secret != nil && *in.Secret != "" {
			return OAuthClientStatus{}, connectorBad("a public client has no secret")
		}
	case in.Secret == nil:
		next.Secret = old.Secret
	default:
		secret := *in.Secret
		if secret == "" || len(secret) > maxSecretBytes || !visibleASCII(secret) {
			return OAuthClientStatus{}, connectorBad("the client secret must have 1 to %d visible ASCII characters or spaces", maxSecretBytes)
		}
		s, err := c.sealerFor(ctx)
		if err != nil {
			return OAuthClientStatus{}, err
		}
		if next.Secret, err = s.Seal(connectors.OAuthClientAAD(addonID), []byte(secret)); err != nil {
			return OAuthClientStatus{}, err
		}
	}
	if d.ClientAuth != contract.OAuthClientAuthNone && len(next.Secret) == 0 {
		return OAuthClientStatus{}, connectorBad("this add-on needs a client secret")
	}
	if err := c.o.Store.PutOAuthClient(ctx, next); err != nil {
		return OAuthClientStatus{}, err
	}
	c.approveGrants(ctx, addonID, d)
	return clientStatus(next, d), nil
}

// GetClientStatus reports the client of an add-on, never its secret.
func (c *Connectors) GetClientStatus(ctx context.Context, addonID string) (OAuthClientStatus, error) {
	d, err := c.oauthDescriptor(ctx, addonID)
	if err != nil {
		return OAuthClientStatus{}, err
	}
	cl, err := c.o.Store.GetOAuthClient(ctx, addonID)
	if errors.Is(err, connectors.ErrNotFound) {
		return OAuthClientStatus{CurrentDigest: d.Digest(), Descriptor: d}, nil
	}
	if err != nil {
		return OAuthClientStatus{}, err
	}
	return clientStatus(cl, d), nil
}

// clientStatus is the status of a stored client cl for the current
// descriptor d.
func clientStatus(cl connectors.OAuthClient, d contract.OAuthDescriptor) OAuthClientStatus {
	digest := d.Digest()
	return OAuthClientStatus{Configured: true, ClientID: cl.ClientID, HasSecret: len(cl.Secret) > 0, Approved: cl.DescriptorDigest == digest,
		ApprovedDigest: cl.DescriptorDigest, CurrentDigest: digest, ApprovedBy: cl.ApprovedBy, ApprovedAt: cl.ApprovedAt, Descriptor: d}
}

// visibleASCII is the VSCHAR of RFC 6749 (appendix A): visible ASCII and the
// space. A client ID and a client secret are made of it, and a service may
// issue a secret with spaces.
func visibleASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
