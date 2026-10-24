// SPDX-License-Identifier: MIT
package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	httpMiddleware "github.com/mudler/LocalAI/core/http/middleware"
)

// ModelExportsStatus is the body of the export status routes.
type ModelExportsStatus struct {
	// ModelExports is false only when this instance does not see the models
	// path of the add-on owner. It is the same value as the model_exports key
	// of GET /api/features.
	ModelExports bool `json:"model_exports"`
}

// ModelExportsStatusEndpoint answers whether this instance may write an
// exported or imported model. The add-on UIs read it to disable their export
// controls before a user hits the refusal of the export route.
//
// @Summary Report whether model exports are allowed
// @Description Returns model_exports=false when this instance does not share the models path of the add-on owner. The value is the same as the model_exports key of GET /api/features.
// @Tags fine-tuning, quantization
// @Produce json
// @Success 200 {object} localai.ModelExportsStatus
// @Failure 503 {object} map[string]any "add-ons are off or the owner is not reachable"
// @Router /api/fine-tuning/status [get]
func ModelExportsStatusEndpoint(gate httpMiddleware.ModelExportsGate) echo.HandlerFunc {
	return func(c echo.Context) error {
		allowed := gate == nil || gate.ModelExportsAllowed(c.Request().Context())
		return c.JSON(http.StatusOK, ModelExportsStatus{ModelExports: allowed})
	}
}

// ModelExportsStatusQuantizationEndpoint serves the same answer on the
// quantization prefix; it exists only to carry its own swagger route.
//
// @Summary Report whether model imports are allowed
// @Description Returns model_exports=false when this instance does not share the models path of the add-on owner. The value is the same as the model_exports key of GET /api/features.
// @Tags fine-tuning, quantization
// @Produce json
// @Success 200 {object} localai.ModelExportsStatus
// @Failure 503 {object} map[string]any "add-ons are off or the owner is not reachable"
// @Router /api/quantization/status [get]
func ModelExportsStatusQuantizationEndpoint(gate httpMiddleware.ModelExportsGate) echo.HandlerFunc {
	return ModelExportsStatusEndpoint(gate)
}
