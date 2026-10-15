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
	Limits    RelayLimits
	Manager   *addons.Manager
	Runtime   CurrentRuntime
	Forward   *Carrier
	Admission Admission
	Resolver  ResourceResolver
	// Tools reports the tools each installed add-on declares. When nil the
	// Manager is used; with neither, a request that names a tool is denied.
	Tools ToolSource
	// Proposals is the owner's proposal broker. Frontends leave it nil and
	// forward through Forward.
	Proposals ProposalBroker
	// Artifacts is the owner's private artifact store. Without it the gateway
	// denies every file result and every file download.
	Artifacts ArtifactProvider
	// Resources is the owner's UI-only resource management: listing,
	// creation, grants, rename, deletion and conversation selections.
	// Frontends leave it nil and forward through Forward.
	Resources  ResourceManager
	streamsMu  sync.Mutex
	streams    int
	Revalidate func(context.Context, contract.Actor) error
}

// ClassifyCheck maps an error from a host-supplied account or authority check
// to the shared answer. Only a definite denial, an error that wraps
// ErrForbidden, is a 403. Anything else, including an unclassified error or a
// cancelled context, means the check could not tell and is ErrUnavailable
// (503): an unknown failure is never reported as a refusal of a valid user and
// never as success. A nil error stays nil.
func ClassifyCheck(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrUnavailable):
		return ErrUnavailable
	case errors.Is(err, ErrForbidden):
		return ErrForbidden
	}
	return ErrUnavailable
}

// revalidate runs the host's account and credential check. An absent check is
// a denial; see ClassifyCheck for the rest.
func (g *Gateway) revalidate(ctx context.Context, a contract.Actor) error {
	if g.Revalidate == nil {
		return ErrForbidden
	}
	return ClassifyCheck(g.Revalidate(ctx, a))
}

type RelayRequest struct {
	Request contract.Request `json:"request"`
	Kind    string           `json:"kind"`
	Handle  string           `json:"handle,omitempty"`
	// Digest names the installed package an asset belongs to: the lower-case
	// hex of its sha256. Only the kind "asset" uses it.
	Digest string `json:"digest,omitempty"`
}

func (g *Gateway) Dispatch(ctx context.Context, actor contract.Actor, request contract.Request) (contract.Result, error) {
	if e := g.revalidate(ctx, actor); e != nil {
		return contract.Result{}, e
	}
	if g.Forward != nil {
		return g.Forward.Dispatch(ctx, actor, request)
	}
	result, err := g.dispatchLocal(ctx, actor, request)
	return result, mutationOutcome(request, Refused(err))
}

// dispatchLocal serves a request on the owner itself, with no forwarding.
func (g *Gateway) dispatchLocal(ctx context.Context, actor contract.Actor, request contract.Request) (contract.Result, error) {
	switch request.Action {
	case "catalog-search", "catalog-details", "catalog-sources", "catalog-source-save", "catalog-source-refresh", "addons-installed":
		return g.management(ctx, actor, request)
	case ActionUpdateStatus, ActionUpdateCheck, ActionUpdateUpgrade:
		return g.updateAction(ctx, actor, request)
	case ActionRecoveryStatus, ActionRecoveryBackup, ActionRecoveryVerify, ActionRecoveryRestore, ActionRecoveryDeleteBackup, ActionRecoveryPrune, ActionRecoveryPurge:
		return g.recoveryAction(ctx, actor, request)
	case "install", "upgrade", "enable", "disable", "uninstall":
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
		cmd := addons.LifecycleCommand{OperationID: request.OperationID, AddonID: request.AddonID, Action: request.Action, SourceID: args.SourceID, Version: args.Version}
		if request.Action == "enable" || request.Action == "disable" {
			// The generation the caller saw is a precondition of enable and
			// disable; a stale one is denied before anything is recorded.
			cmd.Generation = request.Generation
		}
		status, err := g.Manager.Submit(ctx, actor, cmd)
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
