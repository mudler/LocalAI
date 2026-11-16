// SPDX-License-Identifier: MIT
package model

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

type leaseContextKey struct{}

type modelLease struct {
	owner   string
	expires time.Time
	models  map[string]bool
}

// Lease is an advisory lifetime, not a pin or permission to force eviction.
type Lease struct {
	ID      string    `json:"id"`
	Expires time.Time `json:"expires"`
}

type LeaseRelease struct {
	Model  string `json:"model"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

var ErrLeaseNotFound = errors.New("model lease not found")

// BeginLease binds an unguessable handle to the caller. Expiration only forgets
// the advisory claim: it never tears down a model whose client disappeared.
func (ml *ModelLoader) BeginLease(ctx context.Context, owner string, ttl time.Duration) (context.Context, Lease) {
	if ttl <= 0 || ttl > time.Hour {
		ttl = time.Hour
	}
	l := Lease{ID: uuid.NewString(), Expires: time.Now().UTC().Add(ttl)}
	ml.leaseMu.Lock()
	defer ml.leaseMu.Unlock()
	if ml.leases == nil {
		ml.leases = map[string]*modelLease{}
	}
	for id, old := range ml.leases {
		if time.Now().After(old.expires) {
			delete(ml.leases, id)
		}
	}
	ml.leases[l.ID] = &modelLease{owner: owner, expires: l.Expires, models: map[string]bool{}}
	return context.WithValue(ctx, leaseContextKey{}, l.ID), l
}

func leaseFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(leaseContextKey{}).(string)
	return id
}

func (ml *ModelLoader) touchLease(ctx context.Context, model string) {
	id := leaseFromContext(ctx)
	if id == "" {
		return
	}
	ml.leaseMu.Lock()
	defer ml.leaseMu.Unlock()
	if l := ml.leases[id]; l != nil && time.Now().Before(l.expires) {
		l.models[model] = true
	}
}

// ReleaseLease tries to unload only the exact local instance this lease loaded.
// Distributed leases are advisory until worker-wide leases are supported; a
// frontend must never infer exclusive ownership from its local view.
func (ml *ModelLoader) ReleaseLease(ctx context.Context, owner, id string, mayUnload bool) ([]LeaseRelease, error) {
	ml.leaseMu.Lock()
	l := ml.leases[id]
	if l == nil || l.owner != owner {
		ml.leaseMu.Unlock()
		return nil, ErrLeaseNotFound
	}
	delete(ml.leases, id)
	models := make([]string, 0, len(l.models))
	for name := range l.models {
		models = append(models, name)
	}
	ml.leaseMu.Unlock()
	results := make([]LeaseRelease, 0, len(models))
	for _, name := range models {
		r := LeaseRelease{Model: name, State: "retained"}
		if !mayUnload {
			r.Reason = "unload not authorized"
		} else if time.Now().After(l.expires) {
			r.Reason = "lease expired"
		} else {
			r = ml.releaseLeasedModel(ctx, name, id)
		}
		results = append(results, r)
	}
	return results, nil
}

func (ml *ModelLoader) releaseLeasedModel(ctx context.Context, name, id string) LeaseRelease {
	r := LeaseRelease{Model: name, State: "retained"}
	release, err := ml.operations.acquireContext(ctx, name, true)
	if err != nil {
		r.Reason = "model lifecycle busy"
		return r
	}
	defer release()
	// Keep lease admission serialized with this best-effort teardown. A new
	// load waits at the model operation lock and can load again afterwards.
	ml.leaseMu.Lock()
	defer ml.leaseMu.Unlock()
	for _, other := range ml.leases {
		if other.models[name] && time.Now().Before(other.expires) {
			r.Reason = "another lease is active"
			return r
		}
	}
	ml.mu.Lock()
	m, exists := ml.store.Get(name)
	wd, remote := ml.wd, ml.modelRouter != nil || ml.remoteUnloader != nil
	ml.mu.Unlock()
	if remote {
		r.Reason = "distributed unload ownership unavailable"
		return r
	}
	if !exists {
		r.State = "absent"
		return r
	}
	if m.leaseCreator != id {
		r.Reason = "instance was not loaded by this lease"
		return r
	}
	if wd != nil && wd.IsModelPinned(name) {
		r.Reason = "model is pinned"
		return r
	}
	if m.GRPC(false, wd).IsBusy() {
		r.Reason = "model is busy"
		return r
	}
	if err := ml.deleteProcess(ctx, name, false); err != nil {
		r.Reason = "unload failed: " + err.Error()
		return r
	}
	r.State = "unloaded"
	return r
}
