package backend

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
)

// FaceEmbed loads the face recognition backend and returns a 512-d
// face embedding for the base64-encoded image. Unlike ModelEmbedding
// it passes the image through PredictOptions.Images — the insightface
// backend picks the highest-confidence face and returns its
// L2-normalized embedding.
func FaceEmbed(
	imgBase64 string,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	modelConfig config.ModelConfig,
) ([]float32, error) {
	opts := ModelOptions(modelConfig, appConfig)
	faceModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	if faceModel == nil {
		return nil, fmt.Errorf("could not load face recognition model")
	}

	predictOpts := gRPCPredictOpts(modelConfig, loader.ModelPath)
	predictOpts.Images = []string{imgBase64}

	res, err := faceModel.Embeddings(context.Background(), predictOpts)
	if err != nil {
		return nil, err
	}
	if len(res.Embeddings) == 0 {
		return nil, fmt.Errorf("face embedding returned empty vector (no face detected?)")
	}
	return res.Embeddings, nil
}
