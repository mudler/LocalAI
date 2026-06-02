package backend

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

// TokenEntity is one detected span from a token-classification (NER)
// model. Mirrors pb.TokenClassifyEntity but keeps the proto type out of
// consumers. Start/End are BYTE offsets into the classified text,
// half-open (addressing text[Start:End]) — the proto contract. Group is
// the model's entity label (e.g. "private_person", "EMAIL").
type TokenEntity struct {
	Group string
	Start int
	End   int
	Score float32
	Text  string
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
// NOTE: unlike ModelScore this does not yet emit a Traces UI row — wire
// a trace.BackendTrace (new trace type) here if/when NER calls should
// show up alongside the requests they gate.
func ModelTokenClassify(text string, opts TokenClassifyOptions, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (func(ctx context.Context) ([]TokenEntity, error), error) {
	modelOpts := ModelOptions(modelConfig, appConfig)
	inferenceModel, err := loader.Load(modelOpts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	b, ok := inferenceModel.(grpc.Backend)
	if !ok {
		return nil, fmt.Errorf("token classification not supported by backend %q", modelConfig.Backend)
	}
	return func(ctx context.Context) ([]TokenEntity, error) {
		resp, err := b.TokenClassify(ctx, &pb.TokenClassifyRequest{
			Text:      text,
			Threshold: opts.Threshold,
		})
		if err != nil {
			return nil, err
		}
		return tokenClassifyResponseToEntities(resp), nil
	}, nil
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
