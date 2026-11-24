package localai

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// LoadModelEndpoint pre-loads a model into memory by name — the inverse of
// /backend/shutdown. For a realtime pipeline model every configured sub-model
// (VAD, transcription, LLM, TTS, sound_detection, voice_recognition) is loaded; for a regular
// model its own backend is loaded. The call blocks until loading finishes so
// clients can drive warm-up explicitly and learn up front whether a model
// fails to load.
// @Summary Pre-load a model into memory
// @Description Loads the named model (or, for a realtime pipeline, all of its sub-models) into memory so subsequent requests pay no cold-start cost. The inverse of /backend/shutdown.
// @Tags monitoring
// @Accept json
// @Produce json
// @Param request body schema.ModelLoadRequest true "Model to load"
// @Success 200 {object} schema.ModelLoadResponse "Model loaded"
// @Failure 400 {object} schema.ModelLoadResponse "Missing model name"
// @Failure 500 {object} schema.ModelLoadResponse "Load failed (Loaded lists any sub-models that did load)"
// @Router /backend/load [post]
func LoadModelEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input := new(schema.ModelLoadRequest)
		if err := c.Bind(input); err != nil {
			return err
		}
		if input.Model == "" {
			return c.JSON(http.StatusBadRequest, schema.ModelLoadResponse{Message: "model is required"})
		}

		ctx := c.Request().Context()
		var lease model.Lease
		if input.Lease {
			ctx, lease = ml.BeginLease(ctx, leaseOwner(c), time.Hour)
		}
		loaded, err := backend.PreloadModelByName(ctx, cl, ml, appConfig, input.Model)
		if err != nil {
			xlog.Error("failed to pre-load model", "model", input.Model, "loaded", loaded, "error", err)
			return c.JSON(http.StatusInternalServerError, schema.ModelLoadResponse{
				LeaseID: lease.ID, LeaseExpires: leaseExpiry(lease),
				Loaded:  loaded,
				Message: "failed to load model: " + err.Error(),
			})
		}

		return c.JSON(http.StatusOK, schema.ModelLoadResponse{
			LeaseID: lease.ID, LeaseExpires: leaseExpiry(lease),
			Loaded:  loaded,
			Message: "model loaded",
		})
	}
}

func leaseOwner(c echo.Context) string {
	if u := auth.GetUser(c); u != nil {
		return u.ID
	}
	return "local-owner"
}

func leaseExpiry(l model.Lease) *time.Time {
	if l.ID == "" {
		return nil
	}
	return &l.Expires
}

// ReleaseModelLeaseEndpoint releases an advisory preload lease.
// @Summary Release a model preload lease
// @Description Best-effort cleanup of local models loaded by this lease. Busy, pinned, shared and distributed models are retained. Requires the same administrator who acquired the lease.
// @Tags monitoring
// @Produce json
// @Param id path string true "Lease ID"
// @Success 200 {array} model.LeaseRelease
// @Failure 404 {object} schema.ErrorResponse
// @Router /backend/leases/{id}/release [post]
func ReleaseModelLeaseEndpoint(ml *model.ModelLoader) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
		defer cancel()
		result, err := ml.ReleaseLease(ctx, leaseOwner(c), c.Param("id"), true)
		if errors.Is(err, model.ErrLeaseNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "model lease not found")
		}
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, result)
	}
}
