package localai

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/vram"
)

type vramEstimateRequest struct {
	Model       string `json:"model"`                      // model name (must be installed)
	ContextSize uint32 `json:"context_size,omitempty"`     // context length to estimate for (default 8192)
	GPULayers   int    `json:"gpu_layers,omitempty"`       // number of layers to offload to GPU (0 = all)
	KVQuantBits int    `json:"kv_quant_bits,omitempty"`    // KV cache quantization bits (0 = fp16)
}

type vramEstimateResponse struct {
	vram.EstimateResult
	ContextNote     string `json:"context_note,omitempty"`      // note when context_size was defaulted
	ModelMaxContext uint64 `json:"model_max_context,omitempty"` // model's trained maximum context length
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

// VRAMEstimateEndpoint returns a handler that estimates VRAM usage for an
// installed model configuration. For uninstalled models (gallery URLs), use
// the gallery-level estimates in /api/models instead.
// @Summary Estimate VRAM usage for a model
// @Description Estimates VRAM based on model weight files, context size, and GPU layers
// @Tags config
// @Accept json
// @Produce json
// @Param request body vramEstimateRequest true "VRAM estimation parameters"
// @Success 200 {object} vramEstimateResponse "VRAM estimate"
// @Router /api/models/vram-estimate [post]
func VRAMEstimateEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req vramEstimateRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		}

		if req.Model == "" {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": "model name is required"})
		}

		modelConfig, exists := cl.GetModelConfig(req.Model)
		if !exists {
			return c.JSON(http.StatusNotFound, map[string]any{"error": "model configuration not found"})
		}

		modelsPath := appConfig.SystemState.Model.ModelsPath

		var files []vram.FileInput
		var firstGGUF string
		seen := make(map[string]bool)

		for _, f := range modelConfig.DownloadFiles {
			addWeightFile(string(f.URI), modelsPath, &files, &firstGGUF, seen)
		}
		if modelConfig.Model != "" {
			addWeightFile(modelConfig.Model, modelsPath, &files, &firstGGUF, seen)
		}
		if modelConfig.MMProj != "" {
			addWeightFile(modelConfig.MMProj, modelsPath, &files, &firstGGUF, seen)
		}

		if len(files) == 0 {
			return c.JSON(http.StatusOK, map[string]any{
				"message": "no weight files found for estimation",
			})
		}

		contextDefaulted := false
		opts := vram.EstimateOptions{
			ContextLength: req.ContextSize,
			GPULayers:     req.GPULayers,
			KVQuantBits:   req.KVQuantBits,
		}
		if opts.ContextLength == 0 {
			if modelConfig.ContextSize != nil {
				opts.ContextLength = uint32(*modelConfig.ContextSize)
			} else {
				opts.ContextLength = 8192
				contextDefaulted = true
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		result, err := vram.Estimate(ctx, files, opts, vram.DefaultCachedSizeResolver(), vram.DefaultCachedGGUFReader())
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		}

		resp := vramEstimateResponse{EstimateResult: result}

		// When context was defaulted to 8192, read the GGUF metadata to report
		// the model's trained maximum context length so callers know the estimate
		// may be conservative.
		if contextDefaulted && firstGGUF != "" {
			ggufMeta, err := vram.DefaultCachedGGUFReader().ReadMetadata(ctx, firstGGUF)
			if err == nil && ggufMeta != nil && ggufMeta.MaximumContextLength > 0 {
				resp.ModelMaxContext = ggufMeta.MaximumContextLength
				resp.ContextNote = fmt.Sprintf(
					"Estimate used default context_size=8192. The model's trained maximum context is %d; VRAM usage will be higher at larger context sizes.",
					ggufMeta.MaximumContextLength,
				)
			}
		}

		return c.JSON(http.StatusOK, resp)
	}
}
