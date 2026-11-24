package studio

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/xslices"
)

// The OAuth routes of the connections. Only the process that runs the
// add-ons reads an add-on's OAuth descriptor, so every call goes to the
// add-on gateway: the owner serves it, here in a combined process, or over
// the carrier from a frontend. A frontend never answers from its own
// service. The provider sends the browser back to any frontend; the
// callback forwards the query and the nonce cookie as fields of the call.

// OAuthDispatcher sends one OAuth call to the add-on owner. The add-on
// gateway implements it.
type OAuthDispatcher interface {
	OAuth(ctx context.Context, a contract.Actor, call gateway.OAuthCall) (gateway.OAuthReply, error)
}

// OAuthConfig wires the OAuth routes.
type OAuthConfig struct {
	// Dispatcher returns the add-on gateway, or nil when add-ons do not run
	// here. It is read at call time.
	Dispatcher func() OAuthDispatcher
	// Mode is the ownership mode of the add-ons (auth.AddonModeNamedUser or
	// auth.AddonModeLocalOwner). It is deployment configuration.
	Mode string
}

// HandlerOption changes the handlers.
type HandlerOption func(*Handlers)

// WithOAuth wires the OAuth routes. Without it they answer 503.
func WithOAuth(cfg OAuthConfig) HandlerOption { return func(h *Handlers) { h.oauth = cfg } }

// Apply applies options to the handlers.
func (h *Handlers) Apply(opts ...HandlerOption) *Handlers {
	for _, o := range opts {
		o(h)
	}
	return h
}

const (
	// OAuthCallbackPath is the one callback route of every add-on, under the
	// base URL of the server. An administrator registers it with the service.
	OAuthCallbackPath = "/api/chats/connectors/oauth/callback"
	// oauthNonceCookie names the cookie that binds the callback to the
	// browser that started the login, followed by the suffix of that login
	// (studiosvc.LoginCookieSuffix), so two logins in one browser do not
	// overwrite each other. It is HttpOnly, SameSite=Lax and lives as long as
	// the login.
	oauthNonceCookie = "localai_oauth_nonce_"
	// defaultOAuthReturn is the page the callback goes back to when the login
	// named none it can use.
	defaultOAuthReturn = "/app/chats/connectors"
	maxAppQuery        = 1024
)

// The classes the callback puts in oauth=error&class=<c>. It is a closed
// list; nothing the service said is copied.
var oauthErrorClasses = map[string]bool{
	studiosvc.LoginInvalidState:   true,
	studiosvc.LoginProviderError:  true,
	studiosvc.LoginExchangeFailed: true,
	studiosvc.LoginScopesMissing:  true,
	studiosvc.LoginUnavailable:    true,
	studiosvc.LoginTokenTooLarge:  true,
}

// appPath and appQuery are the shape of an address of the web app that the
// add-on SDK accepts (appAddress in the SDK), without a fragment.
var (
	appPath  = regexp.MustCompile(`^/app(?:/[A-Za-z0-9._~%-]+)*/?$`)
	appQuery = regexp.MustCompile(`^[A-Za-z0-9._~%&=+,:@/-]*$`)
)

// validReturnTo says whether s is an address of the web app: a path on this
// host (studiosvc.ValidReturnTo) that is /app or one of its pages, with an
// optional query, no fragment and no dot segment once decoded.
func validReturnTo(s string) bool {
	if !studiosvc.ValidReturnTo(s) || strings.Contains(s, "#") {
		return false
	}
	path, query, hasQuery := strings.Cut(s, "?")
	if !appPath.MatchString(path) || (hasQuery && (len(query) > maxAppQuery || !appQuery.MatchString(query))) {
		return false
	}
	for seg := range strings.SplitSeq(path, "/") {
		d, err := url.PathUnescape(seg)
		if err != nil || d == "." || d == ".." || strings.ContainsAny(d, "/\\") {
			return false
		}
	}
	return true
}

