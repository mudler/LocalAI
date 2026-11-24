package backend

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/trace"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrSlotCacheUnsupported is returned when the model's backend does not
// implement the SlotCache rpc (gRPC UNIMPLEMENTED).
var ErrSlotCacheUnsupported = errors.New("slot save and restore is not supported by this model's backend")

// slotCacheCaller is the part of a loaded backend that slot cache needs.
type slotCacheCaller interface {
	SlotCache(ctx context.Context, in *pb.SlotCacheRequest, opts ...grpc.CallOption) (*pb.SlotCacheResponse, error)
}

// callSlotCache sends the request and maps an UNIMPLEMENTED status to
// ErrSlotCacheUnsupported. Every other error is returned unchanged.
func callSlotCache(ctx context.Context, c slotCacheCaller, req *pb.SlotCacheRequest) (*pb.SlotCacheResponse, error) {
	resp, err := c.SlotCache(ctx, req)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
			return nil, ErrSlotCacheUnsupported
		}
		return nil, err
	}
	return resp, nil
}

// newSlotCacheRequest always sends the configured model as ModelIdentity, so a
// backend that checks it can refuse a request meant for another model.
func newSlotCacheRequest(action pb.SlotCacheAction, slot int32, id string, modelConfig config.ModelConfig) *pb.SlotCacheRequest {
	return &pb.SlotCacheRequest{
		ModelIdentity: modelConfig.Model,
		Action:        action,
		Slot:          slot,
		Id:            id,
	}
}

// ModelSlotCache saves or restores the prompt cache of one slot of a loaded
// model. On save id is empty and the backend issues one. On restore the caller
// must have validated id (see pkg/slotcache); the backend checks it again and
// accepts only an ID it issued for this model.
func ModelSlotCache(action pb.SlotCacheAction, slot int32, id string, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (schema.SlotCacheResponse, error) {
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

	resp, err := callSlotCache(appConfig.Context, inferenceModel, newSlotCacheRequest(action, slot, id, modelConfig))

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
			Summary:   fmt.Sprintf("slot cache %s", name),
			Error:     errStr,
			Data:      map[string]any{"action": name, "slot": slot, "id": id},
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
		ID:         resp.GetId(),
		Message:    resp.GetMessage(),
		NTokens:    resp.GetNTokens(),
		NBytes:     resp.GetNBytes(),
		DurationMS: resp.GetDurationMs(),
	}, nil
}
