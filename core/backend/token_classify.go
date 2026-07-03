package backend

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

// TokenEntity is one detected span from a token-classification (NER)
// model. Mirrors pb.TokenClassifyEntity but keeps the proto type out of
// consumers. Start/End are BYTE offsets into the classified text,
// half-open (addressing text[Start:End]) — the proto contract. Group is
// the model's entity label (e.g. "private_person", "EMAIL").
type TokenEntity struct {
	Group string  `json:"group"`
	Start int     `json:"start"`
	End   int     `json:"end"`
	Score float32 `json:"score"`
	Text  string  `json:"text"`
}

// TokenClassifyOptions controls a single TokenClassify request.
type TokenClassifyOptions struct {
	// Threshold drops entities the backend scores below this value at
	// the source. 0 returns everything the model emits; downstream
	// callers (e.g. the PII redactor's MinScore) can still filter
	// further once they know the per-request policy.
	Threshold float32
}

// TokenClassifier runs a token-classification model over text and
// returns the detected entity spans. Implemented by NewTokenClassifier
// over a model-loaded backend; the PII redactor's encoder/NER tier
// consumes this via a pii.NERDetector adapter (see
// core/services/routing/piidetector).
type TokenClassifier interface {
	TokenClassify(ctx context.Context, text string) ([]TokenEntity, error)
}

// NewTokenClassifier binds (loader, modelConfig, appConfig) into a
// TokenClassifier. The underlying backend is resolved lazily on the
// first call, mirroring NewScorer.
func NewTokenClassifier(loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig, opts TokenClassifyOptions) TokenClassifier {
	return &modelTokenClassifier{loader: loader, modelConfig: modelConfig, appConfig: appConfig, opts: opts}
}

type modelTokenClassifier struct {
	loader      *model.ModelLoader
	modelConfig config.ModelConfig
	appConfig   *config.ApplicationConfig
	opts        TokenClassifyOptions
}

func (m *modelTokenClassifier) TokenClassify(ctx context.Context, text string) ([]TokenEntity, error) {
	fn, err := ModelTokenClassify(text, m.opts, m.loader, m.modelConfig, m.appConfig)
	if err != nil {
		return nil, err
	}
	return fn(ctx)
}

// ModelTokenClassify loads the backend for modelConfig and returns a
// closure that classifies `text`. Mirrors ModelScore: the closure is
// bound to the loaded model so a caller can reuse it within a request
// without re-resolving the backend.
//
// When tracing is enabled it records a BackendTraceTokenClassify row so the
// detector's output — every entity's group, byte range, confidence and the
// matched substring — shows in the Traces UI alongside the request it gated.
// This is the technical view for debugging false positives (e.g. a phone
// number scored as SSN); the persisted PIIEvent keeps only a hash.
func ModelTokenClassify(text string, opts TokenClassifyOptions, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (func(ctx context.Context) ([]TokenEntity, error), error) {
	modelOpts := ModelOptions(modelConfig, appConfig)
	inferenceModel, err := loader.Load(modelOpts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	return func(ctx context.Context) ([]TokenEntity, error) {
		var startTime time.Time
		if appConfig.EnableTracing {
			trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
			startTime = time.Now()
		}
		resp, err := inferenceModel.TokenClassify(ctx, &pb.TokenClassifyRequest{
			Text:      text,
			Threshold: opts.Threshold,
		})
		entities := tokenClassifyResponseToEntities(resp)
		if appConfig.EnableTracing {
			trace.RecordBackendTrace(tokenClassifyTrace(modelConfig, text, opts.Threshold, entities, startTime, err))
		}
		if err != nil {
			return nil, err
		}
		return entities, nil
	}, nil
}

// tokenClassifyTrace assembles the Traces-UI row for one NER call: the input
// preview, the threshold, and every detected entity (group, byte range,
// confidence, matched text). Split out from the closure so the Data assembly
// is unit-testable without a live backend.
func tokenClassifyTrace(modelConfig config.ModelConfig, text string, threshold float32, entities []TokenEntity, start time.Time, callErr error) trace.BackendTrace {
	errStr := ""
	if callErr != nil {
		errStr = callErr.Error()
	}
	return trace.BackendTrace{
		Timestamp: start,
		Duration:  time.Since(start),
		Type:      trace.BackendTraceTokenClassify,
		ModelName: modelConfig.Name,
		Backend:   modelConfig.Backend,
		Summary:   trace.TruncateString(text, 200),
		Error:     errStr,
		Data: map[string]any{
			"input_chars": len(text),
			"threshold":   threshold,
			"entities":    entities,
		},
	}
}

// tokenClassifyResponseToEntities converts the wire-format response into
// the value type consumed by callers. Extracted so the conversion can be
// unit-tested without a real backend (see token_classify_test.go).
func tokenClassifyResponseToEntities(resp *pb.TokenClassifyResponse) []TokenEntity {
	if resp == nil {
		return nil
	}
	out := make([]TokenEntity, 0, len(resp.Entities))
	for _, e := range resp.Entities {
		if e == nil {
			continue
		}
		out = append(out, TokenEntity{
			Group: e.EntityGroup,
			Start: int(e.Start),
			End:   int(e.End),
			Score: e.Score,
			Text:  e.Text,
		})
	}
	return out
}
