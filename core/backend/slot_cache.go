package backend

import (
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/trace"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
)

// ModelSlotCache saves or restores the prompt cache of one slot of a loaded
// model. The caller must have validated filename (see pkg/slotcache); the
// backend checks it again before it touches the disk.
func ModelSlotCache(action pb.SlotCacheAction, slot int32, filename string, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (schema.SlotCacheResponse, error) {
	name := "restore"
	if action == pb.SlotCacheAction_SLOT_CACHE_ACTION_SAVE {
		name = "save"
	}

	inferenceModel, err := loader.Load(ModelOptions(modelConfig, appConfig)...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return schema.SlotCacheResponse{}, err
	}

	release, err := AcquireGlobalBackendSlot()
	if err != nil {
		return schema.SlotCacheResponse{}, err
	}
	defer release()

	var startTime time.Time
	var traceID string
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		startTime = time.Now()
		traceID = trace.BeginBackendTrace(trace.BackendTrace{Timestamp: startTime, Type: trace.BackendTraceTokenize, ModelName: modelConfig.Name, Backend: modelConfig.Backend, Summary: "slot cache " + name})
	}
	defer trace.CancelBackendTrace(traceID)

	resp, err := inferenceModel.SlotCache(appConfig.Context, &pb.SlotCacheRequest{
		ModelIdentity: modelConfig.Model,
		Action:        action,
		Slot:          slot,
		Filename:      filename,
	})

	if appConfig.EnableTracing {
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		trace.RecordBackendTrace(trace.BackendTrace{
			ID:        traceID,
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceTokenize,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Summary:   fmt.Sprintf("slot cache %s %s", name, filename),
			Error:     errStr,
			Data:      map[string]any{"action": name, "slot": slot, "filename": filename},
		})
	}
	if err != nil {
		return schema.SlotCacheResponse{}, err
	}
	if !resp.GetSuccess() {
		return schema.SlotCacheResponse{}, fmt.Errorf("slot cache %s failed: %s", name, resp.GetMessage())
	}

	return schema.SlotCacheResponse{
		Model:      modelConfig.Name,
		Action:     name,
		Slot:       resp.GetSlot(),
		Filename:   resp.GetFilename(),
		NTokens:    resp.GetNTokens(),
		NBytes:     resp.GetNBytes(),
		DurationMS: resp.GetDurationMs(),
	}, nil
}
