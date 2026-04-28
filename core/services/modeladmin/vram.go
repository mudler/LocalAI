package modeladmin

import (
	"context"
	"fmt"
	"path/filepath"
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
}

// VRAMResponse embeds vram.EstimateResult and adds the context-defaulted
// note fields the HTTP endpoint surfaces.
type VRAMResponse struct {
	vram.EstimateResult
	ContextNote     string `json:"context_note,omitempty"`
	ModelMaxContext uint64 `json:"model_max_context,omitempty"`
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
	var firstGGUF string
	seen := make(map[string]bool)

	for _, f := range cfg.DownloadFiles {
		addWeightFile(string(f.URI), modelsPath, &files, &firstGGUF, seen)
	}
	if cfg.Model != "" {
		addWeightFile(cfg.Model, modelsPath, &files, &firstGGUF, seen)
	}
	if cfg.MMProj != "" {
		addWeightFile(cfg.MMProj, modelsPath, &files, &firstGGUF, seen)
	}

	if len(files) == 0 {
		// No weight files: the caller (HTTP or MCP) reports this as a
		// non-error empty estimate. Returning a typed nil here lets both
		// layers format the message consistently.
		return &VRAMResponse{ContextNote: "no weight files found for estimation"}, nil
	}

	contextDefaulted := false
	opts := vram.EstimateOptions{
		ContextLength: req.ContextSize,
		GPULayers:     req.GPULayers,
		KVQuantBits:   req.KVQuantBits,
	}
	if opts.ContextLength == 0 {
		if cfg.ContextSize != nil {
			opts.ContextLength = uint32(*cfg.ContextSize)
		} else {
			opts.ContextLength = 8192
			contextDefaulted = true
		}
	}

	subCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	result, err := vram.Estimate(subCtx, files, opts, vram.DefaultCachedSizeResolver(), vram.DefaultCachedGGUFReader())
	if err != nil {
		return nil, fmt.Errorf("vram estimate: %w", err)
	}

	resp := &VRAMResponse{EstimateResult: result}

	if contextDefaulted && firstGGUF != "" {
		ggufMeta, err := vram.DefaultCachedGGUFReader().ReadMetadata(subCtx, firstGGUF)
		if err == nil && ggufMeta != nil && ggufMeta.MaximumContextLength > 0 {
			resp.ModelMaxContext = ggufMeta.MaximumContextLength
			resp.ContextNote = fmt.Sprintf(
				"Estimate used default context_size=8192. The model's trained maximum context is %d; VRAM usage will be higher at larger context sizes.",
				ggufMeta.MaximumContextLength,
			)
		}
	}
	return resp, nil
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

// addWeightFile appends a resolved weight file to files and tracks the first GGUF.
func addWeightFile(uri, modelsPath string, files *[]vram.FileInput, firstGGUF *string, seen map[string]bool) {
	if !vram.IsWeightFile(uri) {
		return
	}
	resolved := resolveModelURI(uri, modelsPath)
	if seen[resolved] {
		return
	}
	seen[resolved] = true
	*files = append(*files, vram.FileInput{URI: resolved, Size: 0})
	if *firstGGUF == "" && vram.IsGGUF(uri) {
		*firstGGUF = resolved
	}
}
