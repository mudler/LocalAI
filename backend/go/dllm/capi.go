package main

// Typed Go wrappers over dllm.cpp's flat C-ABI (include/dllm_capi.h, ABI v1).
//
// Contract highlights the wrappers encode (see the header + src/capi.cpp):
//   - tokenize_json/generate return malloc'd char* the CALLER owns: bound as
//     uintptr, copied with goStringFromCPtr, released via dllm_capi_free_string.
//   - last_error returns a BORROWED pointer (valid until the next call on the
//     same ctx): bound as a plain string (purego copies), never freed, and only
//     read AFTER the failing call has returned - reading it while a generate is
//     in flight on the same ctx violates the per-ctx serialization contract.
//   - All entry points except dllm_capi_cancel must be externally serialized
//     per ctx (one ctx = one concurrent generate/tokenize). Cancel only flips
//     an atomic and may be called from any goroutine mid-generate.
//   - No C++ exception crosses the boundary; failures land in last_error.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// dllmABIVersion is the DLLM_CAPI_ABI_VERSION this binding was written
// against; main.go refuses to start against a libdllm.so reporting another.
const dllmABIVersion = 1

// purego-bound entry points from libdllm.so. Names match dllm_capi.h
// exactly; loadCAPI (main.go) fills these in at boot.
var (
	cppAbiVersion func() int32
	cppLoad       func(ggufPath, paramsJSON string) uintptr
	cppFree       func(ctx uintptr)
	cppLastError  func(ctx uintptr) string // borrowed pointer: purego copies, do NOT free
	cppFreeString func(s uintptr)
	// malloc'd char* returns, hence uintptr (see loadCAPI's doc comment).
	cppTokenizeJSON func(ctx uintptr, text string) uintptr
	cppGenerate     func(ctx uintptr, prompt, optsJSON string) uintptr
	// on_block/on_step are C function pointers produced by purego.NewCallback;
	// userData carries the streamCallStates registry key.
	cppGenerateStream func(ctx uintptr, prompt, optsJSON string, onBlock, onStep, userData uintptr) int32
	cppCancel         func(ctx uintptr)
)

// Dllm is the LocalAI gRPC backend over the dllm.cpp C-ABI. T1 ships only
// the binding scaffold; Load/PredictRich/PredictStreamRich (and the move to
// a dedicated dllm.go with the per-model worker goroutine) land in T4.
type Dllm struct {
	base.Base
}

// Load is not wired yet: the binding smoke drives the C functions directly.
func (d *Dllm) Load(opts *pb.ModelOptions) error {
	return errors.New("dllm: model loading not implemented yet (backend wiring lands in T4)")
}

// cAbiVersion returns the library's DLLM_CAPI_ABI_VERSION.
func cAbiVersion() int32 {
	return cppAbiVersion()
}

// cLoad opens the GGUF at path with the flat params JSON (e.g.
// {"n_gpu_layers":99}). Returns 0 on failure; per the header contract there
// is no ctx to carry the reason, the C side logs it to stderr (and
// cLastError(0) only yields the static NULL-ctx message).
func cLoad(path, paramsJSON string) uintptr {
	return cppLoad(path, paramsJSON)
}

// cFree releases a ctx; safe on 0 (delete nullptr).
func cFree(h uintptr) {
	cppFree(h)
}

// cLastError returns the ctx's last error message (or the static NULL-ctx
// message for h==0). The C pointer is borrowed and only valid until the next
// call on the same ctx; purego's string return copies it immediately, so the
// returned Go string is safe to keep. Must not be called while another call
// on the same ctx is in flight.
func cLastError(h uintptr) string {
	return cppLastError(h)
}

// lastErrorOr is cLastError with a fallback for the empty-message case, so
// wrapped errors never end in ": ".
func lastErrorOr(h uintptr, fallback string) string {
	if msg := cLastError(h); msg != "" {
		return msg
	}
	return fallback
}

// cTokenizeJSON tokenizes text (the C side prepends bos per vocab.add_bos)
// and returns the token ids as a JSON array string, e.g. "[2,18]".
func cTokenizeJSON(h uintptr, text string) (string, error) {
	ret := cppTokenizeJSON(h, text)
	if ret == 0 {
		return "", fmt.Errorf("dllm: tokenize failed: %s", lastErrorOr(h, "unknown error"))
	}
	out := goStringFromCPtr(ret)
	cppFreeString(ret)
	return out, nil
}

// cGenerate runs a blocking generation and returns the detokenized text.
// optsJSON must be a FLAT JSON object of scalars (use buildOptsJSON); the C
// parser rejects nested objects/arrays. NULL return -> last_error (read only
// after the call returned, per the serialization contract); a cancelled call
// surfaces as the "cancelled" message.
func cGenerate(h uintptr, prompt, optsJSON string) (string, error) {
	ret := cppGenerate(h, prompt, optsJSON)
	if ret == 0 {
		return "", fmt.Errorf("dllm: generate failed: %s", lastErrorOr(h, "unknown error"))
	}
	out := goStringFromCPtr(ret)
	cppFreeString(ret)
	return out, nil
}

