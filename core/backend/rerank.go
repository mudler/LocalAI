package backend

import (
	"context"
	"fmt"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/pkg/concurrency"
	"github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

type RerankBackendService struct {
	ml        *model.ModelLoader
	bcl       *config.BackendConfigLoader
	appConfig *config.ApplicationConfig
}

func NewRerankBackendService(ml *model.ModelLoader, bcl *config.BackendConfigLoader, appConfig *config.ApplicationConfig) *RerankBackendService {
	return &RerankBackendService{
		ml:        ml,
		bcl:       bcl,
		appConfig: appConfig,
	}
}

func (rbs *RerankBackendService) Rerank(request *schema.RerankRequest) *concurrency.JobResult[*schema.RerankRequest, *schema.JINARerankResponse] {
	jr, wjr := concurrency.NewJobResult[*schema.RerankRequest, *schema.JINARerankResponse](request)

	go func(wjr *concurrency.WritableJobResult[*schema.RerankRequest, *schema.JINARerankResponse]) {

		if request.Model == "" {
			wjr.SetResult(nil, fmt.Errorf("request.Model is required"))
			return
		}

		bc, err := rbs.bcl.LoadBackendConfigFileByName(request.Model, rbs.appConfig.ModelPath,
			config.LoadOptionDebug(rbs.appConfig.Debug),
			config.LoadOptionThreads(rbs.appConfig.Threads),
			config.LoadOptionContextSize(rbs.appConfig.ContextSize),
			config.LoadOptionF16(rbs.appConfig.F16),
		)
		if err != nil || bc == nil {
			log.Error().Err(err).Str("modelPath", rbs.appConfig.ModelPath).Msg("unable to load backend config")
			wjr.SetResult(nil, err)
			return
		}

		if request.Backend == "" {
			request.Backend = bc.Backend
		}

		grpcOpts := gRPCModelOpts(*bc)

		opts := modelOpts(config.BackendConfig{}, rbs.appConfig, []model.Option{
			model.WithBackendString(request.Backend),
			model.WithModel(request.Model),
			model.WithContext(rbs.appConfig.Context),
			model.WithAssetDir(rbs.appConfig.AssetsDestination),
			model.WithLoadGRPCLoadModelOpts(grpcOpts),
		})
		rerankModel, err := rbs.ml.BackendLoader(opts...)
		if err != nil {
			log.Error().Err(err).Msg("rerank error loading backend")
			wjr.SetResult(nil, err)
			return
		}

		if rerankModel == nil {
			wjr.SetResult(nil, fmt.Errorf("could not load rerank model"))
			return
		}

		protoReq := &proto.RerankRequest{
			Query:     request.Query,
			TopN:      int32(request.TopN),
			Documents: request.Documents,
		}

		protoRes, err := rerankModel.Rerank(context.Background(), protoReq)

		response := &schema.JINARerankResponse{
			Model: request.Model,
		}

		for _, r := range protoRes.Results {
			response.Results = append(response.Results, schema.JINADocumentResult{
				Index:          int(r.Index),
				Document:       schema.JINAText{Text: r.Text},
				RelevanceScore: float64(r.RelevanceScore),
			})
		}

		response.Usage.TotalTokens = int(protoRes.Usage.TotalTokens)
		response.Usage.PromptTokens = int(protoRes.Usage.PromptTokens)
		wjr.SetResult(response, nil)
	}(wjr)

	return jr
}
