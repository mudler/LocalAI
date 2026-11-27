package modeladmin

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/vram"
)

// VRAMRequest is the input for EstimateVRAM. JSON tags let the HTTP
// handler bind directly into this type instead of carrying a parallel
// private struct.
type VRAMRequest struct {
	Model       string `json:"model"`
	ContextSize uint32 `json:"context_size,omitempty"`
	GPULayers   int    `json:"gpu_layers,omitempty"`
	KVQuantBits int    `json:"kv_quant_bits,omitempty"`
	// CacheTypeK and CacheTypeV are llama.cpp KV cache types (f16, q8_0,
	// q4_0, ...). Empty uses the model's config, then f16. They size the KV
	// cache of the runtime breakdown, and the KV term of the total when
	// KVQuantBits is not set.
	CacheTypeK string `json:"cache_type_k,omitempty"`
	CacheTypeV string `json:"cache_type_v,omitempty"`
	// Batch is the logical batch (parameters.batch). Absent uses the model's
	// config; 0 uses LocalAI's default for this host.
	Batch *int `json:"batch,omitempty"`
	// UBatch is the physical batch (the ubatch option), capped by the batch.
	// Absent uses the model's config; 0 means the same as the batch.
	UBatch *int `json:"ubatch,omitempty"`
}

// VRAMResponse embeds vram.EstimateResult and adds the context-defaulted
// note fields the HTTP endpoint surfaces.
type VRAMResponse struct {
	vram.EstimateResult
	ContextNote     string `json:"context_note,omitempty"`
	ModelMaxContext uint64 `json:"model_max_context,omitempty"`
	// ExpertCount is the number of experts of a mixture-of-experts model, read
	// from the GGUF metadata. Absent for a dense model or when it is unknown.
	ExpertCount uint32 `json:"expert_count,omitempty"`
	// BlockCount is the number of layers of the model, read from the GGUF
	// metadata. Absent when it is unknown.
	BlockCount uint32 `json:"block_count,omitempty"`
	// Architecture is general.architecture of a local GGUF model file, and
	// MTPLayers the layers of its embedded multi-token prediction head (absent
	// without one). VocabSize is the number of tokens of its vocabulary. The
	// model editor reads them to offer MTP and to say whether a draft model
	// fits. Absent when unknown.
	Architecture string `json:"architecture,omitempty"`
	MTPLayers    uint32 `json:"mtp_layers,omitempty"`
	VocabSize    uint64 `json:"vocab_size,omitempty"`
}

// EstimateVRAM computes a VRAM estimate for an installed model. It mirrors
// VRAMEstimateEndpoint without any HTTP coupling.
func EstimateVRAM(ctx context.Context, req VRAMRequest, cl *config.ModelConfigLoader, sysState *system.SystemState) (*VRAMResponse, error) {
	if req.Model == "" {
		return nil, ErrNameRequired
	}
	cfg, exists := cl.GetModelConfig(req.Model)
	if !exists {
		return nil, ErrNotFound
	}
	modelsPath := sysState.Model.ModelsPath

	var files []vram.FileInput
	seen := make(map[string]bool)

	for _, f := range cfg.DownloadFiles {
		addWeightFile(string(f.URI), modelsPath, &files, seen)
	}
	if cfg.Model != "" {
		addWeightFile(cfg.Model, modelsPath, &files, seen)
	}
	if cfg.MMProj != "" {
		addWeightFile(cfg.MMProj, modelsPath, &files, seen)
	}

	if len(files) == 0 {
		// No weight files: the caller (HTTP or MCP) reports this as a
		// non-error empty estimate. Returning a typed nil here lets both
		// layers format the message consistently.
		return &VRAMResponse{ContextNote: "no weight files found for estimation"}, nil
	}

	contextDefaulted := false
	ctxLen := req.ContextSize
	if ctxLen == 0 {
		if cfg.ContextSize != nil {
			ctxLen = uint32(*cfg.ContextSize)
		} else {
			// The window an unset context_size loads with, which depends on
			// whether the model serves chats (see AutoContextTarget).
			ctxLen = uint32(cfg.AutoContextTarget(""))
			contextDefaulted = true
		}
	}

	runtimeOpts, err := runtimeOptions(req, cfg, ctxLen)
	if err != nil {
		return nil, err
	}
	opts := vram.EstimateOptions{
		GPULayers:   req.GPULayers,
		KVQuantBits: req.KVQuantBits,
	}
	// With no kv_quant_bits, the total sizes the KV cache from the same cache
	// types as the runtime breakdown, so the two agree.
	if opts.KVQuantBits == 0 {
		opts.CacheTypeK, opts.CacheTypeV = runtimeOpts.CacheTypeK, runtimeOpts.CacheTypeV
	}

	subCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	multi, err := vram.EstimateMultiContext(subCtx, files, []uint32{ctxLen}, opts, vram.DefaultCachedSizeResolver(), vram.DefaultCachedGGUFReader())
	if err != nil {
		return nil, fmt.Errorf("vram estimate: %w", err)
	}

	// A model never loads with more than it was trained for when the window
	// is automatic, so the estimate must not assume more either.
	if contextDefaulted && multi.ModelMaxContext > 0 && uint64(ctxLen) > multi.ModelMaxContext {
		ctxLen = uint32(multi.ModelMaxContext)
		runtimeOpts.ContextSize = ctxLen
		if multi, err = vram.EstimateMultiContext(subCtx, files, []uint32{ctxLen}, opts, vram.DefaultCachedSizeResolver(), vram.DefaultCachedGGUFReader()); err != nil {
			return nil, fmt.Errorf("vram estimate: %w", err)
		}
	}

	at := multi.Estimates[fmt.Sprint(ctxLen)]
	breakdown := vram.EstimateRuntime(multi.Meta, runtimeOpts)
	resp := &VRAMResponse{
		EstimateResult: vram.EstimateResult{
			SizeBytes:     multi.SizeBytes,
			SizeDisplay:   multi.SizeDisplay,
			ContextLength: at.ContextLength,
			VRAMBytes:     at.VRAMBytes,
			VRAMDisplay:   at.VRAMDisplay,
			Runtime:       &breakdown,
		},
		ModelMaxContext: multi.ModelMaxContext,
		ExpertCount:     multi.ExpertCount,
		BlockCount:      multi.BlockCount,
	}

	if multi.Meta != nil {
		resp.VocabSize = multi.Meta.VocabSize
	}
	if cfg.Model != "" && !strings.Contains(cfg.Model, "://") && strings.HasSuffix(strings.ToLower(cfg.Model), ".gguf") {
		if info, err := config.ReadGGUFModelInfo(filepath.Join(modelsPath, cfg.Model)); err == nil {
			resp.Architecture, resp.MTPLayers = info.Architecture, info.MTPLayers
		}
	}

	if contextDefaulted && multi.ModelMaxContext > 0 {
		resp.ContextNote = fmt.Sprintf(
			"Estimate used default context_size=%d. The model's trained maximum context is %d; VRAM usage will be higher at larger context sizes.",
			ctxLen, multi.ModelMaxContext,
		)
	}
	return resp, nil
}

