package localai

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/slotcache"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SlotCacheSaveEndpoint saves the prompt cache of a model slot.
// @Summary Save a slot's prompt cache.
// @Description Writes the prompt cache (KV state) of one slot of a llama-cpp model under the directory set by the model's slot_save_path option, and returns an ID for it. The server issues the ID; the client cannot choose a name or a path. The server keeps at most 32 saves per model and deletes the oldest one beyond that. Admin only.
// @Tags inference
// @Param request body schema.SlotCacheRequest true "Request"
// @Success 200 {object} schema.SlotCacheResponse "Response"
// @Router /v1/slots/save [post]
func SlotCacheSaveEndpoint(ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return slotCacheEndpoint(pb.SlotCacheAction_SLOT_CACHE_ACTION_SAVE, ml, appConfig)
}

// SlotCacheRestoreEndpoint loads a saved prompt cache back into a model slot.
// @Summary Restore a slot's prompt cache by ID.
// @Description Loads the prompt cache that a save call returned an ID for into one slot of a llama-cpp model. The ID must have been issued for this model. The state is only as fresh as the save. Admin only.
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
		// Refuse bad input before any backend is loaded or called.
		if action == pb.SlotCacheAction_SLOT_CACHE_ACTION_SAVE {
			if input.ID != "" {
				return echo.NewHTTPError(http.StatusBadRequest, "id must not be set on save: the server issues it")
			}
		} else if err := slotcache.ValidateID(input.ID); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		if slotcache.DirFromOptions(cfg.Options) == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "slot save and restore is disabled for this model: set the slot_save_path option")
		}

		resp, err := backend.ModelSlotCache(action, input.Slot, input.ID, ml, *cfg, appConfig)
		if err != nil {
			return slotCacheError(err)
		}
		return c.JSON(http.StatusOK, resp)
	}
}

// errSlotNotFound is the fixed text for an ID the backend does not know.
const errSlotNotFound = "no saved state with this id for this model"

// slotCacheError turns a backend error into an HTTP error. A backend without
// slot cache support gets 501. NOT_FOUND gets 404 with a fixed text, so the
// response says nothing about the disk; a model identity mismatch is not an
// unknown ID and keeps the existing mapping. Everything else follows
// mapBackendError.
func slotCacheError(err error) error {
	if errors.Is(err, backend.ErrSlotCacheUnsupported) {
		return echo.NewHTTPError(http.StatusNotImplemented, backend.ErrSlotCacheUnsupported.Error())
	}
	if st, ok := status.FromError(err); ok {
		switch {
		case st.Code() == codes.NotFound && !grpcerrors.IsModelMismatch(err):
			return echo.NewHTTPError(http.StatusNotFound, errSlotNotFound)
		case st.Code() == codes.ResourceExhausted:
			return echo.NewHTTPError(http.StatusInsufficientStorage, st.Message())
		}
	}
	return mapBackendError(err)
}
