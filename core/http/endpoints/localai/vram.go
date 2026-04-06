package localai

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/vram"
)

type vramEstimateRequest struct {
	Model        string   `json:"model"`                       // model name (must be installed)
	ContextSizes []uint32 `json:"context_sizes,omitempty"`     // context sizes to estimate (default [8192])
	GPULayers    int      `json:"gpu_layers,omitempty"`        // number of layers to offload to GPU (0 = all)
	KVQuantBits  int      `json:"kv_quant_bits,omitempty"`     // KV cache quantization bits (0 = fp16)
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

// VRAMEstimateEndpoint returns a handler that estimates VRAM usage for an
// installed model configuration at multiple context sizes.
// @Summary Estimate VRAM usage for a model
// @Description Estimates VRAM based on model weight files at multiple context sizes
// @Tags config
// @Accept json
// @Produce json
// @Param request body vramEstimateRequest true "VRAM estimation parameters"
// @Success 200 {object} vram.MultiContextEstimate "VRAM estimate"
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
		seen := make(map[string]bool)

		for _, f := range modelConfig.DownloadFiles {
			addWeightFile(string(f.URI), modelsPath, &files, seen)
		}
		if modelConfig.Model != "" {
			addWeightFile(modelConfig.Model, modelsPath, &files, seen)
		}
		if modelConfig.MMProj != "" {
			addWeightFile(modelConfig.MMProj, modelsPath, &files, seen)
		}

		if len(files) == 0 {
			return c.JSON(http.StatusOK, map[string]any{
				"message": "no weight files found for estimation",
			})
		}

		contextSizes := req.ContextSizes
		if len(contextSizes) == 0 {
			if modelConfig.ContextSize != nil {
				contextSizes = []uint32{uint32(*modelConfig.ContextSize)}
			} else {
				contextSizes = []uint32{8192}
			}
		}

		// Include model's configured context size alongside requested sizes
		if modelConfig.ContextSize != nil {
			modelCtx := uint32(*modelConfig.ContextSize)
			if !slices.Contains(contextSizes, modelCtx) {
				contextSizes = append(contextSizes, modelCtx)
			}
		}

		opts := vram.EstimateOptions{
			GPULayers:   req.GPULayers,
			KVQuantBits: req.KVQuantBits,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		result, err := vram.EstimateMultiContext(ctx, files, contextSizes, opts, vram.DefaultCachedSizeResolver(), vram.DefaultCachedGGUFReader())
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, result)
	}
}
