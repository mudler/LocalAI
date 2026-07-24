package main

// purego bindings for the vllm.cpp stable C ABI (include/vllm.h, ABI v2).
//
// The structs below are hand-mirrored PODs of the C declarations, with
// explicit padding so the Go layout matches the C layout on linux/darwin
// amd64+arm64. Struct-by-value entry points (the *_default helpers) are NOT
// bound - purego's struct-return support is platform-dependent - so the
// defaults are replicated here and guarded by the vllm_abi_version check at
// startup: a library whose ABI differs from what these mirrors were written
// against is refused before any request runs.

import (
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
)

// abiVersion is the VLLM_ABI_VERSION this file mirrors (vllm.h).
const abiVersion = 3

// vllm_status (vllm.h).
const (
	vllmOK = 0
)

// cModelParams mirrors vllm_model_params.
type cModelParams struct {
	ModelPath           uintptr // const char*
	TokenizerConfigPath uintptr // const char*
	BlockSize           int32
	NumBlocks           int32
	MaxModelLen         int32
	MaxNumSeqs          int32
}

// cSamplingParams mirrors vllm_sampling_params (ABI v2, structured fields
// included). Padding matches the C compiler's: the uint64 seed is 8-aligned,
// and each pointer following an int32 is 8-aligned.
type cSamplingParams struct {
	Temperature          float32
	TopP                 float32
	TopK                 int32
	MinP                 float32
	MaxTokens            int32
	_                    [4]byte
	Seed                 uint64
	HasSeed              int32
	PresencePenalty      float32
	FrequencyPenalty     float32
	RepetitionPenalty    float32
	MinTokens            int32
	IgnoreEOS            int32
	Stop                 uintptr // const char* const*
	NStop                int32
	_                    [4]byte
	StructuredJSON       uintptr // const char*
	StructuredRegex      uintptr // const char*
	StructuredChoice     uintptr // const char* const*
	NStructuredChoice    int32
	_                    [4]byte
	StructuredGrammar    uintptr // const char*
	StructuredJSONObject int32
	_                    [4]byte
}

// cCompletion mirrors vllm_completion.
type cCompletion struct {
	Text             uintptr // char*, caller-owned
	FinishReason     uintptr // const char*, library-owned
	PromptTokens     int32
	CompletionTokens int32
}

// defaultSamplingParams mirrors vllm_sampling_params_default().
func defaultSamplingParams() cSamplingParams {
	return cSamplingParams{
		Temperature:       1.0,
		TopP:              1.0,
		MaxTokens:         16,
		RepetitionPenalty: 1.0,
	}
}

// defaultModelParams mirrors vllm_model_params_default().
func defaultModelParams() cModelParams {
	return cModelParams{
		BlockSize:  32,
		NumBlocks:  256,
		MaxNumSeqs: 8,
	}
}

var (
	vllmEngineLoad     func(params, out unsafe.Pointer) int32
	vllmEngineFree     func(engine uintptr)
	vllmComplete       func(engine uintptr, prompt string, params, out unsafe.Pointer) int32
	vllmCompleteStream func(engine uintptr, prompt string, params unsafe.Pointer, cb uintptr, userData uintptr) int32
	vllmChat           func(engine uintptr, requestJSON string, out unsafe.Pointer) int32
	vllmChatStream     func(engine uintptr, requestJSON string, cb uintptr, userData uintptr) int32
	vllmStringFree     func(s uintptr)
	vllmCompletionFree func(out unsafe.Pointer)
	vllmLastError      func() string
	vllmVersion        func() string
	vllmABIVersion     func() int32
)

type libFunc struct {
	ptr  any
	name string
}

// registerLib dlopens libvllm and binds the C ABI, refusing an ABI-version
// mismatch (the struct mirrors above would be undefined behavior against a
// different layout).
func registerLib(libName string) error {
	lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("vllm-cpp: dlopen %s: %w", libName, err)
	}
	for _, lf := range []libFunc{
		{&vllmEngineLoad, "vllm_engine_load"},
		{&vllmEngineFree, "vllm_engine_free"},
		{&vllmComplete, "vllm_complete"},
		{&vllmCompleteStream, "vllm_complete_stream"},
		{&vllmChat, "vllm_chat"},
		{&vllmChatStream, "vllm_chat_stream"},
		{&vllmStringFree, "vllm_string_free"},
		{&vllmCompletionFree, "vllm_completion_free"},
		{&vllmLastError, "vllm_last_error"},
		{&vllmVersion, "vllm_version"},
		{&vllmABIVersion, "vllm_abi_version"},
	} {
		purego.RegisterLibFunc(lf.ptr, lib, lf.name)
	}
	if v := vllmABIVersion(); v != abiVersion {
		return fmt.Errorf("vllm-cpp: ABI mismatch: library reports v%d, backend built against v%d", v, abiVersion)
	}
	return nil
}

// cString returns a NUL-terminated byte slice for s. The backing array may be
// passed to C for the duration of a call (the ABI borrows and copies); keep it
// alive across the call with runtime.KeepAlive.
func cString(s string) []byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b
}

// cStringArray builds a NULL-free array of C-string pointers plus the backing
// buffers that must stay alive for the duration of the C call.
func cStringArray(ss []string) (ptrs []uintptr, backing [][]byte) {
	backing = make([][]byte, 0, len(ss))
	ptrs = make([]uintptr, 0, len(ss))
	for _, s := range ss {
		b := cString(s)
		backing = append(backing, b)
		ptrs = append(ptrs, uintptr(unsafe.Pointer(&b[0]))) // #nosec G103 -- borrowed by C for the call only
	}
	return ptrs, backing
}

// goString copies a NUL-terminated C string.
func goString(p uintptr) string {
	if p == 0 {
		return ""
	}
	//nolint:govet // C-owned pointer handed over by purego, valid for this call
	base := unsafe.Pointer(p) // #nosec G103 -- C-owned, copied out immediately
	n := 0
	for *(*byte)(unsafe.Add(base, n)) != 0 {
		n++
	}
	if n == 0 {
		return ""
	}
	return string(unsafe.Slice((*byte)(base), n))
}
