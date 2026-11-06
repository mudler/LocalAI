// SPDX-License-Identifier: MIT
// Package musicoutput serves completed music through manifest-scoped authorization.
package musicoutput

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/music"
	"github.com/mudler/LocalAI/core/services/music/outputstore"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// FeatureMusic must match the shared auth feature registry when routes are wired.
const FeatureMusic = auth.FeatureMusic

// OpenPrincipal is shared by publication and retrieval only when authentication
// is explicitly disabled. All reachable clients then share one trusted identity.
const OpenPrincipal = music.OpenPrincipal

// OutputStore accepts only the durable manifest returned by Lookup, never a
// client-supplied storage key. Open must recheck the manifest and its expiry.
type OutputStore interface {
	Lookup(context.Context, string) (outputstore.Manifest, error)
	Open(context.Context, outputstore.Manifest) (io.ReadCloser, error)
}

var _ OutputStore = (*outputstore.Store)(nil)

// NewHandler handles GET and HEAD at /v1/music/outputs/:output_id/audio.
// Register behind auth.Middleware, but not generic feature/model middleware:
// this route needs opaque 404 decisions using the manifest's canonical models.
// Dependencies are trusted application configuration, not request inputs.
func NewHandler(store OutputStore, db *gorm.DB, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set("Cache-Control", "private, no-store")
		c.Response().Header().Set("X-Content-Type-Options", "nosniff")
		if appConfig == nil {
			return deliveryError(c, http.StatusInternalServerError)
		}
		configured := db != nil || len(appConfig.ApiKeys) > 0
		user := auth.GetUser(c)
		if configured && (user == nil || user.ID == "") {
			c.Response().Header().Set("WWW-Authenticate", "Bearer")
			return deliveryError(c, http.StatusUnauthorized)
		}
		if store == nil || (reflect.ValueOf(store).Kind() == reflect.Pointer && reflect.ValueOf(store).IsNil()) {
			return deliveryError(c, http.StatusServiceUnavailable)
		}
		principal := OpenPrincipal
		admin := false
		var perm *auth.UserPermission
		if configured {
			principal = user.ID
			admin = user.Role == auth.RoleAdmin
			if !admin {
				// A regular user without an auth database cannot acquire permissions.
				if db == nil {
					return deliveryError(c, http.StatusNotFound)
				}
				var err error
				perm, err = auth.GetCachedUserPermissions(c, db, user.ID)
				if err != nil || perm == nil {
					return deliveryError(c, http.StatusNotFound)
				}
				enabled, explicit := perm.Permissions[FeatureMusic]
				if !explicit {
					// Match auth's default-ON API features without hiding a
					// failed permission read behind a permissive helper.
					for _, feature := range auth.APIFeatures {
						if feature == FeatureMusic {
							enabled = true
							break
						}
					}
				}
				if !enabled {
					return deliveryError(c, http.StatusNotFound)
				}
			}
		}
		m, err := store.Lookup(c.Request().Context(), c.Param("output_id"))
		if err != nil {
			return deliveryError(c, http.StatusNotFound)
		}
		if (!admin && m.Principal != principal) || !modelsAllowed(perm, m) {
			return deliveryError(c, http.StatusNotFound)
		}
		if m.ContentType != "audio/wav" || m.Size <= 0 || m.Size > outputstore.MaxWAVBytes || m.GenerationModel == "" {
			return deliveryError(c, http.StatusNotFound)
		}
		r, err := store.Open(c.Request().Context(), m)
		if err != nil {
			if errors.Is(err, outputstore.ErrDenied) {
				return deliveryError(c, http.StatusNotFound)
			}
			return deliveryError(c, http.StatusInternalServerError)
		}
		defer r.Close()
		c.Response().Header().Set("Content-Type", "audio/wav")
		c.Response().Header().Set("Content-Disposition", `attachment; filename="music.wav"`)
		c.Response().Header().Set("Content-Length", strconv.FormatInt(m.Size, 10))
		if c.Request().Method == http.MethodHead {
			return c.NoContent(http.StatusOK)
		}
		// Never hand a storage error to Echo's global error renderer after streaming
		// starts: it could append JSON or expose an internal error to audio bytes.
		c.Response().WriteHeader(http.StatusOK)
		written, err := io.CopyN(c.Response(), r, m.Size)
		if err != nil {
			// The response is committed; report a failed delivery without raw storage
			// errors, credentials, filenames, or an appended JSON error body.
			xlog.Warn("music output stream failed", "stage", "delivery", "reason", "stream_interrupted", "output_id", m.ID, "request_id", c.Response().Header().Get(echo.HeaderXRequestID), "bytes_written", written, "expected_bytes", m.Size)
		}
		return nil
	}
}

func modelsAllowed(perm *auth.UserPermission, m outputstore.Manifest) bool {
	if perm == nil || !perm.AllowedModels.Enabled {
		return true
	}
	allowed := func(model string) bool {
		for _, name := range perm.AllowedModels.Models {
			if model == name {
				return true
			}
		}
		return false
	}
	return allowed(m.GenerationModel) && (m.TranscriptionModel == "" || allowed(m.TranscriptionModel))
}

type stageError struct {
	*schema.APIError
	Stage  string `json:"stage"`
	Reason string `json:"reason"`
}
type errorResponse struct {
	Error     stageError `json:"error"`
	RequestID string     `json:"request_id,omitempty"`
}

func deliveryError(c echo.Context, status int) error {
	message, reason := "Music output unavailable", "output_unavailable"
	if status == http.StatusUnauthorized {
		message, reason = "Authentication required", "authentication_required"
	}
	if status == http.StatusServiceUnavailable {
		message, reason = "Music output delivery not ready", "delivery_not_ready"
	}
	if status == http.StatusInternalServerError {
		message, reason = "Music output delivery failed", "delivery_failed"
	}
	c.Response().Header().Set("Content-Type", echo.MIMEApplicationJSON)
	if c.Request().Method == http.MethodHead {
		return c.NoContent(status)
	}
	return c.JSON(status, errorResponse{Error: stageError{APIError: &schema.APIError{Code: status, Message: message, Type: "music_stage_error"}, Stage: "delivery", Reason: reason}, RequestID: c.Response().Header().Get(echo.HeaderXRequestID)})
}
