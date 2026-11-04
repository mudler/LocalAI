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
	CodeNoCredential = "no_credential"
	// CodeWriteNotAllowed means the binding that holds the credential has its
	// write mode off, so a write tool does not run.
	CodeWriteNotAllowed = "write_not_allowed"
	// CodeSelectConnection means the person has not selected the connection
	// this call uses: the chat and its agent name none, and the person has
	// several or none. The host picks no connection for them.
	CodeSelectConnection = "select_connection"
)

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
	// WriteMode is the write mode of the binding the credential comes from:
	// WriteOff, WritePropose or WriteAuto. Empty means WriteOff. It decides
	// whether a write tool runs and whether a person must confirm it.
	WriteMode string
	// BindingID and BindingName say which connection the credential comes
	// from. The proposal, the result of the call and the audit line carry them.
	BindingID, BindingName string
}

// CredentialSource finds the credential and the allowed resource for a call.
// It is the owner's: the sealed secrets are not on a frontend. An error it
// returns that is not a *CredentialRefusal is an outage.
type CredentialSource interface {
	// Credential returns the grant for actor and add-on. A non-empty key must
	// be on the list of a binding of the person (compared without regard to
	// case). The host calls it before the add-on is called.
	Credential(ctx context.Context, actor contract.Actor, addonID, key string) (CredentialGrant, error)
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
	r.Credential, r.ResourceKey = "", ""
}

// credentialize adds the credential and the allowlisted resource to r when
// the add-on declares credentials. A tool of such an add-on never runs without
// them: a gateway with no source refuses it as unavailable. Anything else
// leaves r as it is.
func (g *Gateway) credentialize(ctx context.Context, a contract.Actor, r *contract.Request) error {
	src, ok := g.toolSource().(CapabilitySource)
	if !ok {
		return nil
	}
	caps, err := src.DeclaredCapabilities(ctx, r.AddonID, r.Generation)
	if err != nil {
		// Not knowing whether the add-on declares credentials is not a reason
		// to run its tool without the check. A stale generation is a denial,
		// not an outage of the owner.
		return toolSourceError(err)
	}
	if len(caps.Credentials) == 0 {
		return nil
	}
	if r.ResourceID != "" {
		return ErrForbidden
	}
	if g.Credentials == nil {
		return ErrUnavailable
	}
	tool, named := NamedTool(r.Arguments)
	if !named {
		return ErrForbidden
	}
	slot := caps.Credentials[0]
	key := ""
	for _, d := range caps.ToolDefinitions {
		if d.Name != tool {
			continue
		}
		k, _, err := slot.CredentialResource(d, r.Arguments)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRequest, contract.ErrRefused)
		}
		key = k
	}
	grant, err := g.Credentials.Credential(ctx, a, r.AddonID, key)
	if err != nil {
		var refusal *CredentialRefusal
		if errors.As(err, &refusal) {
			return refusal
		}
		return ClassifyCheck(err)
	}
	r.Credential, r.ResourceKey = grant.Credential, grant.ResourceKey
	if grant.ResourceID != "" {
		r.ResourceID = grant.ResourceID
	}
	return nil
}
