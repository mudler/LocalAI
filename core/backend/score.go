package backend

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	model "github.com/mudler/LocalAI/pkg/model"
)

// ScoreOptions controls a single Score request.
type ScoreOptions struct {
	// IncludeTokenLogprobs returns per-token log-probability detail for
	// each candidate. Off by default — the joint LogProb is enough for
	// ranking; callers that need calibration / entropy over the token
	// stream opt in.
	IncludeTokenLogprobs bool
	// LengthNormalize divides the joint log-prob by the candidate's
	// token count. Useful when comparing candidates of different
	// lengths — without it, longer candidates score lower by default.
	LengthNormalize bool
	// StablePrefixLen is the byte length of the prompt prefix that stays
	// identical across repeated scoring calls (0 = unknown); forwarded to
	// the backend as a state-reuse boundary hint.
	StablePrefixLen int
}

// CandidateScore is the per-candidate result. Mirrors pb.CandidateScore
// but avoids leaking the proto type to consumers.
type CandidateScore struct {
	LogProb                 float64
	LengthNormalizedLogProb float64
	NumTokens               int
	Tokens                  []TokenLogProb
}

type TokenLogProb struct {
	Token   string
	LogProb float64
}

// Scorer evaluates a model's joint log-probability of each candidate
// continuation given a shared prompt. Implemented by NewScorer over a
// model-loaded backend; the router's score classifier consumes this
// for multi-label policy selection. stablePrefixLen is the byte length
// of the prompt prefix that stays identical across calls (0 = unknown)
// — backends use it to place a state-reuse point at the boundary, which
// is what keeps repeat scoring fast on models that cannot rewind
// (hybrid/recurrent architectures).
type Scorer interface {
	Score(ctx context.Context, prompt string, stablePrefixLen int, candidates []string) ([]CandidateScore, error)
}

// NewScorer binds (loader, modelConfig, appConfig) into a Scorer. The
// underlying backend is resolved lazily on the first Score call.
// Returns nil only as a contract violation — callers that need to
// detect "model not loadable" should look up the config first.
func NewScorer(loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) Scorer {
	return &modelScorer{loader: loader, modelConfig: modelConfig, appConfig: appConfig}
}

type modelScorer struct {
	loader      *model.ModelLoader
	modelConfig config.ModelConfig
	appConfig   *config.ApplicationConfig
}

func (m *modelScorer) Score(ctx context.Context, prompt string, stablePrefixLen int, candidates []string) ([]CandidateScore, error) {
	fn, err := ModelScore(prompt, candidates, ScoreOptions{LengthNormalize: true, StablePrefixLen: stablePrefixLen}, m.loader, m.modelConfig, m.appConfig)
	if err != nil {
		return nil, err
	}
	return fn(ctx)
}

// ModelScore loads the backend for modelConfig and returns a closure
// that scores `candidates` against `prompt`. The closure is bound to
// the loaded model so callers can keep it around for repeat scoring
// within the same request without re-resolving the backend.
func ModelScore(prompt string, candidates []string, opts ScoreOptions, loader *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) (func(ctx context.Context) ([]CandidateScore, error), error) {
	modelOpts := ModelOptions(modelConfig, appConfig)
	inferenceModel, err := loader.Load(modelOpts...)
	if err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return nil, err
	}
	b, ok := inferenceModel.(grpc.Backend)
	if !ok {
		return nil, fmt.Errorf("scoring not supported by backend %q", modelConfig.Backend)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("Score: candidates must be non-empty")
	}
	return func(ctx context.Context) ([]CandidateScore, error) {
		// Surface score calls in the Traces UI alongside the LLM calls
		// they typically gate (router classifier, eval scoring). Without
		// this, a router-classified request shows only the downstream LLM
		// trace with no record of the classification that picked it.
		var startTime time.Time
		if appConfig.EnableTracing {
			trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
			startTime = time.Now()
		}
		resp, err := b.Score(ctx, &pb.ScoreRequest{
			ModelIdentity:        modelConfig.Model,
			Prompt:               prompt,
			Candidates:           candidates,
			IncludeTokenLogprobs: opts.IncludeTokenLogprobs,
			LengthNormalize:      opts.LengthNormalize,
			StablePrefixLen:      int32(opts.StablePrefixLen),
		})
		results := scoreResponseToCandidates(resp, opts.IncludeTokenLogprobs)
		if appConfig.EnableTracing {
			errStr := ""
			if err != nil {
				errStr = err.Error()
			}
			trace.RecordBackendTrace(trace.BackendTrace{
				Timestamp: startTime,
				Duration:  time.Since(startTime),
				Type:      trace.BackendTraceScore,
				ModelName: modelConfig.Name,
				Backend:   modelConfig.Backend,
				Summary:   trace.TruncateString(prompt, 200),
				Error:     errStr,
				Data: map[string]any{
					// Copy candidates so the trace buffer doesn't pin a
					// caller-owned slice for the lifetime of the ring.
					"candidates": append([]string(nil), candidates...),
					"results":    results,
				},
			})
		}
		if err != nil {
			return nil, err
		}
		return results, nil
	}, nil
}

// scoreResponseToCandidates converts the wire-format pb response into
// the value type consumed by callers. Extracted to keep ModelScore's
// closure trivial and so the conversion can be unit-tested without a
// real backend.
func scoreResponseToCandidates(resp *pb.ScoreResponse, includeTokens bool) []CandidateScore {
	if resp == nil {
		return nil
	}
	out := make([]CandidateScore, len(resp.Candidates))
	for i, c := range resp.Candidates {
		cs := CandidateScore{
			LogProb:                 c.LogProb,
			LengthNormalizedLogProb: c.LengthNormalizedLogProb,
			NumTokens:               int(c.NumTokens),
		}
		if includeTokens && len(c.Tokens) > 0 {
			cs.Tokens = make([]TokenLogProb, len(c.Tokens))
			for j, t := range c.Tokens {
				cs.Tokens[j] = TokenLogProb{Token: t.Token, LogProb: t.LogProb}
			}
		}
		out[i] = cs
	}
	return out
}
