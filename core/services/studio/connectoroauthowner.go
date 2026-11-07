// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// OAuthOwner serves the OAuth calls of the add-on gateway
// (gateway.OAuthService) on the process that runs the add-ons, where the
// descriptors are read. Every frontend forwards its calls here, so a login
// started on one frontend finishes on any other and every frontend gives
// the same answer.
type OAuthOwner struct {
	// Connectors returns the connector service, or nil when it does not run.
	// It is read at call time, since it can start after the gateway.
	Connectors func() *Connectors
	// Principal is the person of the chats an actor stands for; "" refuses.
	Principal func(contract.Actor) string
	// Admin checks, against current storage, that the actor is an
	// administrator. A nil Admin refuses every client call.
	Admin func(context.Context, contract.Actor) error
}

// ServeOAuth runs one call. A refusal is an *gateway.OAuthError with host
// text, gateway.ErrForbidden for an actor that names no person, or
// gateway.ErrUnavailable; the cause of an outage is never returned.
func (o OAuthOwner) ServeOAuth(ctx context.Context, a contract.Actor, call gateway.OAuthCall) (gateway.OAuthReply, error) {
	user := ""
	if o.Principal != nil {
		user = o.Principal(a)
	}
	if user == "" {
		return gateway.OAuthReply{}, gateway.ErrForbidden
	}
	var c *Connectors
	if o.Connectors != nil {
		c = o.Connectors()
	}
	if c == nil {
		return gateway.OAuthReply{}, oauthFailure(ErrConnectorsOff)
	}
	switch call.Op {
	case gateway.OAuthStart:
		ls, err := c.StartLogin(ctx, user, call.BindingID, call.Scopes, call.RedirectURI, call.ReturnTo)
		if err != nil {
			return gateway.OAuthReply{}, oauthFailure(err)
		}
		return gateway.OAuthReply{AuthorizeURL: ls.AuthorizeURL, CookieSuffix: ls.CookieSuffix, Nonce: ls.Nonce, Expires: ls.Expires}, nil
	case gateway.OAuthComplete:
		// The outcome is the answer, also when the store failed: the
		// callback redirects with the class and the person may try again.
		res, _ := c.CompleteLogin(ctx, user, call.Nonce, call.State, call.Code, call.ProviderError)
		return gateway.OAuthReply{Outcome: res.Outcome, ReturnTo: res.ReturnTo, BindingID: res.BindingID}, nil
	case gateway.OAuthDisconnect:
		told, err := c.Disconnect(ctx, user, call.BindingID)
		if err != nil {
			return gateway.OAuthReply{}, oauthFailure(err)
		}
		return gateway.OAuthReply{Revoked: told}, nil
	case gateway.OAuthClientGet, gateway.OAuthClientSet:
		if o.Admin == nil || o.Admin(ctx, a) != nil {
			return gateway.OAuthReply{}, &gateway.OAuthError{Status: http.StatusForbidden, Type: gateway.OAuthForbidden, Message: "administrator role required"}
		}
		if call.Op == gateway.OAuthClientGet {
			st, err := c.GetClientStatus(ctx, call.AddonID)
			if err != nil {
				return gateway.OAuthReply{}, oauthFailure(err)
			}
			return gateway.OAuthReply{Client: clientView(st)}, nil
		}
		st, err := c.SetClient(ctx, call.AddonID, OAuthClientInput{Digest: call.Digest, ClientID: call.ClientID, Secret: call.ClientSecret, ApprovedBy: a.UserID})
		if oe, ok := oauthRefusal(err); ok && errors.Is(err, ErrApprovalStale) {
			if cur, cerr := c.GetClientStatus(ctx, call.AddonID); cerr == nil {
				oe.CurrentDigest = cur.CurrentDigest
			}
			return gateway.OAuthReply{}, oe
		}
		if err != nil {
			return gateway.OAuthReply{}, oauthFailure(err)
		}
		return gateway.OAuthReply{Client: clientView(st)}, nil
	}
	return gateway.OAuthReply{}, gateway.ErrForbidden
}

func clientView(s OAuthClientStatus) *gateway.OAuthClientView {
	return &gateway.OAuthClientView{Configured: s.Configured, ClientID: s.ClientID, HasSecret: s.HasSecret, Approved: s.Approved,
		ApprovedDigest: s.ApprovedDigest, CurrentDigest: s.CurrentDigest, ApprovedBy: s.ApprovedBy, ApprovedAt: s.ApprovedAt, Descriptor: s.Descriptor}
}

