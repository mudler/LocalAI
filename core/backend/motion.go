// SPDX-License-Identifier: MIT
package backend

import (
	"context"
	"fmt"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
)

func ModelMotionStream(ctx context.Context, loader *model.ModelLoader, app *config.ApplicationConfig, cfg config.ModelConfig) (grpc.MotionStreamClient, error) {
	m, err := loader.Load(ModelOptions(cfg, app)...)
	if err != nil {
		recordModelLoadFailure(app, cfg.Name, cfg.Backend, err, nil)
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("motion model unavailable")
	}
	// A live session is intentionally long-lived. Its context owns the global slot.
	release, err := AcquireGlobalBackendSlot()
	if err != nil {
		return nil, err
	}
	s, err := m.MotionStream(ctx)
	if err != nil {
		release()
		return nil, err
	}
	go func() { <-ctx.Done(); release() }()
	return s, nil
}
