// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mudler/LocalAI/core/services/addons/modelsprobe"
	"sync"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var ErrUnavailable = errors.New("addon owner unavailable")

// ErrNotSent means the call never reached the add-on: it is not running (it is
// starting, stopped or restarting) or the request was not written. The tool
// cannot have run, so unlike an outage after the send this is not an unknown
// outcome and the caller may try again.
var ErrNotSent = fmt.Errorf("the add-on is not running now and nothing was sent: %w", ErrUnavailable)

// Gateway deliberately admits lifecycle commands only until resource admission
// and proposal confirmation are installed. Installation admin is not data access.
type Gateway struct {
	// Transfers holds the handles of connector file transfers on the process
	// that runs the add-ons. Without it a call that carries a handle is
	// unavailable.
	Transfers          *hostops.Transfers
	Connectors         ConnectorSource
	ConnectorAuthority ConnectorAuthority
	// HostSettings is owner-only durable policy; frontends forward, never fall back.
	HostSettings HostSettingsService
	// HostSettingsAdmin checks current host authority, never add-on supplied flags.
	HostSettingsAdmin func(context.Context, contract.Actor) error
	// ResolveExecutionBinding loads actor-owned context on the owner. Nil denies.
	ResolveExecutionBinding func(context.Context, contract.Actor, ExecutionBindingClaim) (ExecutionStatusBinding, error)
	// AuthorizeExecutionStatus must revalidate current provider current selection,
	// execution feature permission, access and builtin replacement authority on the owner.
	// Nil denies all observations. Status is never selection authorization.
	AuthorizeExecutionStatus func(context.Context, contract.Actor, ExecutionStatusBinding) error
	// Credentials supplies the secret and the allowed resource of a call of
	// an add-on that declares credentials. Only the owner holds it; a gateway
	// with none refuses such calls as unavailable.
	Credentials CredentialSource
	Limits      RelayLimits
	Manager     *addons.Manager
	Runtime     CurrentRuntime
	Forward     *Carrier
	Admission   Admission
	Resolver    ResourceResolver
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
	Resources ResourceManager
	// Shares is the owner's sharing of the resource kinds that are not
	// add-ons: the /api/shares routes. Frontends leave it nil and forward
	// through Forward.
	Sharing ShareManager
	// ShareAuthority is the owner's lookup, creation and admission of the
	// resources of those kinds for the services that run their work on any
	// frontend (see RunShared). Frontends leave it nil and forward.
	ShareAuthority ShareAuthority
	// Libraries is the owner's library administration. Frontends leave it nil
	// and forward through Forward.
	Libraries LibraryAdmin
	// HostGrantsOwner serves the host access routes (grants, deny rules and
	// the blanket approval). Only the owner holds it: a frontend leaves it
	// nil and forwards, and never answers from its own flags or files.
	HostGrantsOwner *HostGrantsOwner
	// OAuthOwner serves the OAuth logins of connections and the OAuth
	// clients of add-ons. Only the owner holds it, since only the owner reads
	// the descriptors; a frontend leaves it nil and forwards.
	OAuthOwner OAuthService
	// ModelsProbe returns the probe the owner wrote into its models path, or
	// false when it has none. Frontends leave it nil.
	ModelsProbe func() (modelsprobe.Probe, bool)
	// OS and Arch are the platform the owner resolves libraries for. Empty
	// means the platform of this process.
	OS, Arch  string
	streamsMu sync.Mutex
	streams   int
	// Revalidate is the host's account check of add-on calls. It includes
	// the add-ons feature.
	Revalidate func(context.Context, contract.Actor) error
	// RevalidateAccount is the host's account check of the sharing calls of
	// the kinds that are not add-ons: the account and the credential, never
	// the add-ons feature. The owner's policy then checks the feature the
	// kind names. Without it the sharing calls are denied.
	RevalidateAccount func(context.Context, contract.Actor) error
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

// revalidateAccount runs the host's account check of a sharing call.
func (g *Gateway) revalidateAccount(ctx context.Context, a contract.Actor) error {
	if g.RevalidateAccount == nil {
		return ErrForbidden
	}
	return ClassifyCheck(g.RevalidateAccount(ctx, a))
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
	scrub(&request)
	// The act action has its own door, Act. A frontend that forwarded it from
	// here would let the owner run it for a consumer that was never offered it.
	if request.Action == "act" {
		return contract.Result{}, ErrForbidden
	}
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
	case ActionHostSettingsRead, ActionHostSettingsUpdate:
		return g.hostSettingsAction(ctx, actor, request)
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
		var status addons.OperationStatus
		var err error
		if cmd.Action == "upgrade" {
			status, err = g.Manager.SubmitUpgrade(ctx, actor, cmd)
		} else {
			status, err = g.Manager.Submit(ctx, actor, cmd)
		}
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
