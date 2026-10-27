// SPDX-License-Identifier: MIT
package main

import (
	"context"

	"github.com/mudler/LocalAI/addons/sandbox/internal/codetools"
	"github.com/mudler/LocalAI/addons/sandbox/internal/manager"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// backendFor returns the function that picks the backend for each call.
func backendFor(dataDir string) func() (sandbox.Backend, error) {
	return func() (sandbox.Backend, error) { return nil, codetools.ErrNoSandbox }
}

// unavailable is the runner of an add-on whose manager did not start.
type unavailable struct{}

func (unavailable) Run(context.Context, manager.Request) (string, error) {
	return codetools.NoSandboxText, nil
}
