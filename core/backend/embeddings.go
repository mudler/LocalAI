package backend

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"

	"github.com/mudler/LocalAI/pkg/grpc"
	model "github.com/mudler/LocalAI/pkg/model"
)

// Embedder produces a fixed-dimension vector from a prompt. The
// router's L2 embedding cache uses it to look up semantically-similar
// past decisions.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// NewEmbedder binds (loader, modelConfig, appConfig) into an Embedder.
func NewEmbedder(loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) Embedder {
	return &modelEmbedder{loader: loader, modelConfig: modelConfig, appConfig: appConfig}
}

type modelEmbedder struct {
	loader      *model.ModelLoader
	modelConfig config.ModelConfig
	appConfig   *config.ApplicationConfig
}

func (e *modelEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	fn, err := ModelEmbedding(ctx, text, nil, e.loader, e.modelConfig, e.appConfig)
	if err != nil {
		return nil, err
	}
	return fn()
}

func ModelEmbedding(ctx context.Context, s string, tokens []int, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (func() ([]float32, error), error) {

	// model.WithContext carries the request context into the load so distributed
	// routing decisions reach the request's X-LocalAI-Node holder via
	// distributedhdr.Stamp. context.WithoutCancel keeps those values but drops
	// the request's cancellation, so a slow first load still completes and
	// caches if the client disconnects instead of aborting the LoadModel RPC and
	// tearing down the backend process (issue #10636). Inference below keeps the
	// cancellable ctx, so a disconnect still stops generation.
	opts := ModelOptions(modelConfig, appConfig, model.WithContext(context.WithoutCancel(ctx)))

	inferenceModel, err := loader.Load(opts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}

	var fn func() ([]float32, error)
	switch model := inferenceModel.(type) {
	case grpc.Backend:
		fn = func() ([]float32, error) {
			predictOptions := gRPCPredictOpts(modelConfig, loader.ModelPath)
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

	wrappedFn := func() ([]float32, error) {
		embeds, err := fn()
		if err != nil {
			return embeds, err
		}
		// Return embeddings as-is to preserve full dimensionality
		// Trailing zeros may be valid values in some embedding models
		return embeds, nil
	}

	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)

		traceData := map[string]any{
			"input_text": trace.TruncateString(s, 1000),
		}
		// Only present for token-mode callers (pre-tokenized override);
		// emitting "0" alongside input_text would read as "consumed zero
		// tokens", which is wrong.
		if len(tokens) > 0 {
			traceData["input_tokens_count"] = len(tokens)
		}

		startTime := time.Now()
		originalFn := wrappedFn
		wrappedFn = func() ([]float32, error) {
			result, err := originalFn()
			duration := time.Since(startTime)

			traceData["embedding_dimensions"] = len(result)

			errStr := ""
			if err != nil {
				errStr = err.Error()
			}

			summary := trace.TruncateString(s, 200)
			if summary == "" {
				summary = fmt.Sprintf("tokens[%d]", len(tokens))
			}

			trace.RecordBackendTrace(trace.BackendTrace{
				Timestamp: startTime,
				Duration:  duration,
				Type:      trace.BackendTraceEmbedding,
				ModelName: modelConfig.Name,
				Backend:   modelConfig.Backend,
				Summary:   summary,
				Error:     errStr,
				Data:      traceData,
			})

			return result, err
		}
	}

	return wrappedFn, nil
}
