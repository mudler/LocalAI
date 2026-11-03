package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

// CredentialGrant is the secret and the allowed resource a tool call of an
// add-on gets from the binding of its caller.
type CredentialGrant struct {
	// Secret is the unsealed token of the binding.
	Secret string
	// Resource is the list entry the call named, or the zero value for a call
	// that named none.
	Resource connectors.ResourceEntry
}

// CredentialFor finds the binding that a tool call of an add-on uses and
// opens its token. The binding is one of the user's own for that add-on: the
// first that lists the resource key, or for a call without a key the first with
// a token. The error is connectors.ErrNoCredential when the user has no usable
// binding and a *connectors.NotAllowedError when no list holds the key.
func (c *Connectors) CredentialFor(ctx context.Context, user, addonID, key string) (CredentialGrant, error) {
	all, err := c.o.Store.ListBindings(ctx, principal(user))
	if err != nil {
		return CredentialGrant{}, err
	}
	var mine []connectors.Binding
	for _, b := range all {
		if b.AddonID == addonID {
			mine = append(mine, b)
		}
	}
	b, entry, err := connectors.Pick(mine, key)
	if err != nil {
		return CredentialGrant{}, err
	}
	secret, err := c.openToken(ctx, b)
	if err != nil {
		return CredentialGrant{}, err
	}
	return CredentialGrant{Secret: secret, Resource: entry}, nil
}
