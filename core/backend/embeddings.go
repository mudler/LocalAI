package backend

import (
	"fmt"
	"time"

	"github.com/go-skynet/LocalAI/pkg/datamodel"
	"github.com/go-skynet/LocalAI/pkg/grpc"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

func ModelEmbedding(s string, tokens []int, loader *model.ModelLoader, c datamodel.Config, o *datamodel.StartupOptions) (func() ([]float32, error), error) {
	if !c.Embeddings {
		return nil, fmt.Errorf("endpoint disabled for this model by API configuration")
	}

	modelFile := c.Model

	grpcOpts := gRPCModelOpts(c)

	var inferenceModel interface{}
	var err error

	opts := modelOpts(c, o, []model.Option{
		model.WithLoadGRPCLoadModelOpts(grpcOpts),
		model.WithThreads(uint32(c.Threads)),
		model.WithAssetDir(o.AssetsDestination),
		model.WithModel(modelFile),
		model.WithContext(o.Context),
		model.WithExternalBackends(o.ExternalGRPCBackends, false),
	})

	if c.Backend == "" {
		inferenceModel, err = loader.GreedyLoader(opts...)
	} else {
		opts = append(opts, model.WithBackendString(c.Backend))
		inferenceModel, err = loader.BackendLoader(opts...)
	}
	if err != nil {
		return nil, err
	}

	var fn func() ([]float32, error)
	switch model := inferenceModel.(type) {
	case *grpc.Client:
		fn = func() ([]float32, error) {
			predictOptions := gRPCPredictOpts(c, loader.ModelPath)
			if len(tokens) > 0 {
				embeds := []int32{}

				for _, t := range tokens {
					embeds = append(embeds, int32(t))
				}
				predictOptions.EmbeddingTokens = embeds

				res, err := model.Embeddings(o.Context, predictOptions)
				if err != nil {
					return nil, err
				}

				return res.Embeddings, nil
			}
			predictOptions.Embeddings = s

			res, err := model.Embeddings(o.Context, predictOptions)
			if err != nil {
				return nil, err
			}

			return res.Embeddings, nil
		}
	default:
		fn = func() ([]float32, error) {
			return nil, fmt.Errorf("embeddings not supported by the backend")
		}
	}

	return func() ([]float32, error) {
		embeds, err := fn()
		if err != nil {
			return embeds, err
		}
		// Remove trailing 0s
		for i := len(embeds) - 1; i >= 0; i-- {
			if embeds[i] == 0.0 {
				embeds = embeds[:i]
			} else {
				break
			}
		}
		return embeds, nil
	}, nil
}

func EmbeddingOpenAIRequest(modelName string, input *datamodel.OpenAIRequest, cl *ConfigLoader, ml *model.ModelLoader, startupOptions *datamodel.StartupOptions) (*datamodel.OpenAIResponse, error) {
	config, input, err := ReadConfigFromFileAndCombineWithOpenAIRequest(modelName, input, cl, startupOptions)
	if err != nil {
		return nil, fmt.Errorf("failed reading parameters from request:%w", err)
	}

	log.Debug().Msgf("Parameter Config: %+v", config)
	items := []datamodel.Item{}

	for i, s := range config.InputToken {
		// get the model function to call for the result
		embedFn, err := ModelEmbedding("", s, ml, *config, startupOptions)
		if err != nil {
			return nil, err
		}

		embeddings, err := embedFn()
		if err != nil {
			return nil, err
		}
		items = append(items, datamodel.Item{Embedding: embeddings, Index: i, Object: "embedding"})
	}

	for i, s := range config.InputStrings {
		// get the model function to call for the result
		embedFn, err := ModelEmbedding(s, []int{}, ml, *config, startupOptions)
		if err != nil {
			return nil, err
		}

		embeddings, err := embedFn()
		if err != nil {
			return nil, err
		}
		items = append(items, datamodel.Item{Embedding: embeddings, Index: i, Object: "embedding"})
	}

	id := uuid.New().String()
	created := int(time.Now().Unix())
	return &datamodel.OpenAIResponse{
		ID:      id,
		Created: created,
		Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
		Data:    items,
		Object:  "list",
	}, nil
}
