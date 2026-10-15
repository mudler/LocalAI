package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/slotcache"
)

// SlotCacheSaveEndpoint saves the prompt cache of a model slot to a file.
// @Summary Save a slot's prompt cache to a file.
// @Description Writes the prompt cache (KV state) of one slot of a llama-cpp model to a file in the directory set by the model's slot_save_path option. The file name must be a single file name. Admin only.
// @Tags inference
// @Param request body schema.SlotCacheRequest true "Request"
// @Success 200 {object} schema.SlotCacheResponse "Response"
// @Router /v1/slots/save [post]
func SlotCacheSaveEndpoint(ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return slotCacheEndpoint(pb.SlotCacheAction_SLOT_CACHE_ACTION_SAVE, ml, appConfig)
}

// SlotCacheRestoreEndpoint loads a saved prompt cache back into a model slot.
// @Summary Restore a slot's prompt cache from a file.
// @Description Reads a file written by the save call from the directory set by the model's slot_save_path option into one slot of a llama-cpp model. The file must come from the same model and quantisation. Admin only.
// @Tags inference
// @Param request body schema.SlotCacheRequest true "Request"
// @Success 200 {object} schema.SlotCacheResponse "Response"
// @Router /v1/slots/restore [post]
func SlotCacheRestoreEndpoint(ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return slotCacheEndpoint(pb.SlotCacheAction_SLOT_CACHE_ACTION_RESTORE, ml, appConfig)
}

func slotCacheEndpoint(action pb.SlotCacheAction, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.SlotCacheRequest)
		if !ok || input.Model == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "model is required")
		}
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.NewHTTPError(http.StatusBadRequest, "model not found")
		}

		if input.Slot < 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "slot must not be negative")
		}
		// Refuse bad names before any backend is loaded or called.
		if err := slotcache.ValidateFilename(input.Filename); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		dir := slotcache.DirFromOptions(cfg.Options)
		if dir == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "slot save and restore is disabled for this model: set the slot_save_path option")
		}
		if _, err := slotcache.Resolve(dir, input.Filename); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		resp, err := backend.ModelSlotCache(action, input.Slot, input.Filename, ml, *cfg, appConfig)
		if err != nil {
			return mapBackendError(err)
		}
		return c.JSON(http.StatusOK, resp)
	}
}