// oauthRule maps the errors that match is (errors.Is) to a refusal. An empty
// msg means the reason the error carries (connectors.InvalidError).
type oauthRule struct {
	is     error
	status int
	typ    string
	msg    string
}

// oauthRules are tried in order. Only host text goes in a refusal.
var oauthRules = []oauthRule{
	{connectors.ErrNotFound, http.StatusNotFound, gateway.OAuthNotFound, "connector not found"},
	// The reason of an invalid input is written for the person and travels
	// in the error.
	{connectors.ErrInvalid, http.StatusBadRequest, gateway.OAuthInvalid, ""},
	{ErrNeedsApproval, http.StatusConflict, gateway.OAuthNeedsApproval, msgNeedsApproval},
	{ErrApprovalStale, http.StatusConflict, gateway.OAuthApprovalStale, msgApprovalStale},
	{ErrOAuthUnavailable, http.StatusServiceUnavailable, gateway.OAuthUnavailable, "the login of this add-on is not available now; check that the add-on runs"},
	{ErrConnectorsOff, http.StatusConflict, gateway.OAuthConnectorsOff, msgConnectorsOff},
	{ErrTooManyLogins, http.StatusTooManyRequests, gateway.OAuthTooManyLogins, "too many logins were started; finish one, or wait a few minutes and try again"},
}

// oauthRefusal maps an error of the connector service to the typed refusal
// that crosses the carrier. ok is false for an outage.
func oauthRefusal(err error) (*gateway.OAuthError, bool) {
	for _, r := range oauthRules {
		if !errors.Is(err, r.is) {
			continue
		}
		msg := r.msg
		if msg == "" {
			msg = invalidReason(err)
		}
		return &gateway.OAuthError{Status: r.status, Type: r.typ, Message: msg}, true
	}
	return nil, false
}

// invalidReason is the reason an invalid input carries, or the text of the
// sentinel when the input was refused without one.
func invalidReason(err error) string {
	var invalid *connectors.InvalidError
	if errors.As(err, &invalid) {
		return invalid.Reason
	}
	return connectors.ErrInvalid.Error()
}

// oauthFailure is oauthRefusal as an error: gateway.ErrUnavailable for an
// outage, whose cause is never returned.
func oauthFailure(err error) error {
	if oe, ok := oauthRefusal(err); ok {
		return oe
	}
	return gateway.ErrUnavailable
}

// The states of an OAuth connection that the lists show.
const (
	// OAuthConnected: the login works.
	OAuthConnected = connectors.GrantConnected
	// OAuthReconnectNeeded: the login ended; Reason says why.
	OAuthReconnectNeeded = connectors.GrantReconnectNeeded
	// OAuthNotConnected: nobody logged in yet, and the add-on has a client.
	OAuthNotConnected = "not_connected"
	// OAuthNeedsApproval: an administrator must save the add-on's client
	// first, or save it again after its descriptor changed.
	OAuthNeedsApproval = "needs_approval"
)

// OAuthView is the state of the login of an OAuth binding, without any
// secret. It is read from the shared store alone, so every frontend shows the
// same; a descriptor that changed since the approval shows as needs_approval
// once a call or a refresh found it.
type OAuthView struct {
	State, Reason, AccountLabel string
	Scopes                      []string
	ExpiresAt                   time.Time
	// HasClient is true when an administrator saved a client for the add-on.
	HasClient bool
}

func (c *Connectors) oauthView(ctx context.Context, b connectors.Binding) *OAuthView {
	v := &OAuthView{Scopes: []string{}}
	if _, err := c.o.Store.GetOAuthClient(ctx, b.AddonID); err == nil {
		v.HasClient = true
	}
	g, err := c.o.Store.GetGrant(ctx, b.ID)
	switch {
	case err != nil || !g.BelongsTo(b):
		v.State = OAuthNotConnected
		if !v.HasClient {
			v.State = OAuthNeedsApproval
		}
		return v
	case g.State == connectors.GrantConnected:
		v.State = OAuthConnected
	case g.State == connectors.GrantNeedsApproval:
		v.State, v.Reason = OAuthNeedsApproval, string(ReasonNeedsApproval)
	default:
		v.State, v.Reason = OAuthReconnectNeeded, g.Reason
	}
	v.AccountLabel, v.ExpiresAt = g.AccountLabel, g.ExpiresAt
	if g.Scopes != nil {
		v.Scopes = g.Scopes
	}
	return v
}
