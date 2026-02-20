package backend

import (
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"

	"github.com/mudler/LocalAI/pkg/grpc"
	model "github.com/mudler/LocalAI/pkg/model"
)

func ModelEmbedding(s string, tokens []int, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (func() ([]float32, error), error) {

	opts := ModelOptions(modelConfig, appConfig)

	inferenceModel, err := loader.Load(opts...)
	if err != nil {
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
		// Remove trailing 0s
		for i := len(embeds) - 1; i >= 0; i-- {
			if embeds[i] == 0.0 {
				embeds = embeds[:i]
			} else {
				break
			}
		}
		return embeds, nil
	}

	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems)

		traceData := map[string]any{
			"input_text":         trace.TruncateString(s, 1000),
			"input_tokens_count": len(tokens),
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
