package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterUIRoutes(app *echo.Echo,
	cl *config.ModelConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	galleryService *services.GalleryService) {

	// Redirect all old UI routes to React SPA at /app
	redirectToApp := func(path string) echo.HandlerFunc {
		return func(c echo.Context) error {
			return c.Redirect(302, "/app"+path)
		}
	}

	redirectToAppWithParam := func(prefix string) echo.HandlerFunc {
		return func(c echo.Context) error {
			param := c.Param("model")
			if param == "" {
				param = c.Param("id")
			}
			if param != "" {
				return c.Redirect(302, "/app"+prefix+"/"+param)
			}
			return c.Redirect(302, "/app"+prefix)
		}
	}

	app.GET("/", redirectToApp(""))
	app.GET("/manage", redirectToApp("/manage"))

	if !appConfig.DisableRuntimeSettings {
		app.GET("/settings", redirectToApp("/settings"))
	}

	// Agent Jobs pages
	app.GET("/agent-jobs", redirectToApp("/agent-jobs"))
	app.GET("/agent-jobs/tasks/new", redirectToApp("/agent-jobs/tasks/new"))
	app.GET("/agent-jobs/tasks/:id/edit", func(c echo.Context) error {
		return c.Redirect(302, "/app/agent-jobs/tasks/"+c.Param("id")+"/edit")
	})
	app.GET("/agent-jobs/tasks/:id", func(c echo.Context) error {
		return c.Redirect(302, "/app/agent-jobs/tasks/"+c.Param("id"))
	})
	app.GET("/agent-jobs/jobs/:id", func(c echo.Context) error {
		return c.Redirect(302, "/app/agent-jobs/jobs/"+c.Param("id"))
	})

	// P2P
	app.GET("/p2p", redirectToApp("/p2p"))

	if !appConfig.DisableGalleryEndpoint {
		app.GET("/browse", redirectToApp("/browse"))
		app.GET("/browse/backends", redirectToApp("/backends"))
	}

	app.GET("/talk", redirectToApp("/talk"))
	app.GET("/chat", redirectToApp("/chat"))
	app.GET("/chat/:model", redirectToAppWithParam("/chat"))
	app.GET("/image", redirectToApp("/image"))
	app.GET("/image/:model", redirectToAppWithParam("/image"))
	app.GET("/tts", redirectToApp("/tts"))
	app.GET("/tts/:model", redirectToAppWithParam("/tts"))
	app.GET("/sound", redirectToApp("/sound"))
	app.GET("/sound/:model", redirectToAppWithParam("/sound"))
	app.GET("/video", redirectToApp("/video"))
	app.GET("/video/:model", redirectToAppWithParam("/video"))

	// Traces UI
	app.GET("/traces", redirectToApp("/traces"))

	app.GET("/api/traces", func(c echo.Context) error {
		return c.JSON(200, middleware.GetTraces())
	})

	app.POST("/api/traces/clear", func(c echo.Context) error {
		middleware.ClearTraces()
		return c.NoContent(204)
	})

	app.GET("/api/backend-traces", func(c echo.Context) error {
		return c.JSON(200, trace.GetBackendTraces())
	})

	app.POST("/api/backend-traces/clear", func(c echo.Context) error {
		trace.ClearBackendTraces()
		return c.NoContent(204)
	})

}
