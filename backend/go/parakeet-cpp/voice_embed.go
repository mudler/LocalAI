// SPDX-License-Identifier: MIT
package main

import (
	"math"
	"unsafe"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// speakerEmbedRate is the sample rate the speaker encoder takes; decodeWavMono16k
// already resamples to it.
const speakerEmbedRate = 16000

// speakerEmbedReady reports why the loaded model cannot embed a voice, or nil.
// An older libparakeet.so without parakeet_capi_speaker_embed_pcm gets Unimplemented;
// a model without a speaker encoder (no speaker_component/speaker_model) gets
// FailedPrecondition. Caller holds engineMu.
func (p *ParakeetCpp) speakerEmbedReady() error {
	if CppSpeakerEmbedPCM == nil || CppFreeFloats == nil {
		return status.Error(codes.Unimplemented,
			"parakeet-cpp: voice embedding needs a libparakeet.so with parakeet_capi_speaker_embed_pcm; rebuild the backend against a newer parakeet.cpp")
	}
	if p.spkCtx == 0 {
		return status.Error(codes.FailedPrecondition,
			"parakeet-cpp: no speaker encoder loaded; use a bundle with a voice component (speaker_component) or set speaker_model")
	}
	return nil
}

// embedPCMLocked embeds mono 16 kHz samples and copies the vector out of the C buffer.
// Caller holds engineMu.
func (p *ParakeetCpp) embedPCMLocked(pcm []float32) ([]float32, error) {
	var vec uintptr
	var dim int32
	rc := CppSpeakerEmbedPCM(p.spkCtx, &pcm[0], int32(len(pcm)), speakerEmbedRate, unsafe.Pointer(&vec), unsafe.Pointer(&dim))
	if rc != 0 || vec == 0 || dim <= 0 {
		if vec != 0 {
			CppFreeFloats(vec)
		}
		return nil, status.Errorf(codes.Internal, "parakeet-cpp: speaker embedding failed: %s", CppLastError(p.spkCtx))
	}
	defer CppFreeFloats(vec)
	src := unsafe.Slice((*float32)(unsafe.Pointer(vec)), int(dim)) //nolint:govet // C-owned malloc'd vector, copied out before free
	out := make([]float32, int(dim))
	copy(out, src)
	return out, nil
}

// embedFile decodes an audio file to 16 kHz mono and embeds it under engineMu.
func (p *ParakeetCpp) embedFile(path string) ([]float32, error) {
	if path == "" {
		return nil, status.Error(codes.InvalidArgument, "parakeet-cpp: audio path is required")
	}
	pcm, _, err := decodeWavMono16k(path)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "parakeet-cpp: decode audio: %s", err)
	}
	if len(pcm) == 0 {
		return nil, status.Error(codes.InvalidArgument, "parakeet-cpp: empty audio")
	}
	p.engineMu.Lock()
	defer p.engineMu.Unlock()
	if err := p.speakerEmbedReady(); err != nil {
		return nil, err
	}
	return p.embedPCMLocked(pcm)
}

// speakerIdentity is the "sha256:" identity of the loaded speaker encoder weights, "" when
// the library cannot report one. Caller holds engineMu.
func (p *ParakeetCpp) speakerIdentityLocked() string {
	if p.spkCtx == 0 || CppSpeakerIdentity == nil {
		return ""
	}
	return goStringFromCPtr(CppSpeakerIdentity(p.spkCtx))
}

// VoiceEmbed returns the speaker embedding of the audio file in req.Audio. The embedding
// space is the one of the loaded speaker encoder, so a bundle's voice component gives the
// same vectors as the standalone model with the same weights.
func (p *ParakeetCpp) VoiceEmbed(req *pb.VoiceEmbedRequest) (pb.VoiceEmbedResponse, error) {
	emb, err := p.embedFile(req.GetAudio())
	if err != nil {
		return pb.VoiceEmbedResponse{}, err
	}
	p.engineMu.Lock()
	model := p.speakerIdentityLocked()
	p.engineMu.Unlock()
	return pb.VoiceEmbedResponse{Embedding: emb, Model: model}, nil
}

// VoiceVerify embeds both clips and compares them by cosine distance. A request threshold
// of 0 or less uses the default speaker distance. There is no anti-spoofing head, so a
// request for it is refused rather than answered without the check.
func (p *ParakeetCpp) VoiceVerify(req *pb.VoiceVerifyRequest) (pb.VoiceVerifyResponse, error) {
	if req.GetAntiSpoofing() {
		return pb.VoiceVerifyResponse{}, status.Error(codes.Unimplemented, "parakeet-cpp: anti-spoofing is not supported")
	}
	if req.GetAudio1() == "" || req.GetAudio2() == "" {
		return pb.VoiceVerifyResponse{}, status.Error(codes.InvalidArgument, "parakeet-cpp: audio1 and audio2 are required")
	}
	a, err := p.embedFile(req.GetAudio1())
	if err != nil {
		return pb.VoiceVerifyResponse{}, err
	}
	b, err := p.embedFile(req.GetAudio2())
	if err != nil {
		return pb.VoiceVerifyResponse{}, err
	}
	if len(a) != len(b) {
		return pb.VoiceVerifyResponse{}, status.Errorf(codes.Internal, "parakeet-cpp: embedding sizes differ (%d and %d)", len(a), len(b))
	}
	threshold := req.GetThreshold()
	if threshold <= 0 {
		threshold = defaultSpeakerDistance
	}
	distance := cosineDistance(a, b)
	confidence := float32(math.Max(0, math.Min(100, (1-float64(distance)/float64(threshold))*100)))
	p.engineMu.Lock()
	model := p.speakerIdentityLocked()
	p.engineMu.Unlock()
	return pb.VoiceVerifyResponse{
		Verified:   distance <= threshold,
		Distance:   distance,
		Threshold:  threshold,
		Confidence: confidence,
		Model:      model,
	}, nil
}

// cosineDistance is 1 minus the cosine similarity; a zero vector counts as maximally far.
func cosineDistance(a, b []float32) float32 {
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
