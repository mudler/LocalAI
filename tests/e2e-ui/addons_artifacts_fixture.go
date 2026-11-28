// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type fixtureArtifactStore interface {
	Status(context.Context, contract.Actor, string) (gateway.Artifact, error)
	Cancel(context.Context, contract.Actor, string) (gateway.Artifact, error)
}

type fixtureArtifactControl struct {
	store  fixtureArtifactStore
	actors map[string]contract.Actor
}

func (f *addonsFixture) configureArtifactControl(app *application.Application, actors map[string]contract.Actor) error {
	provider, ok := app.AddonGateway().Admission.(*policy.GatewayProvider)
	if !ok {
		return fmt.Errorf("fixture artifact admission unavailable")
	}
	store, ok := provider.Outputs.(fixtureArtifactStore)
	if !ok {
		return fmt.Errorf("fixture artifact store unavailable")
	}
	f.mu.Lock()
	f.artifactControl = &fixtureArtifactControl{store: store, actors: actors}
	f.mu.Unlock()
	return nil
}

// settleArtifact belongs only to the loopback fixture control server. A file's
// complete status precedes worker cleanup, so it alone cannot serialize quota
// setup. Cancel on an already-complete file preserves its bytes and waits for
// the worker's done signal. Never apply it to an unfinished or failed file.
func (f *addonsFixture) settleArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		User string `json:"user"`
		ID   string `json:"id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "invalid fixture request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	control := f.artifactControl
	f.mu.Unlock()
	if control == nil {
		http.Error(w, "fixture artifact store unavailable", http.StatusServiceUnavailable)
		return
	}
	actor, ok := control.actors[body.User]
	if !ok {
		http.Error(w, "unknown fixture user", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	before, err := control.store.Status(ctx, actor, body.ID)
	if err != nil || before.State != gateway.ArtifactComplete {
		http.Error(w, "fixture file is not complete", http.StatusConflict)
		return
	}
	after, err := control.store.Cancel(ctx, actor, body.ID)
	if err != nil || after.State != gateway.ArtifactComplete {
		http.Error(w, "fixture file did not settle complete", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(after)
}
