package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/modeladmin"
)

type vramEstimateRequest struct {
	Model       string `json:"model"`                   // model name (must be installed)
	ContextSize uint32 `json:"context_size,omitempty"`  // context length to estimate for (default 8192)
	GPULayers   int    `json:"gpu_layers,omitempty"`    // number of layers to offload to GPU (0 = all)
	KVQuantBits int    `json:"kv_quant_bits,omitempty"` // KV cache quantization bits (0 = fp16)
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
// @Success 200 {object} modeladmin.VRAMResponse "VRAM estimate"
// @Router /api/models/vram-estimate [post]
func VRAMEstimateEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req vramEstimateRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		}
		resp, err := modeladmin.EstimateVRAM(c.Request().Context(), modeladmin.VRAMRequest{
			Model:       req.Model,
			ContextSize: req.ContextSize,
			GPULayers:   req.GPULayers,
			KVQuantBits: req.KVQuantBits,
		}, cl, appConfig.SystemState)
		if err != nil {
			return c.JSON(httpStatusForModelAdminError(err), map[string]any{"error": err.Error()})
		}
		// Backwards compat: when there are no weight files, the previous
		// handler returned {"message": "..."} rather than a typed response.
		if resp.ContextNote == "no weight files found for estimation" && resp.EstimateResult.SizeBytes == 0 {
			return c.JSON(http.StatusOK, map[string]any{"message": resp.ContextNote})
		}
		return c.JSON(http.StatusOK, resp)
	}
}
