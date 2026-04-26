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

func FaceVerify(
	img1, img2 string,
	threshold float32,
	antiSpoofing bool,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) (*proto.FaceVerifyResponse, error) {
	opts := ModelOptions(modelConfig, appConfig)
	faceModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	if faceModel == nil {
		return nil, fmt.Errorf("could not load face recognition model")
	}

	var startTime time.Time
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems)
		startTime = time.Now()
	}

	res, err := faceModel.FaceVerify(context.Background(), &proto.FaceVerifyRequest{
		Img1:         img1,
		Img2:         img2,
		Threshold:    threshold,
		AntiSpoofing: antiSpoofing,
	})

	if appConfig.EnableTracing {
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceFaceVerify,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Error:     errStr,
		})
	}

	return res, err
}