// oauthRedirectURI is the callback address of this server as the browser
// reaches it. It needs an origin the server can trust: LOCALAI_BASE_URL, a
// trusted proxy's forwarding headers, or a loopback host. Otherwise anyone
// could choose the address the service sends the code to, and the answer is
// false. The address must be https, unless its host is loopback or an
// administrator set it as LOCALAI_BASE_URL: over plain http the code, the
// state and the nonce cookie cross the network in clear text.
func oauthRedirectURI(c echo.Context) (string, bool) {
	ext, _ := c.Get("_external_base_url").(string)
	if ext == "" && !middleware.ForwardedHeadersTrusted(c) && !loopbackHost(c.Request().Host) {
		return "", false
	}
	uri := strings.TrimSuffix(middleware.BaseURL(c), "/") + OAuthCallbackPath
	if ext == "" {
		u, err := url.Parse(uri)
		if err != nil || (u.Scheme != "https" && !loopbackHost(u.Host)) {
			return "", false
		}
	}
	return uri, true
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// oauthActor is the actor of the request, built as the add-on routes build
// it. The route middleware already authenticated the request.
func (h *Handlers) oauthActor(c echo.Context) (contract.Actor, OAuthDispatcher, error) {
	var d OAuthDispatcher
	if h.oauth.Dispatcher != nil {
		d = h.oauth.Dispatcher()
	}
	if d == nil {
		return contract.Actor{}, nil, gateway.ErrUnavailable
	}
	a, err := auth.AddonActorForMode(c, h.oauth.Mode)
	if err != nil {
		return contract.Actor{}, nil, gateway.ErrForbidden
	}
	return a, d, nil
}

// failOAuth answers an error of an OAuth call.
func failOAuth(c echo.Context, err error) error {
	var oe *gateway.OAuthError
	var ue *gateway.UnavailableError
	switch {
	case errors.As(err, &oe):
		if oe.Type == gateway.OAuthApprovalStale {
			return c.JSON(oe.Status, schema.ChatsOAuthClientConflict{Error: &schema.APIError{Message: oe.Message, Code: oe.Status, Type: oe.Type}, CurrentDigest: oe.CurrentDigest})
		}
		return fail(c, oe.Status, oe.Type, oe.Message)
	case errors.As(err, &ue):
		return fail(c, http.StatusServiceUnavailable, gateway.OAuthUnavailable, ue.Reason)
	case errors.Is(err, gateway.ErrForbidden):
		return fail(c, http.StatusForbidden, typeAuthz, "this account may not use the logins of add-ons")
	}
	return fail(c, http.StatusServiceUnavailable, gateway.OAuthUnavailable, "the add-on owner is not reachable; try again")
}

// setNonceCookie sets the nonce cookie of the login with this suffix for as
// long as a login may take, or clears it when value is empty. The age is
// fixed, not taken from the owner's clock, so a clock skew between hosts
// cannot end it early.
func setNonceCookie(c echo.Context, suffix, value string) {
	maxAge := int(connectors.LoginStateTTL.Seconds())
	if value == "" {
		maxAge = -1
	}
	c.SetCookie(&http.Cookie{Name: oauthNonceCookie + suffix, Value: value, Path: "/", HttpOnly: true, Secure: c.Scheme() == "https", SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

// StartConnectorOAuth starts the login of an oauth2 binding.
// @Summary Start the login of a connection that uses OAuth.
// @Description Returns the address of the service's login page; open it in the same browser. The required scopes of the add-on are always asked for, plus the optional ones in scopes. The service sends the browser back to the callback route, which ends on return_to (a page of the web app; default the Connectors page). The response sets an HttpOnly cookie that binds the login to this browser for ten minutes; each login has its own cookie, named localai_oauth_nonce_ and a suffix of eight hex digits of the hash of its state. The callback address is built from LOCALAI_BASE_URL, or from the forwarding headers of a trusted proxy, or from a loopback host; without one of them, or when a trusted proxy forwards plain http for a host that is not loopback, the start is a 409 base_url_required. An administrator must have saved the add-on's client first (409 needs_approval).
// @Tags chats
// @Param id path string true "the binding's id"
// @Param request body schema.ChatsConnectorOAuthStart false "the optional scopes and the page to come back to"
// @Success 200 {object} schema.ChatsConnectorOAuthStarted "Where to send the browser"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 409 {object} schema.ErrorResponse "No approved client, or no trusted base URL"
// @Failure 429 {object} schema.ErrorResponse "Too many logins wait for their callback, or were started in the last minute (too_many_logins)"
// @Failure 503 {object} schema.ErrorResponse "The add-on owner or the add-on does not answer"
// @Router /api/chats/connectors/{id}/oauth/start [post]
func (h *Handlers) StartConnectorOAuth(c echo.Context) error {
	if _, ok := h.connectorHost(c); !ok {
		return nil
	}
	req, ok := bind[schema.ChatsConnectorOAuthStart](c, msgNotJSON)
	if !ok {
		return nil
	}
	returnTo := req.ReturnTo
	if returnTo == "" {
		returnTo = defaultOAuthReturn
	}
	if !validReturnTo(returnTo) {
		return fail(c, http.StatusBadRequest, typeInvalid, "return_to must be a page of the web app: a path under /app with an optional query and no fragment")
	}
	redirect, ok := oauthRedirectURI(c)
	if !ok {
		return fail(c, http.StatusConflict, "base_url_required", "the server cannot tell its own https address: set LOCALAI_BASE_URL, or reach it over https through a trusted proxy")
	}
	a, d, err := h.oauthActor(c)
	if err != nil {
		return failOAuth(c, err)
	}
	r, err := d.OAuth(c.Request().Context(), a, gateway.OAuthCall{Op: gateway.OAuthStart, BindingID: c.Param("id"), Scopes: req.Scopes, RedirectURI: redirect, ReturnTo: returnTo})
	if err != nil {
		return failOAuth(c, err)
	}
	if r.CookieSuffix == "" || r.Nonce == "" {
		return failOAuth(c, gateway.ErrUnavailable)
	}
	setNonceCookie(c, r.CookieSuffix, r.Nonce)
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, schema.ChatsConnectorOAuthStarted{AuthorizeURL: r.AuthorizeURL, Expires: r.Expires})
}

// ConnectorOAuthCallback finishes a login.
// @Summary Finish the login of a connection that uses OAuth.
// @Description The service sends the browser here after the login; register this address with the service as the redirect URI. It needs the session of the person who started the login (with authentication off, the nonce cookie alone binds the login to the browser). It always answers 303 to the page the login started from, with oauth=ok, oauth=denied (you declined; the connection stays as it was) or oauth=error and class=invalid_state, provider_error, exchange_failed, scopes_missing, token_too_large or unavailable. The state works once and for ten minutes. Nothing the service says is shown or logged.
// @Tags chats
// @Param state query string false "the state of the login"
// @Param code query string false "the code the service gave"
// @Param error query string false "the error the service gave"
// @Success 303 "To the page of the web app, with the outcome in the query"
// @Router /api/chats/connectors/oauth/callback [get]
func (h *Handlers) ConnectorOAuthCallback(c echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	c.Response().Header().Set("Referrer-Policy", "no-referrer")
	// The cookie of this login is found by the state in the query, and used
	// once, whatever the outcome. The cookies of other logins stay.
	nonce := ""
	if state := c.QueryParam("state"); state != "" {
		if ck, err := c.Cookie(oauthNonceCookie + studiosvc.LoginCookieSuffix(state)); err == nil {
			nonce = ck.Value
			setNonceCookie(c, studiosvc.LoginCookieSuffix(state), "")
		}
	}
	outcome, returnTo := h.completeOAuth(c, nonce)
	return c.Redirect(http.StatusSeeOther, oauthReturnURL(c, returnTo, outcome))
}

func (h *Handlers) completeOAuth(c echo.Context, nonce string) (outcome, returnTo string) {
	if h.svc.Connectors() == nil {
		return studiosvc.LoginUnavailable, ""
	}
	q := c.QueryParams()
	call := gateway.OAuthCall{Op: gateway.OAuthComplete, Nonce: nonce, State: q.Get("state"), Code: q.Get("code"), ProviderError: q.Get("error")}
	a, d, err := h.oauthActor(c)
	if errors.Is(err, gateway.ErrForbidden) {
		return studiosvc.LoginInvalidState, ""
	}
	if err != nil {
		return studiosvc.LoginUnavailable, ""
	}
	r, err := d.OAuth(c.Request().Context(), a, call)
	switch {
	case err == nil:
		return r.Outcome, r.ReturnTo
	case errors.Is(err, gateway.ErrUnavailable):
		return studiosvc.LoginUnavailable, ""
	}
	// A call too long to forward, or an account the host refuses, cannot
	// be the login of this browser.
	return studiosvc.LoginInvalidState, ""
}

// oauthReturnURL is the address the callback redirects to: the page the login
// started from, under the path prefix of the server, with the outcome in its
// query. An unknown outcome is unavailable.
func oauthReturnURL(c echo.Context, returnTo, outcome string) string {
	if !validReturnTo(returnTo) {
		returnTo = defaultOAuthReturn
	}
	path, query, _ := strings.Cut(returnTo, "?")
	q, err := url.ParseQuery(query)
	if err != nil {
		q = url.Values{}
	}
	q.Del("oauth")
	q.Del("class")
	switch {
	case outcome == studiosvc.LoginOK || outcome == studiosvc.LoginDenied:
		q.Set("oauth", outcome)
	case oauthErrorClasses[outcome]:
		q.Set("oauth", "error")
		q.Set("class", outcome)
	default:
		q.Set("oauth", "error")
		q.Set("class", studiosvc.LoginUnavailable)
	}
	return middleware.BasePathPrefix(c) + strings.TrimPrefix(path, "/") + "?" + q.Encode()
}

// DisconnectConnectorOAuth ends the login of an oauth2 binding.
// @Summary Disconnect a connection that uses OAuth.
// @Description Asks the service to revoke the login when the add-on declares a revocation address, then removes the login here in any case. revoked says whether the service confirmed it; when it is false, revoke the access at the service too.
// @Tags chats
// @Param id path string true "the binding's id"
// @Success 200 {object} schema.ChatsConnectorOAuthDisconnected "Whether the service was told"
// @Failure 404 {object} schema.ErrorResponse "No such binding"
// @Router /api/chats/connectors/{id}/oauth/disconnect [post]
func (h *Handlers) DisconnectConnectorOAuth(c echo.Context) error {
	if _, ok := h.connectorHost(c); !ok {
		return nil
	}
	a, d, err := h.oauthActor(c)
	if err != nil {
		return failOAuth(c, err)
	}
	r, err := d.OAuth(c.Request().Context(), a, gateway.OAuthCall{Op: gateway.OAuthDisconnect, BindingID: c.Param("id")})
	if err != nil {
		return failOAuth(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsConnectorOAuthDisconnected{Revoked: r.Revoked})
}

// revokeBeforeDelete asks the add-on owner to disconnect an OAuth binding of
// the person before it is deleted: only the owner reads the descriptor that
// names the revocation endpoint, and a frontend's own service cannot. It is
// best effort; the delete follows whatever the answer.
func (h *Handlers) revokeBeforeDelete(c echo.Context, host *studiosvc.Connectors) {
	ctx := c.Request().Context()
	i, err := host.GetConnector(ctx, userID(c), c.Param("id"))
	if err != nil || !i.UsesOAuth() || i.OAuth == nil || i.OAuth.State == studiosvc.OAuthNotConnected {
		return
	}
	a, d, err := h.oauthActor(c)
	if err != nil {
		return
	}
	_, _ = d.OAuth(ctx, a, gateway.OAuthCall{Op: gateway.OAuthDisconnect, BindingID: i.ID})
}

// GetOAuthClient reads the OAuth client of an add-on.
// @Summary Read the OAuth client of an add-on (administrator).
// @Description The client ID, whether a secret is stored (never the secret), the approval and the login the add-on declares, with the callback address to register with the service. No MCP tool reads or writes it.
// @Tags chats
// @Param addonId path string true "the add-on's id"
// @Success 200 {object} schema.ChatsOAuthClient "The client"
// @Failure 503 {object} schema.ErrorResponse "The add-on does not run"
// @Router /api/chats/admin/oauth-clients/{addonId} [get]
func (h *Handlers) GetOAuthClient(c echo.Context) error {
	return h.oauthClient(c, gateway.OAuthCall{Op: gateway.OAuthClientGet, AddonID: c.Param("addonId")})
}

// PutOAuthClient saves the OAuth client of an add-on.
// @Summary Save the OAuth client of an add-on and approve its login (administrator).
// @Description Saving is the approval of the login the add-on declares now: send its current_digest as descriptor_digest. When the login changed since, nothing is saved and the answer is 409 with the new current_digest. client_secret is write-only; absent keeps the stored one. No MCP tool reads or writes it.
// @Tags chats
// @Param addonId path string true "the add-on's id"
// @Param request body schema.ChatsOAuthClientWrite true "the client"
// @Success 200 {object} schema.ChatsOAuthClient "The client"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 409 {object} schema.ChatsOAuthClientConflict "The login changed since the form showed it"
// @Router /api/chats/admin/oauth-clients/{addonId} [put]
func (h *Handlers) PutOAuthClient(c echo.Context) error {
	req, ok := bind[schema.ChatsOAuthClientWrite](c, msgNotJSON)
	if !ok {
		return nil
	}
	return h.oauthClient(c, gateway.OAuthCall{Op: gateway.OAuthClientSet, AddonID: c.Param("addonId"), ClientID: req.ClientID, ClientSecret: req.ClientSecret, Digest: req.DescriptorDigest})
}

func (h *Handlers) oauthClient(c echo.Context, call gateway.OAuthCall) error {
	if _, ok := h.connectorHost(c); !ok {
		return nil
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	a, d, err := h.oauthActor(c)
	if err != nil {
		return failOAuth(c, err)
	}
	r, err := d.OAuth(c.Request().Context(), a, call)
	if err != nil {
		return failOAuth(c, err)
	}
	if r.Client == nil {
		return failOAuth(c, gateway.ErrUnavailable)
	}
	out := oauthClientOut(call.AddonID, *r.Client)
	if u, ok := oauthRedirectURI(c); ok {
		out.CallbackURL = u
	}
	return c.JSON(http.StatusOK, out)
}

func oauthClientOut(addonID string, v gateway.OAuthClientView) schema.ChatsOAuthClient {
	d := v.Descriptor
	out := schema.ChatsOAuthClient{Addon: addonID, Configured: v.Configured, ClientID: v.ClientID, HasSecret: v.HasSecret, Approved: v.Approved,
		ApprovedDigest: v.ApprovedDigest, CurrentDigest: v.CurrentDigest, ApprovedBy: v.ApprovedBy,
		Descriptor: schema.ChatsOAuthDescriptor{AuthorizeURL: d.AuthorizeURL, TokenURL: d.TokenURL, RevokeURL: d.RevokeURL, Hosts: xslices.NonNil(d.Hosts),
			RequiredScopes: xslices.NonNil(d.Scopes.Required), OptionalScopes: xslices.NonNil(d.Scopes.Optional), PKCE: d.PKCE, ClientAuth: d.ClientAuth,
			AuthorizeParams: []schema.ChatsOAuthParam{}}}
	for _, p := range d.AuthorizeParams {
		out.Descriptor.AuthorizeParams = append(out.Descriptor.AuthorizeParams, schema.ChatsOAuthParam{Name: p.Name, Value: p.Value})
	}
	if !v.ApprovedAt.IsZero() {
		t := v.ApprovedAt
		out.ApprovedAt = &t
	}
	return out
}

// oauthOut is the login state of an oauth2 binding in the connector JSON.
func oauthOut(v *studiosvc.OAuthView) *schema.ChatsConnectorOAuth {
	if v == nil {
		return nil
	}
	out := &schema.ChatsConnectorOAuth{State: v.State, Reason: v.Reason, AccountLabel: v.AccountLabel, Scopes: xslices.NonNil(v.Scopes), HasClient: v.HasClient}
	if !v.ExpiresAt.IsZero() {
		t := v.ExpiresAt
		out.ExpiresAt = &t
	}
	return out
}

// authKindOut names the kind of a binding that uses a login; a token binding
// has none, as it always had.
func authKindOut(k string) string {
	if k == connectors.AuthToken {
		return ""
	}
	return k
}