// runtimeOptions resolves the KV cache types and the physical batch of an
// estimate: the request, then the model's config, then the defaults
// llama.cpp and LocalAI apply.
func runtimeOptions(req VRAMRequest, cfg config.ModelConfig, ctxLen uint32) (vram.RuntimeOptions, error) {
	out := vram.RuntimeOptions{ContextSize: ctxLen}
	pick := func(asked, configured string) (string, error) {
		t := asked
		if t == "" {
			t = configured
		}
		if t == "" {
			return vram.DefaultKVCacheType, nil
		}
		if _, ok := vram.KVCacheTypeBits(t); !ok {
			return "", fmt.Errorf("%w: unknown KV cache type %q", ErrInvalidEstimate, t)
		}
		return t, nil
	}
	var err error
	if out.CacheTypeK, err = pick(req.CacheTypeK, cfg.CacheTypeK); err != nil {
		return out, err
	}
	if out.CacheTypeV, err = pick(req.CacheTypeV, cfg.CacheTypeV); err != nil {
		return out, err
	}

	batch := cfg.Batch
	if req.Batch != nil {
		batch = *req.Batch
	}
	if batch < 0 {
		return out, fmt.Errorf("%w: the batch must not be negative", ErrInvalidEstimate)
	}
	if batch == 0 {
		batch = defaultBatch(int(ctxLen))
	}
	ubatch := configuredUBatch(cfg.Options)
	if req.UBatch != nil {
		ubatch = *req.UBatch
	}
	if ubatch < 0 {
		return out, fmt.Errorf("%w: the ubatch must not be negative", ErrInvalidEstimate)
	}
	// llama.cpp never runs a physical batch larger than the logical one.
	if ubatch == 0 || ubatch > batch {
		ubatch = batch
	}
	out.UBatch = uint32(ubatch)
	return out, nil
}

// defaultBatch is the physical batch LocalAI gives a model with no batch set
// on this host (see config.ApplyHardwareDefaults).
func defaultBatch(ctx int) int {
	if config.HardwareDefaultsDisabled() {
		return config.DefaultPhysicalBatch
	}
	return config.PhysicalBatchForContext(config.LocalGPU(), ctx)
}

// configuredUBatch reads the ubatch option of a model (either spelling the
// llama.cpp backend reads), or 0 when it is not set or not a number.
func configuredUBatch(options []string) int {
	for _, o := range options {
		name, value, ok := strings.Cut(o, ":")
		if !ok || (name != "ubatch" && name != "n_ubatch") {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// resolveModelURI converts a relative model path to a file:// URI so the
// size resolver can stat it on disk. URIs that already have a scheme are
// returned unchanged.
func resolveModelURI(uri, modelsPath string) string {
	if strings.Contains(uri, "://") {
		return uri
	}
	return "file://" + filepath.Join(modelsPath, uri)
}

// addWeightFile appends a resolved weight file to files.
func addWeightFile(uri, modelsPath string, files *[]vram.FileInput, seen map[string]bool) {
	if !vram.IsWeightFile(uri) {
		return
	}
	resolved := resolveModelURI(uri, modelsPath)
	if seen[resolved] {
		return
	}
	seen[resolved] = true
	*files = append(*files, vram.FileInput{URI: resolved, Size: 0})
}
