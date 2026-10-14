// SPDX-License-Identifier: MIT
package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ErrNotCreated is returned by a create step that knows nothing was created.
// Create then drops the reservation at once. Any other error leaves the
// reservation pending for Reconcile, which asks the addon.
var ErrNotCreated = errors.New("addon did not create the resource")

// ReconcileTool is the declared read tool through which the owner asks an addon
// whether the backing of a resource exists. The payload is {"exists": bool}.
const ReconcileTool = "resource-exists"

// Create reserves a resource for the actor, runs create to make its backing,
// and activates it. The order is the whole contract:
//
//  1. Register records a pending resource. Pending is invisible to everyone.
//  2. create makes the backing under the reserved ID.
//  3. Commit makes the resource usable.
//
// A crash between 1 and 2 leaves a pending record with no backing, which
// Reconcile drops. A crash between 2 and 3 leaves a backing and a pending
// record, which Reconcile activates. A backing with no record cannot be
// reached by anyone, so it is never a public orphan.
func (p *Policy) Create(ctx context.Context, actor contract.Actor, addonID string, create func(context.Context, ResourceKey) error) (ResourceKey, error) {
	if create == nil {
		return ResourceKey{}, ErrForbidden
	}
	key, err := p.Register(ctx, actor, addonID)
	if err != nil {
		return ResourceKey{}, err
	}
	if err = create(ctx, key); err != nil {
		if errors.Is(err, ErrNotCreated) {
			// Settle the reservation even when the request context is gone.
			dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = p.Abandon(dctx, actor, key)
			return ResourceKey{}, err
		}
		return key, err
	}
	if err = p.Commit(ctx, actor, key); err != nil {
		return key, err
	}
	return key, nil
}

// Accessible lists the active resources of an addon the actor may at least
// read, in ID order. The addon never lists databases itself: it only knows the
// IDs the owner passes, so the owner answers "which databases may this caller
// see" from its own records.
func (p *Policy) Accessible(ctx context.Context, actor contract.Actor, addonID string) ([]ResourceKey, error) {
	if err := p.current(ctx, actor); err != nil {
		return nil, err
	}
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	var out []ResourceKey
	for id, r := range p.store.doc.Resources {
		if r.State != stateActive || r.AddonID != addonID {
			continue
		}
		if p.role(actor, *r).rank() >= ActionRead.required().rank() {
			out = append(out, ResourceKey{AddonID: addonID, ResourceID: id})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ResourceID < out[j].ResourceID })
	return out, nil
}

// reconcileActor is the identity the owner uses for its own reconciliation
// reads. It carries no authority over any resource: the request is a typed
// read of one fixed tool that returns a boolean.
var reconcileActor = contract.Actor{UserID: "owner-reconcile", CredentialID: "owner:reconcile", Mode: "local-owner"}

// BackingOptions are the owner services BackingExists uses.
type BackingOptions struct {
	// Runtime resolves the addon's current ready instance.
	Runtime gateway.CurrentRuntime
	// Tools reports the tools the installed generation declares.
	Tools gateway.ToolSource
	// Installed says whether the addon is installed at all. An addon that is
	// not installed backs nothing, so its pending resources are dropped.
	Installed func(ctx context.Context, addonID string) (bool, error)
	Limits    contract.Limits
	Now       func() time.Time
}

// BackingExists returns the existence check Reconcile needs for resources
// whose backing lives in an addon. It fails closed: an addon that is installed
// but cannot answer is an error, never "does not exist", so a pending
// resource whose database exists is not dropped because the addon was down.
func BackingExists(o BackingOptions) func(context.Context, ResourceKey) (bool, error) {
	return func(ctx context.Context, key ResourceKey) (bool, error) {
		limits := o.Limits
		if limits == (contract.Limits{}) {
			limits = contract.DefaultLimits()
		}
		now := time.Now
		if o.Now != nil {
			now = o.Now
		}
		if o.Runtime == nil || o.Tools == nil || o.Installed == nil {
			return false, ErrUnavailable
		}
		instance, err := o.Runtime.Runtime(ctx, key.AddonID)
		if err != nil {
			installed, ierr := o.Installed(ctx, key.AddonID)
			if ierr != nil {
				return false, ErrUnavailable
			}
			if !installed {
				return false, nil
			}
			return false, ErrUnavailable
		}
		if instance.Client == nil {
			return false, ErrUnavailable
		}
		declared, err := o.Tools.DeclaredTools(ctx, key.AddonID, instance.Generation)
		if err != nil {
			return false, ErrUnavailable
		}
		known := false
		for _, t := range declared {
			known = known || t == ReconcileTool
		}
		if !known {
			// The addon cannot back resources it cannot confirm.
			return false, nil
		}
		found, err := askExists(ctx, instance, key, limits, now)
		if err != nil {
			return false, ErrUnavailable
		}
		return found, nil
	}
}

