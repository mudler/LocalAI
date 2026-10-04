// SPDX-License-Identifier: MIT
package application

import "github.com/mudler/LocalAI/core/backend"

// DecisionRunner lazily resolves the named model on every native decision call.
// Construction does not load weights or guess an unavailable model's usecase.
func (a *Application) DecisionRunner(modelName string) backend.DecisionRunner {
	return backend.NewDecisionRunner(modelName, a.adapterConfig, a.modelLoader, a.applicationConfig)
}
