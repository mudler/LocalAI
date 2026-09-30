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
	return float32(1 - d), nil
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
	return float32(v), nil
}

// buildSpeakerRegistryLocked makes a parakeet_speaker_registry from the registered voices
// of one request or stream. Caller holds engineMu. It returns 0 (and no error) when there is
// nothing to build: no speaker model loaded or no usable voices. A voice whose embedding size
// differs from the speaker model's is an error naming the voice and both sizes. The caller
// frees a non-zero result with freeSpeakerRegistry.
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
	added := 0
	for _, v := range voices {
		emb := v.GetEmbedding()
		if v.GetName() == "" || len(emb) == 0 {
			xlog.Warn("parakeet-cpp: skipping a known voice with no name or embedding")
			continue
		}
		if dim > 0 && len(emb) != dim {
			CppSpeakerRegistryFree(reg)
			return 0, status.Errorf(codes.InvalidArgument,
				"parakeet-cpp: known voice %q has a %d-value embedding but the speaker model produces %d; "+
					"register the voices again with the same speaker encoder", v.GetName(), len(emb), dim)
		}
		if rc := CppSpeakerRegistryAddEmbedding(reg, v.GetName(), &emb[0], int32(len(emb))); rc != 0 {
			msg := CppSpeakerRegistryLastError(reg)
			CppSpeakerRegistryFree(reg)
			return 0, status.Errorf(codes.InvalidArgument, "parakeet-cpp: known voice %q was refused: %s", v.GetName(), msg)
		}
		added++
	}
	if added == 0 {
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
