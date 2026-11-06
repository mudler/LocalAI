// SPDX-License-Identifier: MIT
package application

import (
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
