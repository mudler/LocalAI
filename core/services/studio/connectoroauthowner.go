// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"errors"
	"strings"
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
		return gateway.OAuthReply{AuthorizeURL: ls.AuthorizeURL, Nonce: ls.Nonce, Expires: ls.Expires}, nil
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
			return gateway.OAuthReply{}, &gateway.OAuthError{Status: 403, Type: gateway.OAuthForbidden, Message: "administrator role required"}
		}
		if call.Op == gateway.OAuthClientGet {
			st, err := c.GetClientStatus(ctx, call.AddonID)
			if err != nil {
				return gateway.OAuthReply{}, oauthFailure(err)
			}
			return gateway.OAuthReply{Client: clientView(st)}, nil
		}
		// Saving is the approval: it is a person's act on the host's form,
		// never a key, a token or a tool session.
		if !browserPerson(a) {
			return gateway.OAuthReply{}, &gateway.OAuthError{Status: 403, Type: gateway.OAuthForbidden, Message: "save the client from your own browser session"}
		}
		st, err := c.SetClient(ctx, call.AddonID, OAuthClientInput{Digest: call.Digest, ClientID: call.ClientID, Secret: call.ClientSecret, ApprovedBy: a.UserID})
		if errors.Is(err, ErrApprovalStale) {
			oe := oauthFailure(err).(*gateway.OAuthError)
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

// browserPerson says whether the credential of the actor is a browser
// session, or the one local owner of an install without accounts.
func browserPerson(a contract.Actor) bool {
	if a.Mode == "local-owner" {
		return a.CredentialID != "" && a.CredentialID == a.UserID
	}
	id, ok := strings.CutPrefix(a.CredentialID, "session:")
	return ok && id != ""
}

func clientView(s OAuthClientStatus) *gateway.OAuthClientView {
	return &gateway.OAuthClientView{Configured: s.Configured, ClientID: s.ClientID, HasSecret: s.HasSecret, Approved: s.Approved,
		ApprovedDigest: s.ApprovedDigest, CurrentDigest: s.CurrentDigest, ApprovedBy: s.ApprovedBy, ApprovedAt: s.ApprovedAt, Descriptor: s.Descriptor}
}

// oauthFailure maps an error of the connector service to the typed refusal
// that crosses the carrier. Only host text goes in it.
func oauthFailure(err error) error {
	fail := func(status int, kind, msg string) error {
		return &gateway.OAuthError{Status: status, Type: kind, Message: msg}
	}
	switch {
	case errors.Is(err, connectors.ErrNotFound):
		return fail(404, gateway.OAuthNotFound, "connector not found")
	case errors.Is(err, connectors.ErrInvalid):
		return fail(400, gateway.OAuthInvalid, strings.TrimPrefix(err.Error(), connectors.ErrInvalid.Error()+": "))
	case errors.Is(err, ErrNeedsApproval):
		return fail(409, gateway.OAuthNeedsApproval, "an administrator must approve this add-on first")
	case errors.Is(err, ErrApprovalStale):
		return fail(409, gateway.OAuthApprovalStale, "the add-on's login changed; review it again")
	case errors.Is(err, ErrOAuthUnavailable):
		return fail(503, gateway.OAuthUnavailable, "the login of this add-on is not available now; check that the add-on runs")
	case errors.Is(err, ErrConnectorsOff):
		return fail(409, gateway.OAuthConnectorsOff, "connectors are not available")
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
	case err != nil || g.Owner != b.Owner || g.AddonID != b.AddonID:
		v.State = OAuthNotConnected
		if !v.HasClient {
			v.State = OAuthNeedsApproval
		}
		return v
	case g.State == connectors.GrantConnected:
		v.State = OAuthConnected
	case g.Reason == ReasonNeedsApproval:
		v.State, v.Reason = OAuthNeedsApproval, g.Reason
	default:
		v.State, v.Reason = OAuthReconnectNeeded, g.Reason
	}
	v.AccountLabel, v.ExpiresAt = g.AccountLabel, g.ExpiresAt
	if g.Scopes != nil {
		v.Scopes = g.Scopes
	}
	return v
}
