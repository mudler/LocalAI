package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// purego-bound entry points from libvoicedetect.so. Names match
// voicedetect_capi.h exactly so a `nm libvoicedetect.so | grep voicedetect_capi`
// is enough to spot drift.
//
// The opaque ctx and the malloc'd char*/float* return values are declared as
// uintptr so we get the raw pointer back and can release it via the matching
// capi free function. purego's native string/[]float32 returns would copy and
// forget the original pointer, leaking the C-owned buffer on every call.
var (
	CppAbiVersion  func() int32
	CppLoad        func(ggufPath string) uintptr
	CppFree        func(ctx uintptr)
	CppLastError   func(ctx uintptr) string
	CppFreeString  func(s uintptr)
	CppFreeVec     func(v uintptr)
	CppEmbedPath   func(ctx uintptr, wavPath string, outVec, outDim unsafe.Pointer) int32
	CppEmbedPCM    func(ctx uintptr, pcm []float32, nSamples, sampleRate int32, outVec, outDim unsafe.Pointer) int32
	CppVerifyPaths func(ctx uintptr, a, b string, threshold float32, outDistance, outVerified unsafe.Pointer) int32
	CppAnalyzeJSON func(ctx uintptr, wavPath string) uintptr
)

// VoiceDetect implements the speaker-recognition voice subset of the Backend
// gRPC service over libvoicedetect.so. The C side keeps a single loaded model
// plus a per-ctx last-error buffer and is not reentrant, so base.SingleThread
// serializes every call.
type VoiceDetect struct {
	base.SingleThread
	opts   loadOptions
	ctxPtr uintptr
}

func (v *VoiceDetect) Load(opts *pb.ModelOptions) error {
	model := opts.ModelFile
	if model == "" {
		model = opts.ModelPath
	}
	if !filepath.IsAbs(model) && opts.ModelPath != "" {
		model = filepath.Join(opts.ModelPath, model)
	}
	if model == "" {
		return errors.New("voice-detect: ModelFile is required")
	}

	v.opts = parseOptions(opts.Options)
	if v.opts.modelName == "" {
		v.opts.modelName = filepath.Base(model)
	}

	xlog.Info("voice-detect: loading model", "model", model,
		"verify_threshold", v.opts.verifyThreshold, "abi", CppAbiVersion())

	ctx := CppLoad(model)
	if ctx == 0 {
		// The last-error buffer lives on the ctx that was never returned, so
		// surface the path the operator tried to load instead.
		return fmt.Errorf("voice-detect: voicedetect_capi_load failed for %q", model)
	}
	v.ctxPtr = ctx
	return nil
}

// VoiceEmbed returns the L2-normalized speaker embedding for an audio clip.
// The request carries a filesystem PATH; the HTTP layer materializes
// base64/URL/data-URI inputs to a temp file before the gRPC call.
func (v *VoiceDetect) VoiceEmbed(req *pb.VoiceEmbedRequest) (pb.VoiceEmbedResponse, error) {
	if v.ctxPtr == 0 {
		return pb.VoiceEmbedResponse{}, errors.New("voice-detect: model not loaded")
	}
	if req.Audio == "" {
		return pb.VoiceEmbedResponse{}, errors.New("voice-detect: audio path is required")
	}
	emb, err := v.embedPath(req.Audio)
	if err != nil {
		return pb.VoiceEmbedResponse{}, err
	}
	return pb.VoiceEmbedResponse{Embedding: emb, Model: v.opts.modelName}, nil
}

func (v *VoiceDetect) embedPath(path string) ([]float32, error) {
	var vec uintptr
	var dim int32
	rc := CppEmbedPath(v.ctxPtr, path, unsafe.Pointer(&vec), unsafe.Pointer(&dim))
	if rc != 0 || vec == 0 || dim <= 0 {
		return nil, v.lastErr("embed", path)
	}
	defer CppFreeVec(vec)
	// Copy out of the C-owned malloc'd buffer before freeing it. The
	// uintptr->Pointer conversion trips vet's unsafeptr check, which can't tell
	// a C heap pointer from Go-managed memory; safe here, the GC neither tracks
	// nor moves this buffer and we copy immediately.
	src := unsafe.Slice((*float32)(unsafe.Pointer(vec)), int(dim)) //nolint:govet // C-owned malloc'd vector, copied out before free
	out := make([]float32, int(dim))
	copy(out, src)
	return out, nil
}

