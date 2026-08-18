package backend

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"

	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type embeddingPoolingCompatibilityError struct {
	message string
}

func (e *embeddingPoolingCompatibilityError) Error() string {
	return e.message
}

func poolingCompatibilityErrorf(format string, args ...any) error {
	return &embeddingPoolingCompatibilityError{message: fmt.Sprintf(format, args...)}
}

// IsEmbeddingPoolingCompatibilityError reports errors caused by a requested
// pooling scheme disagreeing with the layout declared by the loaded backend.
// HTTP callers map these client-selectable incompatibilities to status 400.
func IsEmbeddingPoolingCompatibilityError(err error) bool {
	var target *embeddingPoolingCompatibilityError
	return errors.As(err, &target)
}

// finishEmbeddingResult applies the model's Go-side pooling scheme only when
// the backend declares that it returned per-token vectors. Shape alone is not
// sufficient: one raw token and one final vector are both reported as 1 x dim.
// Legacy backends remain compatible with backend pooling, but cannot opt in to
// Go-side pooling until they declare their result layout.
func finishEmbeddingResult(res *proto.EmbeddingResult, modelConfig config.ModelConfig) ([]float32, error) {
	scheme := modelConfig.Pooling
	if scheme == "" || scheme == PoolingBackend {
		switch res.GetLayout() {
		case proto.EmbeddingLayout_EMBEDDING_LAYOUT_UNSPECIFIED, proto.EmbeddingLayout_EMBEDDING_LAYOUT_FINAL:
			return res.Embeddings, nil
		case proto.EmbeddingLayout_EMBEDDING_LAYOUT_PER_TOKEN:
			return nil, poolingCompatibilityErrorf(
				"pooling %q cannot pass through per-token embeddings: choose %q, %q or %q, or load the backend with pooling enabled",
				PoolingBackend, PoolingMean, PoolingLast, PoolingDecayedMean)
		default:
			return nil, poolingCompatibilityErrorf("pooling %q cannot use unknown embedding layout %d", PoolingBackend, res.GetLayout())
		}
	}
	switch res.GetLayout() {
	case proto.EmbeddingLayout_EMBEDDING_LAYOUT_PER_TOKEN:
		// Pool below after validating the reported matrix shape.
	case proto.EmbeddingLayout_EMBEDDING_LAYOUT_FINAL:
		return nil, poolingCompatibilityErrorf(
			"pooling %q needs per-token embeddings but this backend returned a final vector; configure raw per-token output if the backend supports it (llama.cpp: options [\"pooling:none\"])",
			scheme)
	case proto.EmbeddingLayout_EMBEDDING_LAYOUT_UNSPECIFIED:
		return nil, poolingCompatibilityErrorf(
			"pooling %q needs per-token embeddings but the backend did not declare its embedding layout: rebuild/update the backend to report EmbeddingResult.layout",
			scheme)
	default:
		return nil, poolingCompatibilityErrorf("pooling %q cannot use unknown embedding layout %d", scheme, res.GetLayout())
	}
	return PoolEmbeddingResult(res, scheme,
		float64(modelConfig.PoolingHalfLifeTokens),
		embdNormalizeFromOptions(modelConfig.Options))
}

// mapEmbeddingGRPCError turns a gRPC ResourceExhausted — the per-token
// payload of a very long conversation exceeding the 50MB message cap —
// into an actionable message; everything else passes through unchanged.
func mapEmbeddingGRPCError(err error) error {
	if status.Code(err) == codes.ResourceExhausted {
		return fmt.Errorf("conversation too long for per-token embeddings (gRPC message limit exceeded): %w", err)
	}
	return err
}

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

	// model.WithContext(ctx) overrides the app-context default set in
	// ModelOptions so distributed routing decisions reach the request's
	// X-LocalAI-Node holder via distributedhdr.Stamp.
	opts := ModelOptions(modelConfig, appConfig, model.WithContext(ctx))

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
					return nil, mapEmbeddingGRPCError(err)
				}

				return finishEmbeddingResult(res, modelConfig)
			}
			predictOptions.Embeddings = s

			res, err := model.Embeddings(appConfig.Context, predictOptions)
			if err != nil {
				return nil, mapEmbeddingGRPCError(err)
			}

			return finishEmbeddingResult(res, modelConfig)
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

		summary := trace.TruncateString(s, 200)
		if summary == "" {
			summary = fmt.Sprintf("tokens[%d]", len(tokens))
		}
		originalFn := wrappedFn
		wrappedFn = func() ([]float32, error) {
			startTime := time.Now()
			traceID := trace.BeginBackendTrace(trace.BackendTrace{Timestamp: startTime, Type: trace.BackendTraceEmbedding, ModelName: modelConfig.Name, Backend: modelConfig.Backend, Summary: summary})
			defer trace.CancelBackendTrace(traceID)
			result, err := originalFn()
			duration := time.Since(startTime)

			traceData["embedding_dimensions"] = len(result)

			errStr := ""
			if err != nil {
				errStr = err.Error()
			}

			trace.RecordBackendTrace(trace.BackendTrace{
				ID:        traceID,
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
	originalFn := wrappedFn
	wrappedFn = func() ([]float32, error) {
		release, err := AcquireGlobalBackendSlot()
		if err != nil {
			return nil, err
		}
		defer release()
		return originalFn()
	}

	return wrappedFn, nil
}
