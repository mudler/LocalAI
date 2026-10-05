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

// encoderFingerprint is what the loaded speaker encoder reports about itself:
// the embedding space ("voicedetect:<arch>:<name>:<dim>") and the "sha256:"
// identity of its weights. Both are empty on a libparakeet.so without the
// fingerprint, which turns the checks off.
func (p *ParakeetCpp) encoderFingerprint() (family, weights string) {
	if p.spkCtx == 0 || CppSpeakerRegistryAddEmbeddingFP == nil || CppSpeakerEncoderFamily == nil {
		return "", ""
	}
	if ptr := CppSpeakerEncoderFamily(p.spkCtx); ptr != 0 {
		family = goStringFromCPtr(ptr)
	}
	if CppSpeakerIdentity != nil {
		if ptr := CppSpeakerIdentity(p.spkCtx); ptr != 0 {
			weights = goStringFromCPtr(ptr)
		}
	}
	return family, weights
}

// voiceFamily is the encoder family of a registered voice, "" when unknown. A
// voice that only has a weights identity gets the family of the loaded encoder
// when the weights are the same file, because the same bytes are the same
// embedding space; a different hash proves nothing (another quantization of the
// encoder has the same family), so that voice stays unknown.
func voiceFamily(v *pb.KnownVoice, ownFamily, ownWeights string) string {
	if f := v.GetEncoderFamily(); f != "" {
		return f
	}
	if w := v.GetEncoderWeights(); w != "" && w == ownWeights {
		return ownFamily
	}
	return ""
}

// buildSpeakerRegistryLocked makes a parakeet_speaker_registry from the registered voices
// of one request or stream. Caller holds engineMu. It returns 0 (and no error) when there is
// nothing to build: no speaker model loaded or no usable voices. A voice whose embedding size
// differs from the speaker model's, or that the C side refuses, is skipped with a warning
// (without its name: the log is not for the caller who may not see voice names) so one bad
// voice cannot fail every request. The caller frees a non-zero result with freeSpeakerRegistry.
//
// Encoder fingerprint. The library keeps one registry per encoder and checks it against the
// speaker model before it names anyone, so the voices are sorted by what is known about the
// encoder that made them:
//
//   - matching: the family is the one of the loaded encoder;
//   - other: another family, which can never name a speaker here;
//   - unfingerprinted: no family (registered before it was recorded, or by an encoder that
//     cannot report one). The library refuses to mix these with fingerprinted voices in one
//     registry, so when there are any, the request keeps the old behaviour: they and the
//     matching voices go into one registry that has no fingerprint, and the encoder is
//     unverified (logged). With speaker_strict they are dropped instead.
//
// With no usable voice but voices of another family, the registry is built from those, so the
// library refuses the request and its message names both families.
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
	ownFamily, ownWeights := p.encoderFingerprint()
	skipped := 0
	var matching, other, plain []*pb.KnownVoice
	family := map[*pb.KnownVoice]string{}
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
		f := ""
		if ownFamily != "" {
			f = voiceFamily(v, ownFamily, ownWeights)
		}
		family[v] = f
		switch {
		case f == "":
			plain = append(plain, v)
		case f == ownFamily:
			matching = append(matching, v)
		default:
			other = append(other, v)
		}
	}

	// use is the voices that go into the registry; fingerprinted says whether they carry one.
	var use []*pb.KnownVoice
	fingerprinted, strictPlain := false, false
	switch {
	case len(plain) > 0 && !p.speakerStrict:
		use = append(plain, matching...)
		if ownFamily != "" {
			xlog.Warn("parakeet-cpp: registered voices without an encoder fingerprint are used unverified; register them again to record the encoder",
				"unfingerprinted", len(plain))
		}
		skipped += len(other)
	case len(matching) > 0:
		use, fingerprinted = matching, true
		skipped += len(other) + len(plain)
	case len(other) > 0:
		// Nothing usable here: let the library refuse and say which families differ.
		use, fingerprinted = other, true
		skipped += len(plain)
	case len(plain) > 0:
		// speaker_strict: the library refuses a registry without a fingerprint.
		use, strictPlain = plain, true
	}
	if len(use) == 0 {
		if skipped > 0 {
			xlog.Warn("parakeet-cpp: no registered voice is usable with this speaker model; speakers stay unnamed", "skipped", skipped)
		}
		CppSpeakerRegistryFree(reg)
		return 0, nil
	}
	if p.speakerStrict && len(plain) > 0 && !strictPlain {
		xlog.Warn("parakeet-cpp: speaker_strict: skipped registered voices without an encoder fingerprint", "skipped", len(plain))
	}

	if strictPlain && CppSpeakerRegistrySetStrict != nil {
		CppSpeakerRegistrySetStrict(reg, 1)
	}
	added := 0
	for _, v := range use {
		emb := v.GetEmbedding()
		var rc int32
		if fingerprinted {
			rc = CppSpeakerRegistryAddEmbeddingFP(reg, voiceKey(v), &emb[0], int32(len(emb)), family[v], v.GetEncoderWeights())
		} else {
			rc = CppSpeakerRegistryAddEmbedding(reg, voiceKey(v), &emb[0], int32(len(emb)))
		}
		if rc != 0 {
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
