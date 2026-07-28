package backend

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
)

func VAD(request *schema.VADRequest,
	ctx context.Context,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig) (*schema.VADResponse, error) {
	// model.WithContext(ctx) overrides the app-context default set in
	// ModelOptions so distributed routing decisions reach the request's
	// X-LocalAI-Node holder via distributedhdr.Stamp.
	opts := ModelOptions(modelConfig, appConfig, model.WithContext(ctx))
	vadModel, err := ml.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}

	req := proto.VADRequest{
		ModelIdentity: modelConfig.Model,
		Audio:         request.Audio,
	}
	resp, err := vadModel.VAD(ctx, &req)
	if err != nil {
		return nil, err
	}

	segments := []schema.VADSegment{}
	for _, s := range resp.Segments {
		segments = append(segments, schema.VADSegment{Start: s.Start, End: s.End})
	}

	return &schema.VADResponse{
		Segments: segments,
	}, nil
}