// askExists runs the declared existence tool against one reserved resource.
func askExists(ctx context.Context, instance addonruntime.Instance, key ResourceKey, limits contract.Limits, now func() time.Time) (bool, error) {
	args, _ := json.Marshal(map[string]string{gateway.ToolKey: ReconcileTool})
	env := contract.Envelope{
		Request:   contract.Request{AddonID: key.AddonID, ResourceID: key.ResourceID, OperationID: "reconcile-" + key.ResourceID, Generation: instance.Generation, Action: "read", Arguments: args},
		Authority: contract.Authority{Actor: reconcileActor, Scope: "read", ExpiresAt: now().Add(limits.ReadTimeout)},
	}
	res, err := instance.Client.Read(ctx, env)
	if err != nil {
		return false, err
	}
	var out struct {
		Exists *bool `json:"exists"`
	}
	d := json.NewDecoder(bytes.NewReader(res.Payload))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || out.Exists == nil {
		return false, errors.New("invalid existence answer")
	}
	return *out.Exists, nil
}

// RuntimeCreate returns the create step for Policy.Create that drives an addon
// tool through the same preview, apply and receipt steps every mutation uses.
// The operation ID is derived from the reserved resource ID, so repeating the
// step for one reservation is one operation and a new reservation is a new one.
// A rejected receipt means nothing was created; a pending or unknown receipt
// and every transport error leave the reservation for Reconcile.
func RuntimeCreate(rt gateway.CurrentRuntime, tools gateway.ToolSource, limits contract.Limits, actor contract.Actor, arguments json.RawMessage) func(context.Context, ResourceKey) error {
	return func(ctx context.Context, key ResourceKey) error {
		if limits == (contract.Limits{}) {
			limits = contract.DefaultLimits()
		}
		var named struct {
			Tool string `json:"tool"`
		}
		if json.Unmarshal(arguments, &named) != nil || named.Tool == "" {
			return fmt.Errorf("%w: create arguments must name a tool", ErrNotCreated)
		}
		instance, err := rt.Runtime(ctx, key.AddonID)
		if err != nil || instance.Client == nil {
			return ErrUnavailable
		}
		declared, err := tools.DeclaredTools(ctx, key.AddonID, instance.Generation)
		if err != nil {
			return ErrUnavailable
		}
		ok := false
		for _, t := range declared {
			ok = ok || t == named.Tool
		}
		if !ok {
			return fmt.Errorf("%w: tool is not declared", ErrNotCreated)
		}
		op := "create-" + key.ResourceID
		env := func(scope string) contract.Envelope {
			return contract.Envelope{
				Request:   contract.Request{AddonID: key.AddonID, ResourceID: key.ResourceID, OperationID: op, Generation: instance.Generation, Action: string(ActionSchema), Arguments: arguments},
				Authority: contract.Authority{Actor: actor, Scope: scope, ExpiresAt: time.Now().Add(limits.PreviewTimeout + limits.ApplyTimeout)},
			}
		}
		preview, err := instance.Client.Preview(ctx, env("preview"))
		if err != nil {
			// A preview never creates anything. When the addon confirms that
			// the backing is absent the reservation is dropped now; when it
			// cannot answer, the reservation stays for Reconcile.
			if ctx.Err() != nil {
				return err
			}
			if found, e := askExists(ctx, instance, key, limits, time.Now); e == nil && !found {
				return fmt.Errorf("%w: %w", ErrNotCreated, err)
			}
			return err
		}
		receipt, err := instance.Client.Apply(ctx, env("apply"), preview.Revision)
		if err != nil {
			return err
		}
		switch receipt.State {
		case contract.ReceiptCommitted:
			return nil
		case contract.ReceiptRejected:
			return ErrNotCreated
		}
		return ErrUnavailable
	}
}

// Pending lists the IDs of reservations that were neither committed nor
// abandoned. It is for status and tests; it grants nothing.
func (p *Policy) Pending() ([]string, error) { return p.store.pending(), nil }
