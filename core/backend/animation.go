// SPDX-License-Identifier: MIT
package backend

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
)

func Model3DAnimation(ctx context.Context, request *proto.Animate3DRequest, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (err error) {
	inferenceModel, err := loader.Load(ModelOptions(modelConfig, appConfig)...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return err
	}
	release, err := AcquireGlobalBackendSlot()
	if err != nil {
		return err
	}
	defer release()
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		start := time.Now()
		entry := trace.BackendTrace{Timestamp: start, Type: trace.BackendTrace3DAnimation, ModelName: modelConfig.Name, Backend: modelConfig.Backend, Summary: "3d: animate"}
		entry.ID = trace.BeginBackendTrace(entry)
		defer trace.CancelBackendTrace(entry.ID)
		defer func() {
			entry.Duration = time.Since(start)
			if err != nil {
				entry.Error = err.Error()
			}
			trace.RecordBackendTrace(entry)
		}()
	}
	request.ModelIdentity = modelConfig.Model
	result, err := inferenceModel.Animate3D(ctx, request)
	if err != nil {
		return err
	}
	if result == nil || !result.Success {
		return fmt.Errorf("animation backend failed: %s", result.GetMessage())
	}
	return nil
}
