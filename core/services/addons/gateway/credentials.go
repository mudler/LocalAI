// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Codes of a CredentialRefusal. They are also the wire types of the owner's
// 403 answer, so a forwarding frontend gives the answer the owner gave.
const (
	// CodeResourceNotAllowed means the resource the call names is not on the
	// list of any binding of the person.
	CodeResourceNotAllowed = "resource_not_allowed"
	// CodeNoCredential means the person has no binding that holds a
	// credential for the add-on.
	CodeNoCredential = "no_credential" // #nosec G101 -- an error code, not a credential
	// CodeWriteNotAllowed means the binding that holds the credential has its
	// write mode off, so a write tool does not run.
	CodeWriteNotAllowed = "write_not_allowed"
	// CodeSelectConnection means the person has not selected the connection
	// this call uses: the chat and its agent name none, and the person has
	// several or none. The host picks no connection for them.
	CodeSelectConnection = "select_connection"
	// CodeReconnectNeeded means the person's connection to the service has
	// expired or was revoked and the host cannot renew it. Nothing was sent.
	// The person must connect again.
	CodeReconnectNeeded = "reconnect_needed"
	// CodeOwnerReconnectNeeded means the connection was shared with the person
	// and its owner's login to the service has ended or was never made. Only
	// the owner can connect it again. Nothing was sent.
	CodeOwnerReconnectNeeded = "owner_reconnect_needed"
)

// refusalCode reports whether t is the code of a CredentialRefusal, as it
// travels on the wire between the owner and a forwarding frontend.
func refusalCode(t string) bool {
	switch t {
	case CodeResourceNotAllowed, CodeNoCredential, CodeWriteNotAllowed, CodeSelectConnection, CodeReconnectNeeded, CodeOwnerReconnectNeeded:
		return true
	}
	return false
}

type connectionKey struct{}

// WithConnection returns a context that names the binding a credentialed call
// must use. Only host code sets it: the broker, when it applies a proposal
// with the connection the person saw at the preview. A caller that sets it
// gets that binding or a refusal, never another one.
func WithConnection(ctx context.Context, bindingID string) context.Context {
	return context.WithValue(ctx, connectionKey{}, bindingID)
}

// ConnectionOverride returns the binding a context names, or "".
func ConnectionOverride(ctx context.Context) string {
	v, _ := ctx.Value(connectionKey{}).(string)
	return v
}

// Write modes of a binding, as the host stores them. A binding with none is
// WriteOff.
const (
	WriteOff     = "off"
	WritePropose = "propose"
	WriteAuto    = "auto"
)

// CredentialGrant is what the owner adds to a call of an add-on that declares
// credentials. It is host-made: no argument of the call and nothing the add-on
// says can set it.
type CredentialGrant struct {
	// Credential is the secret of the binding, unsealed for this call.
	Credential string
	// ResourceKey and ResourceID are the entry of the list that matched. Both
	// are empty for a call that names no resource.
	ResourceKey, ResourceID string
	// GrantedScopes are the OAuth scopes the binding holds. They go in
	// Request.GrantedScopes; they are not secret and are empty for a binding
	// that is not an OAuth login.
	GrantedScopes []string
	// WriteMode is the write mode of the binding the credential comes from:
	// WriteOff, WritePropose or WriteAuto. Empty means WriteOff. It decides
	// whether a write tool runs and whether a person must confirm it.
	WriteMode string
	// BindingID and BindingName say which connection the credential comes
	// from. The proposal, the result of the call and the audit line carry them.
	BindingID, BindingName string
	// Version is the version of the OAuth login the credential comes from,
	// for CredentialRenewer. Zero means a credential that cannot be renewed,
	// such as a token the person gave. It never reaches the add-on.
	Version int64
	// Hold, when set, runs the call that uses the credential as admitted work
	// of its source: the binding is another person's, shared with the caller,
	// and a lowering or a revocation of that share cancels the context it gives
	// run and waits for it. It returns without calling run when the caller no
	// longer holds the share. Nil for the caller's own binding. It never
	// leaves the process.
	Hold func(ctx context.Context, run func(context.Context) error) error `json:"-"`
}

