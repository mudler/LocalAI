package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
)

// ModelStorageEndpoint reports the on-disk footprint of the installed
// model configs: per-model sizes with the shared portion split out, the
// file-to-models relation, and references whose files are missing.
// @Summary Report model storage usage on disk
// @Description Per-model disk usage, shared-file relations and missing references under the models path
// @Tags models
// @Produce json
// @Success 200 {object} gallery.StorageIndex "storage report"
// @Router /api/models/storage [get]
func ModelStorageEndpoint(appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		idx, err := gallery.BuildStorageIndex(appConfig.SystemState)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, idx)
	}
}
