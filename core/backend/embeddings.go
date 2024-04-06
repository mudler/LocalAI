package backend

import (
	"fmt"
	"time"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/google/uuid"

	"github.com/go-skynet/LocalAI/pkg/concurrency"
	"github.com/go-skynet/LocalAI/pkg/grpc"
	"github.com/go-skynet/LocalAI/pkg/model"
)

type EmbeddingsBackendService struct {
	ml        *model.ModelLoader
	bcl       *config.BackendConfigLoader
	appConfig *config.ApplicationConfig
}

func NewEmbeddingsBackendService(ml *model.ModelLoader, bcl *config.BackendConfigLoader, appConfig *config.ApplicationConfig) *EmbeddingsBackendService {
	return &EmbeddingsBackendService{
		ml:        ml,
		bcl:       bcl,
		appConfig: appConfig,
	}
}

func (ebs *EmbeddingsBackendService) Embeddings(request *schema.OpenAIRequest) <-chan concurrency.ErrorOr[*schema.OpenAIResponse] {

	resultChannel := make(chan concurrency.ErrorOr[*schema.OpenAIResponse])
	go func(request *schema.OpenAIRequest) {
		if request.Model == "" {
			request.Model = model.StableDiffusionBackend
		}

		bc, request, err := ebs.bcl.LoadBackendConfigForModelAndOpenAIRequest(request.Model, request, ebs.appConfig)
		if err != nil {
			resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: err}
			close(resultChannel)
			return
		}

		items := []schema.Item{}

		for i, s := range bc.InputToken {
			// get the model function to call for the result
			embedFn, err := modelEmbedding("", s, ebs.ml, bc, ebs.appConfig)
			if err != nil {
				resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: err}
				close(resultChannel)
				return
			}

			embeddings, err := embedFn()
			if err != nil {
				resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: err}
				close(resultChannel)
				return
			}
			items = append(items, schema.Item{Embedding: embeddings, Index: i, Object: "embedding"})
		}

		for i, s := range bc.InputStrings {
			// get the model function to call for the result
			embedFn, err := modelEmbedding(s, []int{}, ebs.ml, bc, ebs.appConfig)
			if err != nil {
				resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: err}
				close(resultChannel)
				return
			}

			embeddings, err := embedFn()
			if err != nil {
				resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Error: err}
				close(resultChannel)
				return
			}
			items = append(items, schema.Item{Embedding: embeddings, Index: i, Object: "embedding"})
		}

		id := uuid.New().String()
		created := int(time.Now().Unix())
		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   request.Model, // we have to return what the user sent here, due to OpenAI spec.
			Data:    items,
			Object:  "list",
		}
		resultChannel <- concurrency.ErrorOr[*schema.OpenAIResponse]{Value: resp}
		close(resultChannel)
	}(request)
	return resultChannel
}

func modelEmbedding(s string, tokens []int, loader *model.ModelLoader, backendConfig *config.BackendConfig, appConfig *config.ApplicationConfig) (func() ([]float32, error), error) {
	modelFile := backendConfig.Model

	grpcOpts := gRPCModelOpts(backendConfig)

	var inferenceModel interface{}
	var err error

	opts := modelOpts(backendConfig, appConfig, []model.Option{
		model.WithLoadGRPCLoadModelOpts(grpcOpts),
		model.WithThreads(uint32(*backendConfig.Threads)),
		model.WithAssetDir(appConfig.AssetsDestination),
		model.WithModel(modelFile),
		model.WithContext(appConfig.Context),
	})

	if backendConfig.Backend == "" {
		inferenceModel, err = loader.GreedyLoader(opts...)
	} else {
		opts = append(opts, model.WithBackendString(backendConfig.Backend))
		inferenceModel, err = loader.BackendLoader(opts...)
	}
	if err != nil {
		return nil, err
	}

	var fn func() ([]float32, error)
	switch model := inferenceModel.(type) {
	case grpc.Backend:
		fn = func() ([]float32, error) {
			predictOptions := gRPCPredictOpts(backendConfig, loader.ModelPath)
			if len(tokens) > 0 {
				embeds := []int32{}

				for _, t := range tokens {
					embeds = append(embeds, int32(t))
				}
				predictOptions.EmbeddingTokens = embeds

				res, err := model.Embeddings(appConfig.Context, predictOptions)
				if err != nil {
					return nil, err
				}

				return res.Embeddings, nil
			}
			predictOptions.Embeddings = s

			res, err := model.Embeddings(appConfig.Context, predictOptions)
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
