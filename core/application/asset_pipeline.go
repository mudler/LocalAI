// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	engine "github.com/mudler/LocalAI/core/services/assetpipeline"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	pipeline "github.com/mudler/LocalAI/pkg/assetpipeline"
	"github.com/mudler/LocalAI/pkg/signals"
)

func (a *Application) RegisterAddonPipelines() error {
	if a.addonHostOps == nil || a.addonDataRoot == "" {
		return nil
	}
	type opened struct {
		runner *pipeline.Runner
		store  *pipeline.Store
	}
	var mu sync.Mutex
	engines := map[string]opened{}
	signals.RegisterGracefulTerminationHandler(func() {
		mu.Lock()
		defer mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, e := range engines {
			_ = e.runner.Close(ctx)
			_ = e.store.Close()
		}
	})
	return hostops.RegisterPipeline(a.addonHostOps.Broker, func(addon string) (*pipeline.Runner, *pipeline.Store, error) {
		mu.Lock()
		defer mu.Unlock()
		if e, ok := engines[addon]; ok {
			return e.runner, e.store, nil
		}
		// The broker validates add-on identifiers before this resolver runs.
		store, err := pipeline.Open(filepath.Join(a.addonDataRoot, addon, "pipeline"))
		if err != nil {
			return nil, nil, err
		}
		executor := &engine.Executor{Store: store, Models: a.ModelConfigLoader(), Loader: a.ModelLoader(), Config: a.ApplicationConfig(), Templates: a.TemplatesEvaluator(), Authorize: a.authorizePipeline}
		executor.AuthorizeResource = func(ctx context.Context, principal, resource string) error {
			var actor contract.Actor
			if json.Unmarshal([]byte(principal), &actor) != nil || a.addonPolicy == nil {
				return errors.New("pipeline resource authorization unavailable")
			}
			return a.addonPolicy.Authorize(ctx, actor, policy.ResourceKey{AddonID: addon, ResourceID: resource}, policy.ActionWrite)
		}
		runner, err := pipeline.NewRunner(a.ApplicationConfig().Context, store, executor)
		if err != nil {
			_ = store.Close()
			return nil, nil, err
		}
		engines[addon] = opened{runner, store}
		return runner, store, nil
	})
}

func (a *Application) authorizePipeline(ctx context.Context, principal, model string, kind pipeline.Kind) (bool, error) {
	var actor contract.Actor
	if json.Unmarshal([]byte(principal), &actor) != nil {
		return false, errors.New("invalid pipeline principal")
	}
	mode, id := actor.Mode, actor.UserID
	if a.authDB == nil {
		if mode == "local-owner" {
			return true, nil
		}
		return false, errors.New("pipeline account unavailable")
	}
	var user auth.User
	if err := a.authDB.WithContext(ctx).Where("id = ? AND status = ?", id, auth.StatusActive).First(&user).Error; err != nil {
		return false, errors.New("pipeline account unavailable")
	}
	feature := map[pipeline.Kind]string{pipeline.Source: auth.Feature3D, pipeline.Review: auth.FeatureChat, pipeline.Image: auth.FeatureImages, pipeline.Extract: auth.FeatureDetection, pipeline.Mesh: auth.Feature3D, pipeline.Remesh: auth.Feature3D, pipeline.Motion: auth.Feature3D, pipeline.Rig: auth.Feature3D, pipeline.Skin: auth.Feature3D, pipeline.Retarget: auth.Feature3D}[kind]
	if feature == "" || !auth.HasFeatureAccess(a.authDB, &user, auth.Feature3D) || !auth.HasFeatureAccess(a.authDB, &user, feature) {
		return false, errors.New("pipeline stage permission denied")
	}
	if model != "" && !auth.IsModelAllowed(a.authDB, &user, model) {
		return false, errors.New("pipeline model permission denied")
	}
	return user.Role == auth.RoleAdmin, nil
}
