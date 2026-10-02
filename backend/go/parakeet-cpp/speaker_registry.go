package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultSpeakerDistance = 0.5  // cosine 0.5, parakeet.cpp's default accept_threshold
	defaultSpeakerMargin   = 0.05 // parakeet.cpp's default runner-up margin
)

// parseSpeakerThreshold reads speaker_threshold, a distance (1 minus cosine, the unit
// /v1/voice/identify uses), and returns the cosine acceptance threshold the C-API takes.
// Empty means the default. A distance outside (0, 2) is an error.
func parseSpeakerThreshold(s string) (float32, error) {
	d := defaultSpeakerDistance
	if strings.TrimSpace(s) != "" {
		v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil || math.IsNaN(v) || v <= 0 || v >= 2 {
			return 0, fmt.Errorf("parakeet-cpp: speaker_threshold %q must be a distance in (0, 2) (1 minus cosine similarity)", s)
		}
		d = v
	}
	return nonZero(float32(1 - d)), nil
}

// minPositive stands in for an exact 0 threshold or margin. The C side reads 0 as
// "use the default", so a distance of 1 (cosine 0) or a margin of 0 would silently
// become 0.5 or 0.05.
const minPositive = float32(1e-6)

func nonZero(v float32) float32 {
	if v == 0 {
		return minPositive
	}
	return v
}

// parseSpeakerMargin reads speaker_margin, the runner-up margin in [0, 1).
func parseSpeakerMargin(s string) (float32, error) {
	if strings.TrimSpace(s) == "" {
		return defaultSpeakerMargin, nil
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || v < 0 || v >= 1 {
		return 0, fmt.Errorf("parakeet-cpp: speaker_margin %q must be a number in [0, 1)", s)
	}
	return nonZero(float32(v)), nil
}

// buildSpeakerRegistryLocked makes a parakeet_speaker_registry from the registered voices
// of one request or stream. Caller holds engineMu. It returns 0 (and no error) when there is
// nothing to build: no speaker model loaded or no usable voices. A voice whose embedding size
// differs from the speaker model's, or that the C side refuses, is skipped with a warning
// (without its name: the log is not for the caller who may not see voice names) so one bad
// voice cannot fail every request. The caller frees a non-zero result with freeSpeakerRegistry.
func (p *ParakeetCpp) buildSpeakerRegistryLocked(voices []*pb.KnownVoice) (uintptr, error) {
	if p.spkCtx == 0 || CppSpeakerRegistryNew == nil || CppSpeakerRegistryAddEmbedding == nil || len(voices) == 0 {
		return 0, nil
	}
	dim := 0
	if CppSpeakerDim != nil {
		dim = int(CppSpeakerDim(p.spkCtx))
	}
	reg := CppSpeakerRegistryNew()
	if reg == 0 {
		return 0, status.Error(codes.Internal, "parakeet-cpp: could not create a speaker registry")
	}
	added, skipped := 0, 0
	for _, v := range voices {
		emb := v.GetEmbedding()
		if v.GetName() == "" || len(emb) == 0 {
			xlog.Warn("parakeet-cpp: skipping a known voice with no name or embedding")
			continue
		}
		if dim > 0 && len(emb) != dim {
			xlog.Warn("parakeet-cpp: skipped a registered voice: embedding size does not match the speaker model's",
				"voice_size", len(emb), "speaker_model_size", dim)
			skipped++
			continue
		}
		if rc := CppSpeakerRegistryAddEmbedding(reg, voiceKey(v), &emb[0], int32(len(emb))); rc != 0 {
			xlog.Warn("parakeet-cpp: skipped a registered voice the speaker registry refused", "error", CppSpeakerRegistryLastError(reg))
			skipped++
			continue
		}
		added++
	}
	if added == 0 {
		if skipped > 0 {
			xlog.Warn("parakeet-cpp: no registered voice is usable with this speaker model; speakers stay unnamed", "skipped", skipped)
		}
		CppSpeakerRegistryFree(reg)
		return 0, nil
	}
	return reg, nil
}

// buildSpeakerRegistry is buildSpeakerRegistryLocked under engineMu.
func (p *ParakeetCpp) buildSpeakerRegistry(voices []*pb.KnownVoice) (uintptr, error) {
	p.engineMu.Lock()
	defer p.engineMu.Unlock()
	return p.buildSpeakerRegistryLocked(voices)
}

// freeSpeakerRegistry releases a registry built above. A zero handle is a no-op.
func (p *ParakeetCpp) freeSpeakerRegistry(reg uintptr) {
	if reg == 0 || CppSpeakerRegistryFree == nil {
		return
	}
	CppSpeakerRegistryFree(reg)
}

// Old transport clients have no IDs; retain their name-keyed semantics.
func voiceKey(v *pb.KnownVoice) string {
	if v.GetId() != "" {
		return v.GetId()
	}
	return v.GetName()
}
func voiceNames(voices []*pb.KnownVoice) map[string]string {
	names := make(map[string]string, len(voices))
	for _, v := range voices {
		names[voiceKey(v)] = v.GetName()
	}
	return names
}
func translateNames(names map[string]speakerNameJSON, display map[string]string) {
	for slot, match := range names {
		if name, ok := display[match.Name]; ok {
			match.Name = name
			names[slot] = match
		}
	}
}
