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
	"context"
	"encoding/json"
	"fmt"
	"math"
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
	// videoEngine is the MiniMax-H3 handle (ABI v12). It is deliberately a
	// SECOND handle, not a mode of the first: H3 is a checkpoint set rather
	// than a model directory, and vllm.cpp has the two loaders refuse each
	// other's checkpoints. Exactly one of the two is ever non-zero.
	videoEngine uintptr
	opts        loadOptions
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
		// vllm.cpp accepts three config filenames: config.json (standard),
		// cua-s1-forms.json (cua-s1-forms scoring model), and
		// rl_agent_config.json (laya decision model). The engine's
		// model_loader.cpp checks them in that order.
		for _, cfg := range []string{"config.json", "cua-s1-forms.json", "rl_agent_config.json"} {
			if _, err := os.Stat(filepath.Join(model, cfg)); err == nil {
				return nil
			}
		}
		return fmt.Errorf("vllm-cpp: model dir %q has no config.json, cua-s1-forms.json, or rl_agent_config.json", model)
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

	// MiniMax-H3 is a checkpoint SET behind its own engine handle, so the
	// branch is taken before any text-engine knob is resolved. The two loaders
	// refuse each other's checkpoints, which is why this is decided from the
	// config rather than probed.
	if v.opts.video.engaged() {
		return v.loadVideo(opts, model)
	}

	// A DFlash draft is a second checkpoint the engine opens by path, and the
	// engine never downloads one. Resolve it against LocalAI's models directory
	// now so a repo-id spelling works, and so a missing draft fails here with an
	// actionable message rather than as an HF-cache miss inside the load.
	resolvedSpec, err := resolveDraftModelPath(v.opts.speculativeConfig, opts.ModelPath)
	if err != nil {
		return err
	}
	v.opts.speculativeConfig = resolvedSpec

	mp := defaultModelParams()
	if v.opts.blockSize > 0 {
		mp.BlockSize = v.opts.blockSize
	}
	if v.opts.numBlocks > 0 {
		mp.NumBlocks = v.opts.numBlocks
	}
	// Sequence-length precedence, narrowest source last: context_size is the
	// generic LocalAI knob every backend honours, max_model_len is the
	// vLLM-specific one, and engine_args.max_model_len is the explicit
	// vllm-cpp override.
	if opts.ContextSize > 0 {
		mp.MaxModelLen = opts.ContextSize
	}
	if opts.MaxModelLen > 0 {
		mp.MaxModelLen = opts.MaxModelLen
	}
	if v.opts.maxModelLen > 0 {
		mp.MaxModelLen = v.opts.maxModelLen
	}
	if v.opts.maxNumSeqs > 0 {
		mp.MaxNumSeqs = v.opts.maxNumSeqs
	}
	if v.opts.maxNumBatchedTokens > 0 {
		mp.MaxNumBatchedTokens = v.opts.maxNumBatchedTokens
	}
	mp.EnablePrefixCaching = v.opts.enablePrefixCaching
	mp.EnableJumpForward = v.opts.enableJumpForward

	// Every string below is borrowed by C for the duration of the load call
	// only (the library copies what it keeps), so the backing slices just have
	// to outlive vllmEngineLoad - hence the single KeepAlive after it.
	modelC := cString(model)
	mp.ModelPath = uintptr(unsafe.Pointer(&modelC[0])) // #nosec G103 -- borrowed by C for the load call only
	keep := [][]byte{modelC}
	setStr := func(dst *uintptr, s string) {
		if s == "" {
			return
		}
		b := cString(s)
		keep = append(keep, b)
		*dst = uintptr(unsafe.Pointer(&b[0])) // #nosec G103 -- borrowed by C for the load call only
	}
	setStr(&mp.ToolParser, v.opts.toolParser)
	setStr(&mp.ReasoningParser, v.opts.reasoningParser)
	setStr(&mp.SpeculativeConfig, v.opts.speculativeConfig)
	setStr(&mp.KVTransferConfig, v.opts.kvTransferConfig)
	setStr(&mp.SchedulingPolicy, v.opts.schedulingPolicy)
	setStr(&mp.TokenizerConfigPath, v.opts.tokenizerConfigPath)

	xlog.Info("[vllm-cpp] Load", "model", model, "engine", vllmVersion(),
		"blockSize", mp.BlockSize, "numBlocks", mp.NumBlocks,
		"maxModelLen", mp.MaxModelLen, "maxNumSeqs", mp.MaxNumSeqs,
		"maxNumBatchedTokens", mp.MaxNumBatchedTokens,
		"prefixCaching", triStateName(mp.EnablePrefixCaching),
		"jumpForward", triStateName(mp.EnableJumpForward),
		"schedulingPolicy", v.opts.schedulingPolicy,
		"speculativeConfig", v.opts.speculativeConfig,
		"kvTransferConfig", v.opts.kvTransferConfig)

	var engine uintptr
	rc := vllmEngineLoad(unsafe.Pointer(&mp), unsafe.Pointer(&engine)) // #nosec G103 -- POD out-params
	runtime.KeepAlive(keep)
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
	if v.videoEngine != 0 {
		vllmVideoEngineFree(v.videoEngine)
		v.videoEngine = 0
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

// defaultNerLabels is the general-purpose entity type set used when the model
// config does not supply ner_labels. These cover the most common NER use cases
// and match the categories the GLiNER2.5 model card demonstrates.
var defaultNerLabels = []string{
	"person", "organization", "location",
	"date", "time", "money", "quantity",
}

// TokenClassify runs zero-shot NER on the loaded GLiNER2.5 engine via the
// vllm_gliner_ner C ABI (ABI v27). The engine refuses non-BoundaryExtractor
// architectures, so a model loaded for chat or embeddings returns an error
// here rather than silent garbage.
func (v *VllmCpp) TokenClassify(_ context.Context, in *pb.TokenClassifyRequest) (*pb.TokenClassifyResponse, error) {
	if v.engine == 0 {
		return nil, fmt.Errorf("vllm-cpp: model not loaded")
	}
	labels := v.opts.nerLabels
	if len(in.Labels) > 0 {
		labels = in.Labels
	}
	if len(labels) == 0 {
		labels = defaultNerLabels
	}
	threshold := v.opts.nerThreshold
	if in.Threshold > 0 {
		threshold = in.Threshold
	}
	maxWidth := v.opts.nerMaxWidth

	labelPtrs, labelBacking := cStringArray(labels)
	if len(labelPtrs) == 0 {
		return nil, fmt.Errorf("vllm-cpp: no NER labels configured")
	}
	labelsPtr := uintptr(unsafe.Pointer(&labelPtrs[0])) // #nosec G103 -- borrowed by C for the call only

	var out cNerResult
	rc := vllmGlinerNer(v.engine, in.Text, labelsPtr, int32(len(labelPtrs)), threshold, maxWidth, unsafe.Pointer(&out)) // #nosec G103 -- POD in/out params
	runtime.KeepAlive(labelBacking)
	if rc != vllmOK {
		return nil, fmt.Errorf("vllm-cpp: NER failed: %s", vllmLastError())
	}
	defer vllmNerResultFree(unsafe.Pointer(&out)) // #nosec G103 -- frees C-owned members

	entities := make([]*pb.TokenClassifyEntity, 0, out.nEntities)
	if out.nEntities > 0 && out.entities != 0 {
		//nolint:govet // C-owned array, valid for this call before vllmNerResultFree
		cents := unsafe.Slice((*cNerEntity)(unsafe.Pointer(out.entities)), int(out.nEntities)) // #nosec G103 -- C-owned, copied out immediately
		for i := range cents {
			e := &cents[i]
			entities = append(entities, &pb.TokenClassifyEntity{
				EntityGroup: goString(e.label),
				Start:       e.charStart,
				End:         e.charEnd,
				Score:       e.confidence,
				Text:        goString(e.text),
			})
		}
	}
	return &pb.TokenClassifyResponse{Entities: entities}, nil
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

// Score runs the cua-s1-forms scoring pipeline via the vllm_score C ABI
// (ABI v28). The engine refuses non-CuaS1Forms architectures, so a chat or
// embedding model returns an error here.
func (v *VllmCpp) Score(_ context.Context, in *pb.ScoreRequest) (*pb.ScoreResponse, error) {
	if v.engine == 0 {
		return nil, fmt.Errorf("vllm-cpp: model not loaded")
	}
	if len(in.Candidates) == 0 {
		return nil, fmt.Errorf("vllm-cpp: score requires at least one candidate")
	}
	reqJSON, err := json.Marshal(map[string]any{
		"context": in.Prompt,
		"options": in.Candidates,
	})
	if err != nil {
		return nil, fmt.Errorf("vllm-cpp: score request encode: %w", err)
	}
	var out uintptr
	rc := vllmScore(v.engine, string(reqJSON), unsafe.Pointer(&out)) // #nosec G103 -- char** out-param
	if rc != vllmOK {
		return nil, fmt.Errorf("vllm-cpp: score failed: %s", vllmLastError())
	}
	payload := goString(out)
	vllmScoreFree(out)

	var resp struct {
		Probabilities []float64 `json:"probabilities"`
	}
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		return nil, fmt.Errorf("vllm-cpp: unparseable score response: %w", err)
	}
	candidates := make([]*pb.CandidateScore, len(in.Candidates))
	for i, c := range in.Candidates {
		var p float64
		if i < len(resp.Probabilities) {
			p = resp.Probabilities[i]
		}
		lp := math.Log(p) // probability → log-prob
		if p <= 0 {
			lp = -999.0 // JSON cannot encode -Inf; use a large negative sentinel
		}
		nTok := max((len(c)+3)/4, 1)
		candidates[i] = &pb.CandidateScore{
			LogProb:                 lp,
			NumTokens:               int32(nTok),
			LengthNormalizedLogProb: lp / float64(nTok),
		}
	}
	return &pb.ScoreResponse{Candidates: candidates}, nil
}

// SystemOne runs the kev/laya decision pipeline via the vllm_systemone C
// ABI (ABI v28). The engine refuses non-KevModel/LayaModel architectures.
// The request_json is forwarded as-is; the response_json is returned as-is.
func (v *VllmCpp) SystemOne(_ context.Context, in *pb.SystemOneRequest) (*pb.SystemOneResponse, error) {
	if v.engine == 0 {
		return nil, fmt.Errorf("vllm-cpp: model not loaded")
	}
	if in.RequestJson == "" {
		return nil, fmt.Errorf("vllm-cpp: systemone requires a request body")
	}
	var out uintptr
	rc := vllmSystemone(v.engine, in.RequestJson, unsafe.Pointer(&out)) // #nosec G103 -- char** out-param
	if rc != vllmOK {
		return nil, fmt.Errorf("vllm-cpp: systemone failed: %s", vllmLastError())
	}
	payload := goString(out)
	vllmSystemoneFree(out)
	return &pb.SystemOneResponse{ResponseJson: payload}, nil
}
