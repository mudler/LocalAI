// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The OAuth login of a connection and the OAuth client of an add-on are
// served by the owner: only the process that runs the add-ons reads the
// verified manifest that holds the OAuth descriptor. A frontend forwards each
// call with the signed actor. The browser nonce, the state, the code and the
// client secret travel as typed fields of the call over the pinned carrier,
// never as headers, and no text form of a call shows them.

// callOAuth is the capability of an owner that serves the OAuth calls.
const callOAuth = "oauth"

// reasonOAuthUnsupported is the 503 reason of an owner without the calls.
const reasonOAuthUnsupported = "the add-on owner does not support OAuth logins; upgrade the owner"

// The operations of an OAuth call.
const (
	OAuthStart      = "start"
	OAuthComplete   = "complete"
	OAuthDisconnect = "disconnect"
	OAuthClientGet  = "client-get"
	OAuthClientSet  = "client-set"
)

// Bounds of an OAuth call. A callback query or a client field that is longer
// is refused before it is forwarded.
const (
	maxOAuthField  = 4096
	maxOAuthScopes = 32
)

// OAuthCall is one OAuth call of a person or an administrator.
type OAuthCall struct {
	Op        string   `json:"op"`
	BindingID string   `json:"bindingId,omitempty"`
	AddonID   string   `json:"addonId,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
	// RedirectURI is the callback address the frontend built; ReturnTo the
	// app path the person comes back to.
	RedirectURI string `json:"redirectUri,omitempty"`
	ReturnTo    string `json:"returnTo,omitempty"`
	// Nonce is the value of the browser's nonce cookie; State, Code and
	// ProviderError are the query of the callback.
	Nonce         string `json:"nonce,omitempty"`
	State         string `json:"state,omitempty"`
	Code          string `json:"code,omitempty"`
	ProviderError string `json:"providerError,omitempty"`
	// Digest is the descriptor digest the administrator saw; ClientID and
	// ClientSecret the client they saved. A nil secret keeps the stored one.
	Digest       string  `json:"digest,omitempty"`
	ClientID     string  `json:"clientId,omitempty"`
	ClientSecret *string `json:"clientSecret,omitempty"`
}

func (c OAuthCall) wireAction() string { return "oauth-" + c.Op }

func (c OAuthCall) valid() bool {
	for _, f := range []string{c.BindingID, c.AddonID, c.RedirectURI, c.ReturnTo, c.Nonce, c.State, c.Code, c.ProviderError, c.Digest, c.ClientID} {
		if len(f) > maxOAuthField {
			return false
		}
	}
	if c.ClientSecret != nil && len(*c.ClientSecret) > maxOAuthField {
		return false
	}
	if len(c.Scopes) > maxOAuthScopes {
		return false
	}
	for _, s := range c.Scopes {
		if len(s) > maxOAuthField {
			return false
		}
	}
	switch c.Op {
	case OAuthStart, OAuthDisconnect:
		return c.BindingID != ""
	case OAuthComplete:
		return true
	case OAuthClientGet, OAuthClientSet:
		return c.AddonID != ""
	}
	return false
}

// String, GoString and LogValue name the call without its secrets, so no
// format verb or log line prints them.
func (c OAuthCall) String() string {
	return fmt.Sprintf("oauth %s binding=%q addon=%q", c.Op, c.BindingID, c.AddonID)
}

func (c OAuthCall) GoString() string { return c.String() }

func (c OAuthCall) LogValue() slog.Value {
	return slog.GroupValue(slog.String("op", c.Op), slog.String("binding", c.BindingID), slog.String("addon", c.AddonID))
}

// OAuthReply is the answer of an OAuth call. Only the fields of its op are
// set.
type OAuthReply struct {
	// AuthorizeURL, CookieSuffix, Nonce and Expires answer a start. The nonce
	// goes in the browser's HttpOnly cookie and nowhere else; CookieSuffix
	// ends the name of that cookie.
	AuthorizeURL string    `json:"authorizeUrl,omitempty"`
	CookieSuffix string    `json:"cookieSuffix,omitempty"`
	Nonce        string    `json:"nonce,omitempty"`
	Expires      time.Time `json:"expires,omitzero"`
	// Outcome, ReturnTo and BindingID answer a callback. Outcome is a fixed
	// class.
	Outcome   string `json:"outcome,omitempty"`
	ReturnTo  string `json:"returnTo,omitempty"`
	BindingID string `json:"bindingId,omitempty"`
	// Revoked answers a disconnect: the service confirmed the revocation.
	Revoked bool `json:"revoked,omitempty"`
	// Client answers the client calls. It never holds the secret.
	Client *OAuthClientView `json:"client,omitempty"`
}

// OAuthClientView is the OAuth client of an add-on without its secret.
type OAuthClientView struct {
	Configured     bool                     `json:"configured"`
	ClientID       string                   `json:"clientId"`
	HasSecret      bool                     `json:"hasSecret"`
	Approved       bool                     `json:"approved"`
	ApprovedDigest string                   `json:"approvedDigest"`
	CurrentDigest  string                   `json:"currentDigest"`
	ApprovedBy     string                   `json:"approvedBy"`
	ApprovedAt     time.Time                `json:"approvedAt,omitzero"`
	Descriptor     contract.OAuthDescriptor `json:"descriptor"`
}

// The types of an OAuthError.
const (
	OAuthNotFound      = "not_found"
	OAuthInvalid       = "invalid_request"
	OAuthNeedsApproval = "needs_approval"
	OAuthApprovalStale = "approval_stale"
	OAuthForbidden     = "forbidden"
	OAuthConnectorsOff = "connectors_off"
	OAuthUnavailable   = "unavailable"
	OAuthTooManyLogins = "too_many_logins"
)

// OAuthError is a refusal the owner decided. Every frontend gives the same
// status, type and message. The message is host text; it never carries what
// the service said.
type OAuthError struct {
	Status  int    `json:"status"`
	Type    string `json:"type"`
	Message string `json:"message"`
	// CurrentDigest is the digest of the descriptor now, for approval_stale.
	CurrentDigest string `json:"currentDigest,omitempty"`
}

func (e *OAuthError) Error() string { return e.Message }

// OAuthService serves the OAuth calls on the owner.
type OAuthService interface {
	ServeOAuth(ctx context.Context, a contract.Actor, call OAuthCall) (OAuthReply, error)
}

type oauthReply struct {
	Data  *OAuthReply `json:"data,omitempty"`
	Error *OAuthError `json:"error,omitempty"`
}

// open needs exactly one of data and error.
func (r *oauthReply) open() (OAuthReply, bool, error) {
	if (r.Data == nil) == (r.Error == nil) {
		return OAuthReply{}, false, nil
	}
	if r.Error != nil {
		return OAuthReply{}, true, r.Error
	}
	return *r.Data, true, nil
}

func init() { markRead("oauth-" + OAuthClientGet) }

// OAuth runs one OAuth call: on the owner itself, or through the carrier. A
// frontend never answers from its own service.
func (g *Gateway) OAuth(ctx context.Context, a contract.Actor, call OAuthCall) (OAuthReply, error) {
	if !call.valid() {
		return OAuthReply{}, ErrForbidden
	}
	if e := g.revalidate(ctx, a); e != nil {
		return OAuthReply{}, e
	}
	if g.Forward != nil {
		return g.Forward.OAuth(ctx, a, call)
	}
	return g.oauthLocal(ctx, a, call)
}

func (g *Gateway) oauthLocal(ctx context.Context, a contract.Actor, call OAuthCall) (OAuthReply, error) {
	if g.OAuthOwner == nil {
		return OAuthReply{}, ErrUnavailable
	}
	return g.OAuthOwner.ServeOAuth(ctx, a, call)
}

// oauthLocalReply runs a forwarded call on the owner and writes the reply
// envelope.
func (g *Gateway) oauthLocalReply(ctx context.Context, w http.ResponseWriter, a contract.Actor, call OAuthCall) {
	r, e := g.oauthLocal(ctx, a, call)
	writeReply(w, e, oauthReply{Data: &r}, decided(func(oe *OAuthError) any { return oauthReply{Error: oe} }))
}

// OAuth forwards one OAuth call to the owner. A refusal the owner decided is
// an *OAuthError. An owner without the calls is unavailable with its reason.
func (c *Carrier) OAuth(ctx context.Context, a contract.Actor, call OAuthCall) (OAuthReply, error) {
	if !call.valid() {
		return OAuthReply{}, ErrForbidden
	}
	oc := ownerCall{need: callOAuth, reason: reasonOAuthUnsupported, action: call.wireAction(), limit: 1 << 20}
	return forward[OAuthReply, oauthReply](ctx, c, a, oc, wireRequest{OAuth: &call})
}
