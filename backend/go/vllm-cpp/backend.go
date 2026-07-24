package main

// LocalAI gRPC backend over the vllm.cpp C ABI.
//
// Predict maps to the blocking vllm_complete; PredictStream maps to
// vllm_complete_stream, whose per-delta C callback bridges into the gRPC
// stream channel. Concurrent calls are intentional: every completion entry
// point submits into the engine's shared AsyncLLM scheduler, so parallel
// LocalAI requests batch continuously inside the engine (the reason this
// backend embeds base.Base and not base.SingleThread).

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

type VllmCpp struct {
	base.Base

	engine uintptr
	opts   loadOptions
}

// Stream registry: the per-request bridge between the C token callback and
// the gRPC stream channel, keyed by an integer handle round-tripped through
// the C user_data pointer (never a Go pointer across the ABI). The host gRPC
// server drains the channel even after a client disconnect, so sends here
// cannot wedge the engine's delivery loop.
var (
	streamsMu   sync.Mutex
	streams     = map[uintptr]chan string{}
	streamNext  uintptr
	tokenCbOnce sync.Once
	tokenCbPtr  uintptr
)

// tokenCallback is the single C-shared callback for every stream; it
// dispatches on the user_data handle. Returning 0 aborts the in-flight
// request (vllm_token_callback contract).
func tokenCallback(delta uintptr, finished uintptr, userData uintptr) uintptr {
	streamsMu.Lock()
	results := streams[userData]
	streamsMu.Unlock()
	if results == nil {
		return 0 // unknown request: stop generation.
	}
	if text := goString(delta); text != "" {
		results <- text
	}
	return 1
}

func registerStream(results chan string) uintptr {
	streamsMu.Lock()
	defer streamsMu.Unlock()
	streamNext++
	streams[streamNext] = results
	return streamNext
}

func unregisterStream(h uintptr) {
	streamsMu.Lock()
	defer streamsMu.Unlock()
	delete(streams, h)
}

// validModelPath enforces the greedy-probe rule: when a model config has no
// explicit backend, the loader probes every backend with the model name, so
// Load must refuse anything vllm.cpp cannot serve (a GGUF file, or a HF-style
// directory with config.json + safetensors).
func validModelPath(model string) error {
	info, err := os.Stat(model)
	if err != nil {
		return fmt.Errorf("vllm-cpp: model path %q not found: %w", model, err)
	}
	if info.IsDir() {
		if _, err := os.Stat(filepath.Join(model, "config.json")); err != nil {
			return fmt.Errorf("vllm-cpp: model dir %q has no config.json", model)
		}
		return nil
	}
	if strings.EqualFold(filepath.Ext(model), ".gguf") {
		return nil
	}
	return fmt.Errorf("vllm-cpp: model %q is neither a .gguf file nor a config.json model dir", model)
}

func (v *VllmCpp) Load(opts *pb.ModelOptions) error {
	model := opts.ModelFile
	if model == "" {
		model = opts.ModelPath
	}
	if !filepath.IsAbs(model) && opts.ModelPath != "" {
		model = filepath.Join(opts.ModelPath, model)
	}
	if err := validModelPath(model); err != nil {
		return err
	}

	v.opts = parseOptions(opts)

	mp := defaultModelParams()
	if v.opts.blockSize > 0 {
		mp.BlockSize = v.opts.blockSize
	}
	if v.opts.numBlocks > 0 {
		mp.NumBlocks = v.opts.numBlocks
	}
	if opts.ContextSize > 0 {
		mp.MaxModelLen = opts.ContextSize
	}
	if v.opts.maxNumSeqs > 0 {
		mp.MaxNumSeqs = v.opts.maxNumSeqs
	}

	modelC := cString(model)
	mp.ModelPath = uintptr(unsafe.Pointer(&modelC[0])) // #nosec G103 -- borrowed by C for the load call only
	var toolParserC, reasoningParserC []byte
	if v.opts.toolParser != "" {
		toolParserC = cString(v.opts.toolParser)
		mp.ToolParser = uintptr(unsafe.Pointer(&toolParserC[0])) // #nosec G103 -- borrowed by C for the load call only
	}
	if v.opts.reasoningParser != "" {
		reasoningParserC = cString(v.opts.reasoningParser)
		mp.ReasoningParser = uintptr(unsafe.Pointer(&reasoningParserC[0])) // #nosec G103 -- borrowed by C for the load call only
	}

	xlog.Info("[vllm-cpp] Load", "model", model, "engine", vllmVersion(),
		"blockSize", mp.BlockSize, "numBlocks", mp.NumBlocks,
		"maxModelLen", mp.MaxModelLen, "maxNumSeqs", mp.MaxNumSeqs)

	var engine uintptr
	rc := vllmEngineLoad(unsafe.Pointer(&mp), unsafe.Pointer(&engine)) // #nosec G103 -- POD out-params
	runtime.KeepAlive(modelC)
	runtime.KeepAlive(toolParserC)
	runtime.KeepAlive(reasoningParserC)
	if rc != vllmOK {
		return fmt.Errorf("vllm-cpp: engine load failed: %s", vllmLastError())
	}
	v.engine = engine
	return nil
}