// Use runs run inside the hold of the grant, or directly when it has none.
func (g CredentialGrant) Use(ctx context.Context, run func(context.Context) error) error {
	if g.Hold == nil {
		return run(ctx)
	}
	return g.Hold(ctx, run)
}

// SetOn puts the credential, the resource key and the granted scopes of g on
// r. The resource ID is left to the caller: it decides the resource of the
// call, which the caller checks or sets once.
func (g CredentialGrant) SetOn(r *contract.Request) {
	r.Credential, r.ResourceKey, r.GrantedScopes = g.Credential, g.ResourceKey, g.GrantedScopes
}

// CredentialSource finds the credential and the allowed resource for a call.
// It is wired on the owner, where the add-on runs: a frontend forwards the
// call and never adds a credential itself. An error it returns that is not a
// *CredentialRefusal or a *contract.ErrRateLimited is an outage.
type CredentialSource interface {
	// Credential returns the grant for actor and add-on. A non-empty key must
	// be on the list of a binding of the person (compared without regard to
	// case). The host calls it before the add-on is called.
	Credential(ctx context.Context, actor contract.Actor, addonID, key string) (CredentialGrant, error)
}

// CredentialRenewer is a CredentialSource that can renew a credential the
// add-on rejected (contract.ErrCredentialRejected). It is the owner's, like the
// source: the retry runs where the add-on runs, so only its final answer
// crosses the carrier.
type CredentialRenewer interface {
	// RenewCredential renews the rejected grant, or returns the newer one
	// when the login changed since the call began. With final the add-on
	// rejected a renewed grant too: the source ends the login and returns the
	// reconnect_needed refusal.
	RenewCredential(ctx context.Context, actor contract.Actor, addonID string, rejected CredentialGrant, final bool) (CredentialGrant, error)
}

// WithRenewal runs call with grant. When the add-on rejects the credential and
// src can renew it, the host renews it once and runs call once more with the
// new grant. A second rejection ends the login, and the answer is the
// reconnect_needed refusal. A grant of version zero is never renewed: the
// rejection is returned as it is.
func WithRenewal(ctx context.Context, src CredentialSource, a contract.Actor, addonID string, grant CredentialGrant, call func(CredentialGrant) error) error {
	err := call(grant)
	rn, ok := src.(CredentialRenewer)
	if !errors.Is(err, contract.ErrCredentialRejected) || !ok || grant.Version == 0 {
		return err
	}
	next, rerr := rn.RenewCredential(ctx, a, addonID, grant, false)
	if rerr != nil {
		return credentialError(rerr)
	}
	if err = call(next); !errors.Is(err, contract.ErrCredentialRejected) {
		return err
	}
	_, rerr = rn.RenewCredential(ctx, a, addonID, next, true)
	if rerr == nil {
		return ErrUnavailable
	}
	return credentialError(rerr)
}

// credentialError keeps the typed answers of a credential source, a refusal
// or the service's request to wait, and makes anything else a check result.
func credentialError(err error) error {
	var refusal *CredentialRefusal
	if errors.As(err, &refusal) {
		return refusal
	}
	var limited *contract.ErrRateLimited
	if errors.As(err, &limited) {
		return limited
	}
	return ClassifyCheck(err)
}

// DefaultCredentialSource is a CredentialSource that can also serve a call
// that leaves out an optional resource argument (see
// contract.CredentialSlot.ResourceOptional): it uses the one resource that the
// binding lists. A binding that lists several is a *CredentialRefusal with
// CodeResourceNotAllowed, which names them. A source that does not implement it
// refuses such a call as invalid.
type DefaultCredentialSource interface {
	DefaultCredential(ctx context.Context, actor contract.Actor, addonID string) (CredentialGrant, error)
}

