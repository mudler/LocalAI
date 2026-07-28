package openai

import (
	"context"
	"fmt"
	"math"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	"github.com/mudler/LocalAI/pkg/model"
)

type namedEmbedding struct {
	name string
	emb  []float32
}

// voiceGate decides whether a committed utterance's speaker is authorized to
// drive the realtime pipeline.
type voiceGate struct {
	cfg       config.PipelineVoiceRecognition // normalized
	recCfg    *config.ModelConfig             // resolved speaker-recognition model, for warm-up
	registry  voicerecognition.Registry       // identify mode (nil otherwise)
	refEmbeds []namedEmbedding                // verify mode, pre-embedded refs
	refAudios []config.VoiceReference         // verify + anti-spoofing: ref paths

	// Seams for testing; set by newVoiceGate to call the real backend.
	embedFn  func(ctx context.Context, wavPath string) ([]float32, error)
	verifyFn func(ctx context.Context, uttWav, refWav string) (bool, error)
}

// resolution is the outcome of resolving a committed utterance's speaker. It
// carries the surfacing-facing Speaker plus the metadata the policy layer needs
// (labels for the allow-list) and a human reason when no usable identity exists.
type resolution struct {
	speaker types.Speaker     // name/id/confidence/distance/matched
	labels  map[string]string // identify-mode metadata labels, for the allow-list
	found   bool              // a candidate identity existed at all
	reason  string            // why-unknown / deny reason at the resolve level
}

// confidence maps a cosine distance to a 0..100 score relative to the match
// threshold, mirroring the /v1/voice/identify endpoint.
func confidence(distance, threshold float32) float32 {
	if threshold <= 0 {
		return 0
	}
	c := (1 - distance/threshold) * 100
	if c < 0 {
		return 0
	}
	if c > 100 {
		return 100
	}
	return c
}

// newVoiceGate builds a gate from a pipeline's voice_recognition config. It
// validates fail-fast (before loading the model), loads the recognition model
// config, wires the real backend seams, and pre-embeds references for verify
// mode so per-turn cost is one utterance embed plus cheap cosine comparisons.
func newVoiceGate(
	cfg config.PipelineVoiceRecognition,
	cl *config.ModelConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	registry voicerecognition.Registry,
) (*voiceGate, error) {
	cfg.Normalize()
	if err := cfg.Validate(registry != nil); err != nil {
		return nil, err
	}

	// Resolved like every other pipeline sub-model (one alias hop), so an
	// aliased voice_recognition model gets its target's backend.
	recCfg, err := cl.LoadResolvedModelConfig(cfg.Model, ml.ModelPath)
	if err != nil {
		return nil, fmt.Errorf("voice_recognition: failed to load model %q: %w", cfg.Model, err)
	}
	if valid, _ := recCfg.Validate(); !valid {
		return nil, fmt.Errorf("voice_recognition: invalid model config %q", cfg.Model)
	}

	g := &voiceGate{
		cfg:      cfg,
		recCfg:   recCfg,
		registry: registry,
		embedFn: func(ctx context.Context, wavPath string) ([]float32, error) {
			res, err := backend.VoiceEmbed(ctx, wavPath, ml, appConfig, *recCfg)
			if err != nil {
				return nil, err
			}
			return res.Embedding, nil
		},
		verifyFn: func(ctx context.Context, uttWav, refWav string) (bool, error) {
			res, err := backend.VoiceVerify(ctx, uttWav, refWav, cfg.Threshold, true, ml, appConfig, *recCfg)
			if err != nil {
				return false, err
			}
			return res.Verified, nil
		},
	}

	if cfg.Mode == config.VoiceGateModeVerify {
		if cfg.AntiSpoofing {
			g.refAudios = cfg.References
		} else {
			for _, r := range cfg.References {
				emb, err := g.embedFn(context.Background(), r.Audio)
				if err != nil {
					return nil, fmt.Errorf("voice_recognition: failed to embed reference %q: %w", r.Name, err)
				}
				g.refEmbeds = append(g.refEmbeds, namedEmbedding{name: r.Name, emb: emb})
			}
		}
	}

	return g, nil
}

// Resolve embeds the utterance once and resolves the speaker's identity. It does
// NOT apply the authorization policy (see authorize). On a backend error it
// returns the error and a resolution whose reason explains the failure.
func (g *voiceGate) Resolve(ctx context.Context, wavPath string) (resolution, error) {
	if g.cfg.Mode == config.VoiceGateModeVerify {
		return g.resolveVerify(ctx, wavPath)
	}
	return g.resolveIdentify(ctx, wavPath)
}

