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

// Depth runs depth estimation (Depth Anything 3) on the supplied image and
// returns the full DepthResponse: per-pixel metric depth + confidence + sky,
// camera pose (extrinsics/intrinsics), an optional 3D point cloud and any
// requested exports (glb/colmap). The include_* flags and exports mirror the
// DepthRequest proto so callers can ask for less work.
func Depth(
	ctx context.Context,
	in *proto.DepthRequest,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (*proto.DepthResponse, error) {
	opts := ModelOptions(modelConfig, appConfig)
	depthModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}

	if depthModel == nil {
		return nil, fmt.Errorf("could not load depth model")
	}

	var startTime time.Time
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		startTime = time.Now()
	}

	// Stamped here for the same reason as in rerank.go: the caller builds the
	// request without a ModelConfig, this function has the one that loaded.
	in.ModelIdentity = modelConfig.Model

	res, err := depthModel.Depth(ctx, in)

	if appConfig.EnableTracing {
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}

		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceDepth,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Summary:   trace.TruncateString(in.GetSrc(), 200),
			Error:     errStr,
			Data: map[string]any{
				"exports": in.GetExports(),
			},
		})
	}

	return res, err
}