// streamCallState carries the Go callbacks for one in-flight
// cGenerateStream call; the registry key travels through C as user_data.
// The map shape mirrors the whisper backend's streamCallStates: only one
// entry per ctx is ever live (the C-ABI is serialized per ctx), but keying
// by call survives multiple models/processes sharing the package.
type streamCallState struct {
	onBlock func(text string)
	onStep  func(step, total int, preview string)
}

var (
	streamCallStates sync.Map // uint64 -> *streamCallState
	streamCallSeq    atomic.Uint64

	// purego.NewCallback allocates a finite, never-released callback slot, so
	// the two trampolines are created exactly once and reused across calls.
	streamCbOnce sync.Once
	blockCbPtr   uintptr
	stepCbPtr    uintptr
)

// onBlockTrampoline is the Go side of dllm_block_cb. It runs on the C
// calling thread, mid-generate: keep it tiny and non-blocking (callers that
// bridge to goroutines must hand off via buffered channels). The text
// pointer is only valid for the duration of the invocation, so it is copied
// to a Go string immediately.
func onBlockTrampoline(text uintptr, userData uintptr) {
	v, ok := streamCallStates.Load(uint64(userData))
	if !ok {
		return // call already torn down
	}
	state := v.(*streamCallState)
	if state.onBlock != nil {
		state.onBlock(goStringFromCPtr(text))
	}
}

// onStepTrampoline is the Go side of dllm_step_cb; same threading and
// lifetime caveats as onBlockTrampoline.
func onStepTrampoline(step int32, totalSteps int32, canvasPreview uintptr, userData uintptr) {
	v, ok := streamCallStates.Load(uint64(userData))
	if !ok {
		return
	}
	state := v.(*streamCallState)
	if state.onStep != nil {
		state.onStep(int(step), int(totalSteps), goStringFromCPtr(canvasPreview))
	}
}

// cGenerateStream runs a generation with per-committed-block (onBlock) and
// per-denoising-step (onStep) callbacks; either may be nil. The callbacks
// run on the C thread (see the trampoline docs). Returns an error carrying
// last_error on failure; cancellation surfaces as the "cancelled" message.
func cGenerateStream(h uintptr, prompt, optsJSON string, onBlock func(text string), onStep func(step, total int, preview string)) error {
	streamCbOnce.Do(func() {
		blockCbPtr = purego.NewCallback(onBlockTrampoline)
		stepCbPtr = purego.NewCallback(onStepTrampoline)
	})

	id := streamCallSeq.Add(1)
	streamCallStates.Store(id, &streamCallState{onBlock: onBlock, onStep: onStep})
	defer streamCallStates.Delete(id)

	// Pass NULL for absent callbacks so the C side skips the per-block /
	// per-step detokenize work entirely.
	var blockPtr, stepPtr uintptr
	if onBlock != nil {
		blockPtr = blockCbPtr
	}
	if onStep != nil {
		stepPtr = stepCbPtr
	}

	if rc := cppGenerateStream(h, prompt, optsJSON, blockPtr, stepPtr, uintptr(id)); rc != 0 {
		return fmt.Errorf("dllm: generate_stream failed: %s", lastErrorOr(h, "unknown error"))
	}
	return nil
}

// cCancel requests cancellation of the in-flight generate on h. This is the
// ONE entry point safe to call from any goroutine while a generate runs (it
// only flips an atomic). Note the cancel-reset race from the header: each
// generate resets the flag on entry, so a watchdog should re-issue cancel if
// the call has not returned.
func cCancel(h uintptr) {
	cppCancel(h)
}

// buildOptsJSON renders generation options as the flat JSON object the
// C-ABI expects (known keys: n_predict, blocks, seed, eb_*, kv_cache). The
// C-side scanner only understands scalar number/string values and rejects
// nested objects/arrays loudly; bools are rejected here too because the
// scanner has no concept of them. Fail loud rather than let an option be
// silently misread.
func buildOptsJSON(opts map[string]any) (string, error) {
	if len(opts) == 0 {
		return "{}", nil
	}
	for k, v := range opts {
		switch v.(type) {
		case string,
			int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64,
			float32, float64,
			json.Number:
			// scalar: fine
		default:
			return "", fmt.Errorf("dllm: opts key %q has non-scalar value %T (the C-ABI only accepts flat number/string scalars)", k, v)
		}
	}
	b, err := json.Marshal(opts)
	if err != nil {
		return "", fmt.Errorf("dllm: marshal opts: %w", err)
	}
	return string(b), nil
}

// goStringFromCPtr copies a NUL-terminated C string into Go memory. cptr is
// the raw pointer returned by purego from the C-ABI (a malloc'd buffer the
// caller owns, or a callback argument only valid during the invocation);
// owning callers must free it via cppFreeString after the copy lands.
//
// The uintptr->unsafe.Pointer conversion below trips go vet's unsafeptr
// check, which can't distinguish a C-owned heap pointer from Go-managed
// memory. It is safe here: the pointer addresses C memory the Go GC neither
// tracks nor moves, and we dereference it immediately to copy the bytes out,
// the same pattern (and the same tolerated warning) as the parakeet-cpp and
// whisper backends.
func goStringFromCPtr(cptr uintptr) string {
	if cptr == 0 {
		return ""
	}
	p := unsafe.Pointer(cptr) //nolint:govet // C-owned buffer, not Go-GC memory (see doc above)
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}
