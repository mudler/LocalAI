// SPDX-License-Identifier: MIT
package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai/musicoutput"
	"gorm.io/gorm"
)

// RegisterMusicOutputRoutes requires the application's real private durable store.
// It performs no storage initialization and must run behind the global auth stack.
func RegisterMusicOutputRoutes(e *echo.Echo, store musicoutput.OutputStore, db *gorm.DB, appConfig *config.ApplicationConfig) {
	h := musicoutput.NewHandler(store, db, appConfig)
	e.GET("/v1/music/outputs/:output_id/audio", h)
	e.HEAD("/v1/music/outputs/:output_id/audio", h)
}