func (g *voiceGate) resolveIdentify(ctx context.Context, wavPath string) (resolution, error) {
	emb, err := g.embedFn(ctx, wavPath)
	if err != nil {
		return resolution{reason: "embed failed"}, err
	}
	if len(emb) == 0 {
		return resolution{reason: "no speech detected"}, nil
	}
	matches, err := g.registry.Identify(ctx, emb, 1)
	if err != nil {
		return resolution{reason: "identify failed"}, err
	}
	if len(matches) == 0 {
		return resolution{reason: "unknown speaker"}, nil
	}
	m := matches[0]
	matched := m.Distance <= g.cfg.Threshold
	r := resolution{
		speaker: types.Speaker{
			Name:       m.Metadata.Name,
			ID:         m.Metadata.ID,
			Labels:     m.Metadata.Labels,
			Distance:   m.Distance,
			Confidence: confidence(m.Distance, g.cfg.Threshold),
			Matched:    matched,
		},
		labels: m.Metadata.Labels,
		found:  true,
	}
	if !matched {
		r.reason = "distance above threshold"
	}
	return r, nil
}

func (g *voiceGate) resolveVerify(ctx context.Context, wavPath string) (resolution, error) {
	if g.cfg.AntiSpoofing {
		for _, ref := range g.refAudios {
			ok, err := g.verifyFn(ctx, wavPath, ref.Audio)
			if err != nil {
				return resolution{reason: "verify failed"}, err
			}
			if ok {
				return resolution{
					speaker: types.Speaker{Name: ref.Name, Confidence: 100, Matched: true},
					found:   true,
				}, nil
			}
		}
		return resolution{reason: "no reference matched"}, nil
	}

	emb, err := g.embedFn(ctx, wavPath)
	if err != nil {
		return resolution{reason: "embed failed"}, err
	}
	if len(emb) == 0 {
		return resolution{reason: "no speech detected"}, nil
	}
	for _, ref := range g.refEmbeds {
		d := cosineDistance(emb, ref.emb)
		if d <= g.cfg.Threshold {
			return resolution{
				speaker: types.Speaker{Name: ref.name, Distance: d, Confidence: confidence(d, g.cfg.Threshold), Matched: true},
				found:   true,
			}, nil
		}
	}
	return resolution{reason: "no reference matched"}, nil
}

// authorize applies the gate's policy to an already-resolved identity.
func (g *voiceGate) authorize(r resolution) (allowed bool, reason string) {
	if g.cfg.Mode == config.VoiceGateModeVerify {
		if r.speaker.Matched {
			return true, ""
		}
		if r.reason == "" {
			return false, "no reference matched"
		}
		return false, r.reason
	}
	if !r.found {
		return false, r.reason
	}
	if !r.speaker.Matched {
		return false, "distance above threshold"
	}
	if !g.allowMatch(r.speaker.Name, r.labels) {
		return false, "speaker not in allow list"
	}
	return true, ""
}

// allowMatch reports whether a matched identity is authorized. An empty allow
// (no names and no labels) authorizes any registered speaker.
func (g *voiceGate) allowMatch(name string, labels map[string]string) bool {
	a := g.cfg.Allow
	if len(a.Names) == 0 && len(a.Labels) == 0 {
		return true
	}
	for _, n := range a.Names {
		if n == name {
			return true
		}
	}
	for _, l := range a.Labels {
		if _, ok := labels[l]; ok {
			return true
		}
	}
	return false
}

// Authorize is the legacy convenience wrapper: resolve then apply policy.
//
//	allowed: speaker is authorized.
//	matched: matched person's name (informational), empty if none.
//	reason:  human-readable deny reason.
//	err:     backend failure (caller should fail closed).
func (g *voiceGate) Authorize(ctx context.Context, wavPath string) (allowed bool, matched string, reason string, err error) {
	r, rerr := g.Resolve(ctx, wavPath)
	if rerr != nil {
		return false, "", r.reason, rerr
	}
	allowed, reason = g.authorize(r)
	return allowed, r.speaker.Name, reason, nil
}

// decide interprets an Authorize result against the gate's when-policy and the
// session's prior verification state.
//   proceed:      run the LLM response for this utterance.
//   markVerified: record a successful first-utterance verification.
// Note: when:first AND alreadyVerified is normally handled by the caller
// skipping Authorize entirely; if it still reaches here, proceed is true.
func (g *voiceGate) decide(alreadyVerified, allowed bool) (proceed, markVerified bool) {
	if g.cfg.When == config.VoiceGateWhenFirst {
		if alreadyVerified {
			return true, false
		}
		return allowed, allowed
	}
	return allowed, false
}

// cosineDistance returns 1 - cosine_similarity, matching the voice registry's
// distance convention (lower = closer). Returns 1 (treated as "no match") for
// zero-length, mismatched, or zero-magnitude vectors.
func cosineDistance(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 1
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 1
	}
	return float32(1 - dot/(math.Sqrt(na)*math.Sqrt(nb)))
}
