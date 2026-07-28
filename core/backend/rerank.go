package backend

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

// RerankResult is the per-document score returned to consumers,
// narrowed from proto.RerankResult so callers don't need to depend on
// the proto package.
type RerankResult struct {
	Index          int
	RelevanceScore float32
}

// Reranker scores a list of candidate documents against a query.
// Returns one RerankResult per input document (no top-N truncation -
// callers that need it can sort and slice).
type Reranker interface {
	Rerank(ctx context.Context, query string, documents []string) ([]RerankResult, error)
}

// NewReranker binds (loader, modelConfig, appConfig) into a Reranker.
func NewReranker(loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) Reranker {
	return &modelReranker{loader: loader, modelConfig: modelConfig, appConfig: appConfig}
}

type modelReranker struct {
	loader      *model.ModelLoader
	modelConfig config.ModelConfig
	appConfig   *config.ApplicationConfig
}

func (r *modelReranker) Rerank(ctx context.Context, query string, documents []string) ([]RerankResult, error) {
	req := &proto.RerankRequest{
		Query:     query,
		Documents: documents,
		// TopN=0: backend returns scores for every document. Truncating
		// here would silently zero out labels the reranker considered
		// unlikely, which the router classifier needs.
	}
	res, err := Rerank(ctx, req, r.loader, r.appConfig, r.modelConfig)
	if err != nil {
		return nil, err
	}
	out := make([]RerankResult, 0, len(res.GetResults()))
	for _, dr := range res.GetResults() {
		out = append(out, RerankResult{Index: int(dr.GetIndex()), RelevanceScore: dr.GetRelevanceScore()})
	}
	return out, nil
}

func Rerank(ctx context.Context, request *proto.RerankRequest, loader *model.ModelLoader, appConfig *config.ApplicationConfig, modelConfig config.ModelConfig) (*proto.RerankResult, error) {
	// model.WithContext(ctx) overrides the app-context default set in
	// ModelOptions so distributed routing decisions reach the request's
	// X-LocalAI-Node holder via distributedhdr.Stamp.
	opts := ModelOptions(modelConfig, appConfig, model.WithContext(ctx))
	rerankModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}

	if rerankModel == nil {
		return nil, fmt.Errorf("could not load rerank model")
	}

	var startTime time.Time
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		startTime = time.Now()
	}

	// Stamped here, not at the HTTP handler: this is the function that also
	// builds ModelOptions from the same config, so the two values are equal by
	// construction (#10952).
	request.ModelIdentity = modelConfig.Model

	res, err := rerankModel.Rerank(ctx, request)

	if appConfig.EnableTracing {
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}

		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceRerank,
			ModelName: modelConfig.Name,
			Backend:   modelConfig.Backend,
			Summary:   trace.TruncateString(request.Query, 200),
			Error:     errStr,
			Data: map[string]any{
				"query":           request.Query,
				"documents_count": len(request.Documents),
				"top_n":           request.TopN,
			},
		})
	}

	return res, err
}
