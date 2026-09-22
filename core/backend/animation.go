// SPDX-License-Identifier: MIT
package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
)

func Model3DAnimation(ctx context.Context, request *proto.Animate3DRequest, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (responseMetadata []byte, err error) {
	var entry *trace.BackendTrace
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		inputs := map[string]any{}
		for name, input := range request.Inputs {
			if input == nil {
				continue
			}
			detail := map[string]any{"type": input.Type}
			if input.Type == "text" {
				detail["text"] = input.Data
			} else {
				detail["present"] = input.Data != ""
			}
			inputs[name] = detail
		}
		params := map[string]any{}
		for name, value := range request.Params {
			params[name] = value
		}
		summary := "3d: animate"
		if prompt := request.Inputs["prompt"]; prompt != nil && prompt.Type == "text" {
			summary += ": " + prompt.Data
		}
		entry = &trace.BackendTrace{Timestamp: time.Now(), Type: trace.BackendTrace3DAnimation,
			ModelName: modelConfig.Name, Backend: modelConfig.Backend, Summary: trace.TruncateString(summary, 200),
			Data: map[string]any{"inputs": inputs, "params": params, "model_file": modelConfig.Model, "stage": "loading_model"}}
		entry.ID = trace.BeginBackendTrace(*entry)
		defer trace.CancelBackendTrace(entry.ID)
		defer func() {
			entry.Duration = time.Since(entry.Timestamp)
			if err != nil {
				entry.Error = err.Error()
			}
			if len(responseMetadata) > 0 {
				var metadata map[string]any
				if decodeErr := json.Unmarshal(responseMetadata, &metadata); decodeErr != nil {
					entry.Data["metadata_error"] = decodeErr.Error()
					entry.Data["metadata_raw"] = string(responseMetadata)
				} else {
					entry.Data["metadata"] = metadata
				}
			}
			trace.RecordBackendTrace(*entry)
		}()
	}
	phaseStart := time.Now()
	inferenceModel, err := loader.Load(ModelOptions(modelConfig, appConfig)...)
	if entry != nil {
		entry.Data["load_ms"] = time.Since(phaseStart).Milliseconds()
	}
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	phaseStart = time.Now()
	if entry != nil {
		entry.Data["stage"] = "waiting_for_slot"
	}
	release, err := AcquireGlobalBackendSlot()
	if entry != nil {
		entry.Data["queue_ms"] = time.Since(phaseStart).Milliseconds()
	}
	if err != nil {
		return nil, err
	}
	defer release()
	if entry != nil {
		entry.Data["stage"] = "inference"
	}
	phaseStart = time.Now()
	request.ModelIdentity = modelConfig.Model
	result, err := inferenceModel.Animate3D(ctx, request)
	if entry != nil {
		entry.Data["inference_ms"] = time.Since(phaseStart).Milliseconds()
	}
	if err != nil {
		return nil, err
	}
	if result == nil || !result.Success {
		return nil, fmt.Errorf("animation backend failed: %s", result.GetMessage())
	}
	if entry != nil {
		entry.Data["stage"] = "completed"
		if info, statErr := os.Stat(request.Dst); statErr == nil {
			entry.Data["output_bytes"] = info.Size()
		}
	}
	return result.Metadata, nil
}
