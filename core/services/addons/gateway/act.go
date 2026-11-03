// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"errors"
	"time"

	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Act runs an action tool of an add-on as the actor. It is not part of the
// typed dispatch: the direct API, agents and scripts reach the add-on through
// Dispatch, which refuses the act action, and only a consumer that holds a
// ToolAdapter built for actions calls this.
//
// The call is never repeated. A frontend forwards it to the owner and a lost
// answer is an unknown outcome, because the tool may have run.
func (g *Gateway) Act(ctx context.Context, actor contract.Actor, request contract.Request) (contract.Result, error) {
	if request.Action != "act" {
		return contract.Result{}, ErrForbidden
	}
	if e := g.revalidate(ctx, actor); e != nil {
		return contract.Result{}, e
	}
	if g.Forward != nil {
		return g.Forward.Act(ctx, actor, request)
	}
	result, err := g.actLocal(ctx, actor, request)
	return result, mutationOutcome(request, Refused(err))
}

// actLocal runs the call on the owner itself.
func (g *Gateway) actLocal(ctx context.Context, a contract.Actor, r contract.Request) (out contract.Result, err error) {
	aa, ok := g.Admission.(AddonAdmission)
	if !ok {
		return out, ErrForbidden
	}
	tool, named := NamedTool(r.Arguments)
	if !named {
		return out, ErrForbidden
	}
	// A tool the add-on did not declare never reaches it.
	if e := AuthorizeTool(ctx, g.toolSource(), r.AddonID, r.Generation, r.Arguments); e != nil {
		return out, e
	}
	// The add-on must have said it acts, and the tool must be an action tool:
	// a read tool is never run unconfirmed through this door under another name.
	src, ok := g.toolSource().(CapabilitySource)
	if !ok {
		return out, ErrForbidden
	}
	caps, e := src.DeclaredCapabilities(ctx, r.AddonID, r.Generation)
	if e != nil || !caps.Actions {
		return out, ErrForbidden
	}
	action := false
	for _, d := range caps.ToolDefinitions {
		if d.Name == tool && d.Kind == contract.ToolKindAction {
			action = true
		}
	}
	if !action {
		return out, ErrForbidden
	}
	err = aa.AdmitAddon(ctx, a, r.AddonID, func(ctx context.Context) error {
		if e := g.revalidate(ctx, a); e != nil {
			return e
		}
		instance, e := g.current(ctx, r.AddonID)
		if e != nil {
			if errors.Is(e, addonruntime.ErrRuntime) || errors.Is(e, ErrUnavailable) {
				return ErrNotSent // nothing is running that could have taken the call
			}
			return e
		}
		if instance.Generation != r.Generation {
			return ErrForbidden
		}
		runtime, ok := instance.Client.(contract.ActRuntime)
		if !ok {
			return ErrUnavailable
		}
		// The authority outlives the longest call and no longer.
		out, e = runtime.Act(ctx, contract.Envelope{Request: r, Authority: contract.Authority{
			Actor: a, Scope: "act", ExpiresAt: time.Now().Add(contract.ActTimeout + time.Minute)}})
		if errors.Is(e, addonruntime.ErrNotSent) {
			return ErrNotSent
		}
		return e
	})
	if err == nil && len(out.Outputs) > 0 {
		// Files travel through the workspace, not through private artifacts.
		return contract.Result{}, ErrForbidden
	}
	return out, err
}