func (v *VllmCpp) Free() error {
	if v.engine != 0 {
		vllmEngineFree(v.engine)
		v.engine = 0
	}
	return nil
}

// samplingFromPredict lowers PredictOptions into the C sampling POD plus the
// backing buffers that must stay alive for the duration of the C call.
func samplingFromPredict(opts *pb.PredictOptions) (sp cSamplingParams, keep []any) {
	sp = defaultSamplingParams()
	sp.Temperature = opts.Temperature
	if opts.TopP > 0 {
		sp.TopP = opts.TopP
	}
	if opts.TopK > 0 {
		sp.TopK = opts.TopK
	}
	if opts.MinP > 0 {
		sp.MinP = opts.MinP
	}
	if opts.Tokens > 0 {
		sp.MaxTokens = opts.Tokens
	} else {
		sp.MaxTokens = 0 // unbounded; the engine caps at max_model_len.
	}
	if opts.Seed > 0 {
		sp.Seed = uint64(opts.Seed)
		sp.HasSeed = 1
	}
	sp.PresencePenalty = opts.PresencePenalty
	sp.FrequencyPenalty = opts.FrequencyPenalty
	if opts.Penalty > 0 {
		sp.RepetitionPenalty = opts.Penalty
	}
	if opts.IgnoreEOS {
		sp.IgnoreEOS = 1
	}
	if len(opts.StopPrompts) > 0 {
		ptrs, backing := cStringArray(opts.StopPrompts)
		sp.Stop = uintptr(unsafe.Pointer(&ptrs[0])) // #nosec G103 -- borrowed by C for the call only
		sp.NStop = int32(len(ptrs))
		keep = append(keep, ptrs, backing)
	}
	if opts.Grammar != "" {
		g := cString(opts.Grammar)
		sp.StructuredGrammar = uintptr(unsafe.Pointer(&g[0])) // #nosec G103 -- borrowed by C for the call only
		keep = append(keep, g)
	}
	return sp, keep
}

func (v *VllmCpp) Predict(opts *pb.PredictOptions) (string, error) {
	if v.engine == 0 {
		return "", fmt.Errorf("vllm-cpp: model not loaded")
	}
	sp, keep := samplingFromPredict(opts)
	var out cCompletion
	rc := vllmComplete(v.engine, opts.Prompt, unsafe.Pointer(&sp), unsafe.Pointer(&out)) // #nosec G103 -- POD in/out params
	runtime.KeepAlive(keep)
	if rc != vllmOK {
		return "", fmt.Errorf("vllm-cpp: completion failed: %s", vllmLastError())
	}
	text := goString(out.Text)
	vllmCompletionFree(unsafe.Pointer(&out)) // #nosec G103 -- frees out.Text
	return text, nil
}

func (v *VllmCpp) PredictStream(opts *pb.PredictOptions, results chan string) error {
	if v.engine == 0 {
		close(results)
		return fmt.Errorf("vllm-cpp: model not loaded")
	}
	tokenCbOnce.Do(func() {
		tokenCbPtr = purego.NewCallback(tokenCallback)
	})

	sp, keep := samplingFromPredict(opts)
	handle := registerStream(results)

	go func() {
		defer close(results)
		defer unregisterStream(handle)
		rc := vllmCompleteStream(v.engine, opts.Prompt, unsafe.Pointer(&sp), tokenCbPtr, handle) // #nosec G103 -- POD in-params
		runtime.KeepAlive(keep)
		if rc != vllmOK {
			xlog.Error("[vllm-cpp] stream failed", "error", vllmLastError())
		}
	}()
	return nil
}
