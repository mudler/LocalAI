// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"errors"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

// The texts a request must carry to turn on the two choices that widen what a
// binding reaches. The owner types them, or the client sends them, in the same
// request: a stored flag alone never turns the choice on for a new reach.
const (
	// AnyResourceConfirmation confirms the entry * in the resource list.
	AnyResourceConfirmation = "allow any resource"
	// WiderTokenConfirmation confirms the option that lets a guarded token
	// (such as a classic access token) be used.
	WiderTokenConfirmation = "accept wider token"
)

// ErrTokenGuarded means the token of a binding starts with a prefix its kind
// holds back, and the binding did not choose to allow it.
var ErrTokenGuarded = errors.New("studio: the token needs an option the binding did not choose")

// checkWildcards holds the resource list of a binding to the wildcard rules
// of its kind, and asks for the confirmation of a new *. old is nil for a new
// binding. The confirmation is asked whatever the kind says, because a kind
// whose add-on is stopped cannot be asked.
func (c *Connectors) checkWildcards(ctx context.Context, old *connectors.Binding, b connectors.Binding, in ConnectorInput) error {
	kind := c.kindOf(ctx, b)
	if err := connectors.ValidateWildcards(b.Resources, kind != nil && kind.WildcardResources); err != nil {
		return err
	}
	if connectors.HasAnyResource(b.Resources) && (old == nil || !connectors.HasAnyResource(old.Resources)) && in.ConfirmAnyResource != AnyResourceConfirmation {
		return connectorBad(`the entry * lets the agent use every resource the token reaches; to turn it on, send confirm_any_resource set to %q`, AnyResourceConfirmation)
	}
	return nil
}

// checkTokenGuards applies the token guards of the kind: choosing the option
// that allows a guarded token needs the confirmation, and a new token with a
// guarded prefix needs the option. The message never holds the token.
func (c *Connectors) checkTokenGuards(ctx context.Context, old *connectors.Binding, b connectors.Binding, in ConnectorInput) error {
	kind := c.kindOf(ctx, b)
	if kind == nil {
		return nil
	}
	token := strings.TrimSpace(in.Token)
	for _, g := range kind.TokenGuards {
		allowed := b.Options[g.Option] == g.Allow
		if allowed && (old == nil || old.Options[g.Option] != g.Allow) && in.ConfirmWiderToken != WiderTokenConfirmation {
			return connectorBad(`allowing a token that starts with %q lets it reach more than the resource list; to turn it on, send confirm_wider_token set to %q`, g.Prefix, WiderTokenConfirmation)
		}
		if !allowed && token != "" && strings.HasPrefix(token, g.Prefix) {
			return connectorBad("a token that starts with %q is refused unless the connection chooses %q for the option %q", g.Prefix, g.Allow, g.Option)
		}
	}
	return nil
}

// guardedToken reports a token of the binding that its kind holds back and
// the binding did not allow. Called when a token is opened for a call.
func (c *Connectors) guardedToken(ctx context.Context, b connectors.Binding, secret string) bool {
	kind := c.kindOf(ctx, b)
	if kind == nil {
		return false
	}
	for _, g := range kind.TokenGuards {
		if strings.HasPrefix(secret, g.Prefix) && b.Options[g.Option] != g.Allow {
			return true
		}
	}
	return false
}
