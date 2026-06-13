package openai

import (
	"context"
	"math"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
)

type namedEmbedding struct {
	name string
	emb  []float32
}

// voiceGate decides whether a committed utterance's speaker is authorized to
// drive the realtime pipeline.
type voiceGate struct {
	cfg       config.PipelineVoiceRecognition // normalized
	registry  voicerecognition.Registry       // identify mode (nil otherwise)
	refEmbeds []namedEmbedding                // verify mode, pre-embedded refs
	refAudios []config.VoiceReference         // verify + anti-spoofing: ref paths

	// Seams for testing; set by newVoiceGate to call the real backend.
	embedFn  func(ctx context.Context, wavPath string) ([]float32, error)
	verifyFn func(ctx context.Context, uttWav, refWav string) (bool, error)
}

// Authorize embeds the utterance and decides allow/deny.
//
//	allowed: speaker is authorized.
//	matched: matched person's name (informational), empty if none.
//	reason:  human-readable deny reason.
//	err:     backend failure (caller should fail closed).
func (g *voiceGate) Authorize(ctx context.Context, wavPath string) (allowed bool, matched string, reason string, err error) {
	if g.cfg.Mode == config.VoiceGateModeVerify {
		return g.authorizeVerify(ctx, wavPath)
	}
	return g.authorizeIdentify(ctx, wavPath)
}

func (g *voiceGate) authorizeIdentify(ctx context.Context, wavPath string) (bool, string, string, error) {
	emb, err := g.embedFn(ctx, wavPath)
	if err != nil {
		return false, "", "embed failed", err
	}
	if len(emb) == 0 {
		return false, "", "no speech detected", nil
	}
	matches, err := g.registry.Identify(ctx, emb, 1)
	if err != nil {
		return false, "", "identify failed", err
	}
	if len(matches) == 0 {
		return false, "", "unknown speaker", nil
	}
	m := matches[0]
	if m.Distance > g.cfg.Threshold {
		return false, m.Metadata.Name, "distance above threshold", nil
	}
	if !g.allowMatch(m.Metadata) {
		return false, m.Metadata.Name, "speaker not in allow list", nil
	}
	return true, m.Metadata.Name, "", nil
}

// allowMatch reports whether a matched identity is authorized. An empty allow
// (no names and no labels) authorizes any registered speaker.
func (g *voiceGate) allowMatch(meta voicerecognition.Metadata) bool {
	a := g.cfg.Allow
	if len(a.Names) == 0 && len(a.Labels) == 0 {
		return true
	}
	for _, n := range a.Names {
		if n == meta.Name {
			return true
		}
	}
	for _, l := range a.Labels {
		if _, ok := meta.Labels[l]; ok {
			return true
		}
	}
	return false
}

func (g *voiceGate) authorizeVerify(ctx context.Context, wavPath string) (bool, string, string, error) {
	if g.cfg.AntiSpoofing {
		for _, r := range g.refAudios {
			ok, err := g.verifyFn(ctx, wavPath, r.Audio)
			if err != nil {
				return false, "", "verify failed", err
			}
			if ok {
				return true, r.Name, "", nil
			}
		}
		return false, "", "no reference matched", nil
	}

	emb, err := g.embedFn(ctx, wavPath)
	if err != nil {
		return false, "", "embed failed", err
	}
	if len(emb) == 0 {
		return false, "", "no speech detected", nil
	}
	for _, r := range g.refEmbeds {
		if cosineDistance(emb, r.emb) <= g.cfg.Threshold {
			return true, r.name, "", nil
		}
	}
	return false, "", "no reference matched", nil
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
