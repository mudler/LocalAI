// SPDX-License-Identifier: MIT

// Package idle is the executable of an add-on that has a UI and no tools. The
// contract requires an executable for every add-on, so such an add-on ships
// this one: it speaks the add-on protocol, declares nothing and refuses every
// operation. All of its work happens in the browser, through the SDK.
package idle

import (
	"context"
	"errors"
	"io"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ErrNoTools is what every operation answers.
var ErrNoTools = errors.New("this add-on declares no tools")

// Runtime implements contract.Runtime with no tools.
type Runtime struct{}

func (Runtime) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, ErrNoTools
}

func (Runtime) Preview(context.Context, contract.Envelope) (contract.Preview, error) {
	return contract.Preview{}, ErrNoTools
}

func (Runtime) Apply(context.Context, contract.Envelope, contract.Revision) (contract.Receipt, error) {
	return contract.Receipt{}, ErrNoTools
}

func (Runtime) Receipt(context.Context, string) (contract.Receipt, error) {
	return contract.Receipt{}, ErrNoTools
}

func (Runtime) OpenOutput(context.Context, contract.Envelope, string) (io.ReadCloser, error) {
	return nil, ErrNoTools
}

// Run is the whole behaviour of a UI add-on executable. It is protocol.Run
// with the runtime that refuses every operation. It returns the process exit
// code.
func Run(args []string) int {
	return protocol.Run(args, func(protocol.Startup) contract.Runtime { return Runtime{} })
}
