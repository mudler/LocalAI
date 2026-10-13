// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var ErrUnavailable = errors.New("addon owner unavailable")

// Gateway deliberately admits lifecycle commands only until resource admission
// and proposal confirmation are installed. Installation admin is not data access.
type Gateway struct {
	Limits     RelayLimits
	Manager    *addons.Manager
	Runtime    CurrentRuntime
	Forward    *Carrier
	Admission  Admission
	Resolver   ResourceResolver
	streamsMu  sync.Mutex
	streams    int
	Revalidate func(context.Context, contract.Actor) error
}
type RelayRequest struct {
	Request contract.Request `json:"request"`
	Kind    string           `json:"kind"`
	Handle  string           `json:"handle,omitempty"`
}

func (g *Gateway) Dispatch(ctx context.Context, actor contract.Actor, request contract.Request) (contract.Result, error) {
	if g.Revalidate == nil || g.Revalidate(ctx, actor) != nil {
		return contract.Result{}, ErrForbidden
	}
	if g.Forward != nil {
		return g.Forward.Dispatch(ctx, actor, request)
	}
	result, err := g.dispatchLocal(ctx, actor, request)
	return result, mutationOutcome(request, err)
}

// dispatchLocal serves a request on the owner itself, with no forwarding.
func (g *Gateway) dispatchLocal(ctx context.Context, actor contract.Actor, request contract.Request) (contract.Result, error) {
	switch request.Action {
	case "install", "enable", "disable", "uninstall":
		var args struct {
			SourceID string `json:"sourceId"`
			Version  string `json:"version"`
		}
		if err := decode(bytes.NewReader(request.Arguments), &args); err != nil {
			return contract.Result{}, ErrForbidden
		}
		if g.Manager == nil {
			return contract.Result{}, ErrUnavailable
		}
		status, err := g.Manager.Submit(ctx, actor, addons.LifecycleCommand{OperationID: request.OperationID, AddonID: request.AddonID, Action: request.Action, SourceID: args.SourceID, Version: args.Version})
		if err != nil {
			return contract.Result{}, err
		}
		payload, err := json.Marshal(status)
		return contract.Result{Payload: payload}, err
	case "read":
		return g.read(ctx, actor, request)
	case "operation-status":
		if g.Manager == nil {
			return contract.Result{}, ErrUnavailable
		}
		status, err := g.Manager.OperationStatus(ctx, actor, request.OperationID)
		if err != nil {
			return contract.Result{}, err
		}
		payload, err := json.Marshal(status)
		return contract.Result{Payload: payload}, err
	default:
		return contract.Result{}, ErrForbidden
	}
}