// VoiceVerify embeds two clips and reports whether they are the same speaker by
// cosine distance against a threshold. A request threshold <= 0 falls back to
// the model-configured default (verify_threshold option, 0.25 if unset).
func (v *VoiceDetect) VoiceVerify(req *pb.VoiceVerifyRequest) (pb.VoiceVerifyResponse, error) {
	if v.ctxPtr == 0 {
		return pb.VoiceVerifyResponse{}, errors.New("voice-detect: model not loaded")
	}
	if req.Audio1 == "" || req.Audio2 == "" {
		return pb.VoiceVerifyResponse{}, errors.New("voice-detect: audio1 and audio2 are required")
	}

	threshold := req.Threshold
	if threshold <= 0 {
		threshold = v.opts.verifyThreshold
	}

	started := time.Now()
	var distance float32
	var verified int32
	rc := CppVerifyPaths(v.ctxPtr, req.Audio1, req.Audio2, threshold,
		unsafe.Pointer(&distance), unsafe.Pointer(&verified))
	if rc != 0 {
		return pb.VoiceVerifyResponse{}, v.lastErr("verify", req.Audio1+","+req.Audio2)
	}
	elapsedMs := float32(time.Since(started).Seconds() * 1000.0)

	// Confidence decays linearly from 100 at distance 0 to 0 at the threshold,
	// matching the Python speaker-recognition backend's reporting.
	confidence := float32(0)
	if threshold > 0 {
		confidence = float32(math.Max(0, math.Min(100, (1.0-float64(distance)/float64(threshold))*100.0)))
	}

	return pb.VoiceVerifyResponse{
		Verified:         verified != 0,
		Distance:         distance,
		Threshold:        threshold,
		Confidence:       confidence,
		Model:            v.opts.modelName,
		ProcessingTimeMs: elapsedMs,
	}, nil
}

// VoiceAnalyze runs the age/gender/emotion heads on a single clip. The C-API
// always evaluates every supported head, so the request's actions filter is
// advisory and the full analysis is returned as a single segment (the engine
// does not produce time-bounded segments).
func (v *VoiceDetect) VoiceAnalyze(req *pb.VoiceAnalyzeRequest) (pb.VoiceAnalyzeResponse, error) {
	if v.ctxPtr == 0 {
		return pb.VoiceAnalyzeResponse{}, errors.New("voice-detect: model not loaded")
	}
	if req.Audio == "" {
		return pb.VoiceAnalyzeResponse{}, errors.New("voice-detect: audio path is required")
	}

	ptr := CppAnalyzeJSON(v.ctxPtr, req.Audio)
	if ptr == 0 {
		return pb.VoiceAnalyzeResponse{}, v.lastErr("analyze", req.Audio)
	}
	defer CppFreeString(ptr)

	seg, err := parseAnalyzeJSON(goStringFromCPtr(ptr))
	if err != nil {
		return pb.VoiceAnalyzeResponse{}, fmt.Errorf("voice-detect: analyze JSON for %q: %w", req.Audio, err)
	}
	return pb.VoiceAnalyzeResponse{Segments: []*pb.VoiceAnalysis{seg}}, nil
}

// analyzeJSON mirrors the document returned by voicedetect_capi_analyze_path_json:
//
//	{"age":42.0,
//	 "gender":{"label":"female","female":0.88,"male":0.12},
//	 "emotion":{"label":"neutral","scores":{"neutral":0.7, ...}}}
//
// gender is a mixed object (a "label" string plus per-class float scores), so
// it is decoded into raw messages and split in parseAnalyzeJSON.
type analyzeJSON struct {
	Age     float32                    `json:"age"`
	Gender  map[string]json.RawMessage `json:"gender"`
	Emotion struct {
		Label  string             `json:"label"`
		Scores map[string]float32 `json:"scores"`
	} `json:"emotion"`
}

// parseAnalyzeJSON maps the engine's analyze document onto a VoiceAnalysis.
// start/end stay 0: the model emits a single whole-utterance result, not
// time-bounded segments.
func parseAnalyzeJSON(doc string) (*pb.VoiceAnalysis, error) {
	var a analyzeJSON
	if err := json.Unmarshal([]byte(doc), &a); err != nil {
		return nil, err
	}

	seg := &pb.VoiceAnalysis{
		Age:             a.Age,
		DominantEmotion: a.Emotion.Label,
		Emotion:         a.Emotion.Scores,
	}

	if len(a.Gender) > 0 {
		gender := make(map[string]float32, len(a.Gender))
		for k, raw := range a.Gender {
			if k == "label" {
				_ = json.Unmarshal(raw, &seg.DominantGender)
				continue
			}
			var score float32
			if err := json.Unmarshal(raw, &score); err == nil {
				gender[k] = score
			}
		}
		seg.Gender = gender
	}

	return seg, nil
}

// lastErr wraps the C-API's per-ctx last-error buffer into a Go error.
func (v *VoiceDetect) lastErr(op, subject string) error {
	msg := strings.TrimSpace(CppLastError(v.ctxPtr))
	if msg == "" {
		msg = "no error detail"
	}
	return fmt.Errorf("voice-detect: %s failed for %q: %s", op, subject, msg)
}

// goStringFromCPtr copies a NUL-terminated C string into Go memory. cptr is a
// malloc'd buffer the caller owns; release it via CppFreeString after the copy.
//
// The uintptr->Pointer conversion trips vet's unsafeptr check, which can't tell
// a C heap pointer from Go-managed memory. Safe here: the GC neither tracks nor
// moves the buffer and we dereference it immediately to copy the bytes out.
func goStringFromCPtr(cptr uintptr) string {
	if cptr == 0 {
		return ""
	}
	p := unsafe.Pointer(cptr) //nolint:govet // C-owned malloc'd buffer, not Go-GC memory (see doc above)
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}
