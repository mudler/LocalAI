// SPDX-License-Identifier: MIT
package application

import (
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/policy"
)

// RegisterShareKinds adds services whose resources are shared through the
// add-on policy and the /api/shares routes. A provider given before the add-on
// owner starts registers its kinds when the policy opens, before anything is
// reconciled or served; one given later registers at once. Only the owner
// holds the policy: on any other process the providers are kept and never
// asked, and the routes forward to the owner.
func (a *Application) RegisterShareKinds(providers ...policy.KindProvider) error {
	a.shareKindsMu.Lock()
	defer a.shareKindsMu.Unlock()
	if a.shareKindsPolicy != nil {
		if err := policy.RegisterKinds(a.shareKindsPolicy, providers...); err != nil {
			return err
		}
	}
	a.shareKinds = append(a.shareKinds, providers...)
	return nil
}

// registerShareKinds registers the kinds of every provider given so far with
// the policy the owner just opened. A provider that fails stops the start: a
// kind left unregistered would deny every resource of it as unknown.
func (a *Application) registerShareKinds(p *policy.Policy) error {
	a.shareKindsMu.Lock()
	defer a.shareKindsMu.Unlock()
	if err := policy.RegisterKinds(p, a.shareKinds...); err != nil {
		return err
	}
	a.shareKindsPolicy = p
	return nil
}

// addonSharingForward is the owner's sharing policy for the services of this
// process when it holds none: a frontend with the add-on carrier in
// named-user mode. The owner decides every call, and an owner that cannot be
// reached gives a typed unavailable answer and never a grant. It is nil on
// the owner, which uses its own policy, and where there is no carrier.
func (a *Application) addonSharingForward() policy.Sharing {
	if a.addonPolicy != nil || a.AddonOwnershipMode() != auth.AddonModeNamedUser {
		return nil
	}
	g := a.addonGateway
	if g == nil || g.Forward == nil {
		return nil
	}
	return policy.Forwarded{Gateway: g}
}
