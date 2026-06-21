package main

// Go side of the ced backend: purego bindings over ced_capi.h plus the gRPC
// SoundDetection implementation.
//
// SKETCH: the pb.SoundDetection* types come from backend.proto (regenerate with
// `make protogen-go`). The C side is single-threaded per ctx, so we guard the
// engine with engineMu; LocalAI also serializes via base.SingleThread.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// purego-bound entry points from libced.so. Names match ced_capi.h exactly.
var (
	CppAbiVersion       func() int32
	CppLoad             func(ggufPath string) uintptr
	CppFree             func(ctx uintptr)
	CppLastError        func(ctx uintptr) string
	CppNumClasses       func(ctx uintptr) int32
	CppSampleRate       func(ctx uintptr) int32
	CppClassifyPathJSON func(ctx uintptr, wavPath string, topK int32) uintptr
	CppClassifyPcmJSON  func(ctx uintptr, pcm []float32, nSamples int32, sampleRate int32, topK int32) uintptr
	CppFreeString       func(s uintptr)
)

// cstr copies a malloc'd C string (returned as uintptr) into a Go string and
// frees the original via ced_capi_free_string. Empty/0 -> "".
func cstr(p uintptr) string {
	if p == 0 {
		return ""
	}
	defer CppFreeString(p)
	var b []byte
	for i := 0; ; i++ {
		ch := *(*byte)(unsafe.Pointer(p + uintptr(i)))
		if ch == 0 {
			break
		}
		b = append(b, ch)
	}
	return string(b)
}

// Ced is the gRPC backend. One loaded CED model per instance.
type Ced struct {
	base.Base
	ctxPtr   uintptr
	engineMu sync.Mutex
}

// Load resolves the GGUF and opens the C-API context.
func (c *Ced) Load(opts *pb.ModelOptions) error {
	if opts.ModelFile == "" {
		return errors.New("ced: ModelFile is required")
	}
	ctx := CppLoad(opts.ModelFile)
	if ctx == 0 {
		return fmt.Errorf("ced: ced_capi_load failed for %q: %s", opts.ModelFile, CppLastError(0))
	}
	c.ctxPtr = ctx
	return nil
}

// jsonTag mirrors the ced_capi JSON tag objects.
type jsonTag struct {
	Index int     `json:"index"`
	Score float32 `json:"score"`
	Label string  `json:"label"`
}

// SoundDetection classifies the clip at req.Src and returns scored AudioSet tags.
func (c *Ced) SoundDetection(ctx context.Context, req *pb.SoundDetectionRequest) (*pb.SoundDetectionResponse, error) {
	if c.ctxPtr == 0 {
		return nil, errors.New("ced: model not loaded")
	}
	if req.GetSrc() == "" {
		return nil, errors.New("ced: SoundDetectionRequest.src (audio path) is required")
	}
	topK := req.GetTopK()
	if topK <= 0 {
		topK = 10 // sensible default for a tagging response
	}

	c.engineMu.Lock()
	out := cstr(CppClassifyPathJSON(c.ctxPtr, req.GetSrc(), topK))
	lastErr := CppLastError(c.ctxPtr)
	c.engineMu.Unlock()

	if out == "" {
		return nil, fmt.Errorf("ced: classification failed: %s", lastErr)
	}
	var tags []jsonTag
	if err := json.Unmarshal([]byte(out), &tags); err != nil {
		return nil, fmt.Errorf("ced: bad classifier JSON: %w", err)
	}

	thr := req.GetThreshold()
	resp := &pb.SoundDetectionResponse{}
	for _, t := range tags {
		if t.Score < thr {
			continue
		}
		resp.Detections = append(resp.Detections, &pb.SoundClass{
			Label: t.Label, Score: t.Score, Index: int32(t.Index),
		})
	}
	sort.Slice(resp.Detections, func(i, j int) bool {
		return resp.Detections[i].Score > resp.Detections[j].Score
	})
	return resp, nil
}

func (c *Ced) Free() error {
	c.engineMu.Lock()
	defer c.engineMu.Unlock()
	if c.ctxPtr != 0 {
		CppFree(c.ctxPtr)
		c.ctxPtr = 0
	}
	return nil
}
