package backend

import (
	"fmt"
	"time"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

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

func (ebs *EmbeddingsBackendService) Embeddings(request *schema.OpenAIRequest) *concurrency.JobResult[*schema.OpenAIRequest, *schema.OpenAIResponse] {

	jr, wjr := concurrency.NewJobResult[*schema.OpenAIRequest, *schema.OpenAIResponse](request)

	go func(wjr *concurrency.WritableJobResult[*schema.OpenAIRequest, *schema.OpenAIResponse]) {
		id := uuid.New().String()
		created := int(time.Now().Unix())
		request = *wjr.Request // TODO is needed?

		bc, err := ebs.bcl.LoadBackendConfigFileByName(request.Model, ebs.appConfig.ModelPath,
			config.LoadOptionDebug(ebs.appConfig.Debug),
			config.LoadOptionThreads(ebs.appConfig.Threads),
			config.LoadOptionContextSize(ebs.appConfig.ContextSize),
			config.LoadOptionF16(ebs.appConfig.F16),
		)
		if err != nil {
			log.Error().Err(err).Str("modelPath", ebs.appConfig.ModelPath).Msg("unable to load backend config")
			wjr.SetResult(nil, err)
			return
		}

		// Set the parameters for the language model prediction
		bc.UpdateFromOpenAIRequest(request)

		items := []schema.Item{}

		for i, s := range bc.InputToken {
			// get the model function to call for the result
			embedFn, err := ebs.modelEmbedding("", s, *bc)
			if err != nil {
				log.Error().Err(err).Ints("numeric tokens", s).Msg("error during modelEmbedding")
				wjr.SetResult(nil, err)
				return
			}

			embeddings, err := embedFn()
			if err != nil {
				log.Error().Err(err).Ints("numeric tokens", s).Msg("error during embedFn")
				wjr.SetResult(nil, err)
				return
			}
			items = append(items, schema.Item{Embedding: embeddings, Index: i, Object: "embedding"})
		}

		for i, s := range bc.InputStrings {
			// get the model function to call for the result
			embedFn, err := ebs.modelEmbedding(s, []int{}, *bc)
			if err != nil {
				log.Error().Err(err).Str("string tokens", s).Msg("error during modelEmbedding")
				wjr.SetResult(nil, err)
				return
			}

			embeddings, err := embedFn()
			if err != nil {
				log.Error().Err(err).Str("string tokens", s).Msg("error during embedFn")
				wjr.SetResult(nil, err)
				return
			}
			items = append(items, schema.Item{Embedding: embeddings, Index: i, Object: "embedding"})
		}

		resp := &schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   request.Model, // we have to return what the user sent here, due to OpenAI spec.
			Data:    items,
			Object:  "list",
		}
		wjr.SetResult(resp, nil)
	}(wjr)

	return jr
}

func (ebs *EmbeddingsBackendService) modelEmbedding(s string, tokens []int, backendConfig config.BackendConfig) (func() ([]float32, error), error) {
	modelFile := backendConfig.Model

	grpcOpts := gRPCModelOpts(backendConfig)

	var inferenceModel interface{}
	var err error

	opts := modelOpts(backendConfig, ebs.appConfig, []model.Option{
		model.WithLoadGRPCLoadModelOpts(grpcOpts),
		model.WithThreads(uint32(*backendConfig.Threads)),
		model.WithAssetDir(ebs.appConfig.AssetsDestination),
		model.WithModel(modelFile),
		model.WithContext(ebs.appConfig.Context),
	})

	if backendConfig.Backend == "" {
		inferenceModel, err = ebs.ml.GreedyLoader(opts...)
	} else {
		opts = append(opts, model.WithBackendString(backendConfig.Backend))
		inferenceModel, err = ebs.ml.BackendLoader(opts...)
	}
	if err != nil {
		return nil, err
	}

	var fn func() ([]float32, error)
	switch model := inferenceModel.(type) {
	case grpc.Backend:
		fn = func() ([]float32, error) {
			predictOptions := gRPCPredictOpts(backendConfig, ebs.appConfig.ModelPath)
			if len(tokens) > 0 {
				embeds := []int32{}

				for _, t := range tokens {
					embeds = append(embeds, int32(t))
				}
				predictOptions.EmbeddingTokens = embeds

				res, err := model.Embeddings(ebs.appConfig.Context, predictOptions)
				if err != nil {
					return nil, err
				}

				return res.Embeddings, nil
			}
			predictOptions.Embeddings = s

			res, err := model.Embeddings(ebs.appConfig.Context, predictOptions)
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