// CredentialFor asks src for the grant of a call. omitted says that the call
// left out an optional resource argument, and key is then empty.
func CredentialFor(ctx context.Context, src CredentialSource, actor contract.Actor, addonID, key string, omitted bool) (CredentialGrant, error) {
	if !omitted {
		return src.Credential(ctx, actor, addonID, key)
	}
	d, ok := src.(DefaultCredentialSource)
	if !ok {
		return CredentialGrant{}, fmt.Errorf("%w: %w", ErrInvalidRequest, contract.ErrRefused)
	}
	return d.DefaultCredential(ctx, actor, addonID)
}

// CredentialRefusal is the host's refusal of a call before the add-on was
// called. Nothing was sent. It is a denial, and a repeat of the same call is
// denied again until the person changes their binding.
type CredentialRefusal struct {
	Code string
	// Allowed lists the resources of the person's bindings, for
	// CodeResourceNotAllowed, so the model can pick a listed one.
	Allowed []string
}

func (r *CredentialRefusal) Error() string {
	return fmt.Sprintf("the host refused the call: %s", r.Code)
}

// Unwrap makes a refusal a plain denial for callers that do not tell it apart.
func (r *CredentialRefusal) Unwrap() error { return ErrForbidden }

// scrub drops the fields of a request that only the host sets. A client can
// send any JSON in a request, so every door that takes one calls it first.
func scrub(r *contract.Request) {
	r.Credential, r.ResourceKey, r.GrantedScopes = "", "", nil
}

// credentialize adds the credential and the allowlisted resource to r when
// the add-on declares credentials. A tool of such an add-on never runs without
// them: a gateway with no source refuses it as unavailable. Anything else
// leaves r as it is.
//
// The grant it returns carries the hold of a shared binding: the caller runs
// the add-on call inside grant.Use.
func (g *Gateway) credentialize(ctx context.Context, a contract.Actor, r *contract.Request) (CredentialGrant, error) {
	src, ok := g.toolSource().(CapabilitySource)
	if !ok {
		return CredentialGrant{}, nil
	}
	caps, err := src.DeclaredCapabilities(ctx, r.AddonID, r.Generation)
	if err != nil {
		// Not knowing whether the add-on declares credentials is not a reason
		// to run its tool without the check. A stale generation is a denial,
		// not an outage of the owner.
		return CredentialGrant{}, toolSourceError(err)
	}
	if len(caps.Credentials) == 0 {
		return CredentialGrant{}, nil
	}
	if r.ResourceID != "" {
		return CredentialGrant{}, ErrForbidden
	}
	if g.Credentials == nil {
		return CredentialGrant{}, ErrUnavailable
	}
	tool, named := NamedTool(r.Arguments)
	if !named {
		return CredentialGrant{}, ErrForbidden
	}
	slot := caps.Credentials[0]
	key := ""
	omitted := false
	for _, d := range caps.ToolDefinitions {
		if d.Name != tool {
			continue
		}
		if d.UIOnly {
			// A tool of the add-on's own page is never offered to a model and
			// answers for the add-on, not for a service account of the person.
			// It gets no credential, so the page works with no connection or
			// with several.
			return CredentialGrant{}, nil
		}
		k, _, err := slot.CredentialResource(d, r.Arguments)
		switch {
		case errors.Is(err, contract.ErrResourceOmitted):
			omitted = true
		case err != nil:
			return CredentialGrant{}, fmt.Errorf("%w: %w", ErrInvalidRequest, contract.ErrRefused)
		}
		key = k
		if d.Enumerates {
			// It names no resource: the whole reach of the credential is the
			// resource, and only a binding that lists * covers it.
			key, omitted = contract.AnyResource, false
		}
	}
	grant, err := CredentialFor(ctx, g.Credentials, a, r.AddonID, key, omitted)
	if err != nil {
		return CredentialGrant{}, credentialError(err)
	}
	grant.SetOn(r)
	if grant.ResourceID != "" {
		r.ResourceID = grant.ResourceID
	}
	return grant, nil
}
