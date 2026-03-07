package http

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	httpMiddleware "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/http/routes"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"

	"github.com/mudler/xlog"
)

// Embed a directory
//
//go:embed static/*
var embedDirStatic embed.FS

// Embed React UI build output
//
//go:embed react-ui/dist/*
var reactUI embed.FS

var quietPaths = []string{"/api/operations", "/api/resources", "/healthz", "/readyz"}

// @title LocalAI API
// @version 2.0.0
// @description The LocalAI Rest API.
// @termsOfService
// @contact.name LocalAI
// @contact.url https://localai.io
// @license.name MIT
// @license.url https://raw.githubusercontent.com/mudler/LocalAI/master/LICENSE
// @BasePath /
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization

func API(application *application.Application) (*echo.Echo, error) {
	e := echo.New()

	// Set body limit
	if application.ApplicationConfig().UploadLimitMB > 0 {
		e.Use(middleware.BodyLimit(fmt.Sprintf("%dM", application.ApplicationConfig().UploadLimitMB)))
	}

	// SPA fallback handler, set later when React UI is available
	var spaFallback func(echo.Context) error

	// Set error handler
	if !application.ApplicationConfig().OpaqueErrors {
		e.HTTPErrorHandler = func(err error, c echo.Context) {
			code := http.StatusInternalServerError
			var he *echo.HTTPError
			if errors.As(err, &he) {
				code = he.Code
			}

			// Handle 404 errors: serve React SPA for HTML requests, JSON otherwise
			if code == http.StatusNotFound {
				if spaFallback != nil {
					accept := c.Request().Header.Get("Accept")
					contentType := c.Request().Header.Get("Content-Type")
					if strings.Contains(accept, "text/html") && !strings.Contains(contentType, "application/json") {
						spaFallback(c)
						return
					}
				}
				notFoundHandler(c)
				return
			}

			// Send custom error page
			c.JSON(code, schema.ErrorResponse{
				Error: &schema.APIError{Message: err.Error(), Code: code},
			})
		}
	} else {
		e.HTTPErrorHandler = func(err error, c echo.Context) {
			code := http.StatusInternalServerError
			var he *echo.HTTPError
			if errors.As(err, &he) {
				code = he.Code
			}
			c.NoContent(code)
		}
	}

	// Set renderer
	e.Renderer = renderEngine()

	// Hide banner
	e.HideBanner = true
	e.HidePort = true

	// Middleware - StripPathPrefix must be registered early as it uses Rewrite which runs before routing
	e.Pre(httpMiddleware.StripPathPrefix())

	e.Pre(middleware.RemoveTrailingSlash())

	if application.ApplicationConfig().MachineTag != "" {
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.Response().Header().Set("Machine-Tag", application.ApplicationConfig().MachineTag)
				return next(c)
			}
		})
	}

	// Custom logger middleware using xlog
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			res := c.Response()
			err := next(c)

			// Fix for #7989: Reduce log verbosity of Web UI polling, resources API, and health checks
			// These paths are logged at DEBUG level (hidden by default) instead of INFO.
			isQuietPath := false
			for _, path := range quietPaths {
				if req.URL.Path == path {
					isQuietPath = true
					break
				}
			}

			if isQuietPath && res.Status == 200 {
				xlog.Debug("HTTP request", "method", req.Method, "path", req.URL.Path, "status", res.Status)
			} else {
				xlog.Info("HTTP request", "method", req.Method, "path", req.URL.Path, "status", res.Status)
			}
			return err
		}
	})

	// Recover middleware
	if !application.ApplicationConfig().Debug {
		e.Use(middleware.Recover())
	}

	// Metrics middleware
	if !application.ApplicationConfig().DisableMetrics {
		metricsService, err := services.NewLocalAIMetricsService()
		if err != nil {
			return nil, err
		}

		if metricsService != nil {
			e.Use(localai.LocalAIMetricsAPIMiddleware(metricsService))
			e.Server.RegisterOnShutdown(func() {
				metricsService.Shutdown()
			})
		}
	}

	// Health Checks should always be exempt from auth, so register these first
	routes.HealthRoutes(e)

	// Get key auth middleware
	keyAuthMiddleware, err := httpMiddleware.GetKeyAuthConfig(application.ApplicationConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create key auth config: %w", err)
	}

	// Favicon handler
	e.GET("/favicon.svg", func(c echo.Context) error {
		data, err := embedDirStatic.ReadFile("static/favicon.svg")
		if err != nil {
			return c.NoContent(http.StatusNotFound)
		}
		c.Response().Header().Set("Content-Type", "image/svg+xml")
		return c.Blob(http.StatusOK, "image/svg+xml", data)
	})

	// Static files - use fs.Sub to create a filesystem rooted at "static"
	staticFS, err := fs.Sub(embedDirStatic, "static")
	if err != nil {
		return nil, fmt.Errorf("failed to create static filesystem: %w", err)
	}
	e.StaticFS("/static", staticFS)

	// Generated content directories
	if application.ApplicationConfig().GeneratedContentDir != "" {
		os.MkdirAll(application.ApplicationConfig().GeneratedContentDir, 0750)
		audioPath := filepath.Join(application.ApplicationConfig().GeneratedContentDir, "audio")
		imagePath := filepath.Join(application.ApplicationConfig().GeneratedContentDir, "images")
		videoPath := filepath.Join(application.ApplicationConfig().GeneratedContentDir, "videos")

		os.MkdirAll(audioPath, 0750)
		os.MkdirAll(imagePath, 0750)
		os.MkdirAll(videoPath, 0750)

		e.Static("/generated-audio", audioPath)
		e.Static("/generated-images", imagePath)
		e.Static("/generated-videos", videoPath)
	}

	// Auth is applied to _all_ endpoints. No exceptions. Filtering out endpoints to bypass is the role of the Skipper property of the KeyAuth Configuration
	e.Use(keyAuthMiddleware)

	// CORS middleware
	if application.ApplicationConfig().CORS {
		corsConfig := middleware.CORSConfig{}
		if application.ApplicationConfig().CORSAllowOrigins != "" {
			corsConfig.AllowOrigins = strings.Split(application.ApplicationConfig().CORSAllowOrigins, ",")
		}
		e.Use(middleware.CORSWithConfig(corsConfig))
	} else {
		e.Use(middleware.CORS())
	}

	// CSRF middleware
	if application.ApplicationConfig().CSRF {
		xlog.Debug("Enabling CSRF middleware. Tokens are now required for state-modifying requests")
		e.Use(middleware.CSRF())
	}

	requestExtractor := httpMiddleware.NewRequestExtractor(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())

	routes.RegisterElevenLabsRoutes(e, requestExtractor, application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())

	// Create opcache for tracking UI operations (used by both UI and LocalAI routes)
	var opcache *services.OpCache
	if !application.ApplicationConfig().DisableWebUI {
		opcache = services.NewOpCache(application.GalleryService())
	}

	routes.RegisterLocalAIRoutes(e, requestExtractor, application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig(), application.GalleryService(), opcache, application.TemplatesEvaluator(), application)
	routes.RegisterAgentPoolRoutes(e, application)
	routes.RegisterOpenAIRoutes(e, requestExtractor, application)
	routes.RegisterAnthropicRoutes(e, requestExtractor, application)
	routes.RegisterOpenResponsesRoutes(e, requestExtractor, application)
	if !application.ApplicationConfig().DisableWebUI {
		routes.RegisterUIAPIRoutes(e, application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig(), application.GalleryService(), opcache, application)
		routes.RegisterUIRoutes(e, application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig(), application.GalleryService())

		// Serve React SPA from / with SPA fallback via 404 handler
		reactFS, fsErr := fs.Sub(reactUI, "react-ui/dist")
		if fsErr != nil {
			xlog.Warn("React UI not available (build with 'make core/http/react-ui/dist')", "error", fsErr)
		} else {
			serveIndex := func(c echo.Context) error {
				indexHTML, err := reactUI.ReadFile("react-ui/dist/index.html")
				if err != nil {
					return c.String(http.StatusNotFound, "React UI not built")
				}
				// Inject <base href> for reverse-proxy support
				baseURL := httpMiddleware.BaseURL(c)
				if baseURL != "" {
					baseTag := `<base href="` + baseURL + `" />`
					indexHTML = []byte(strings.Replace(string(indexHTML), "<head>", "<head>\n  "+baseTag, 1))
				}
				return c.HTMLBlob(http.StatusOK, indexHTML)
			}

			// Enable SPA fallback in the 404 handler for client-side routing
			spaFallback = serveIndex

			// Serve React SPA at /
			e.GET("/", serveIndex)

			// Serve React static assets (JS, CSS, etc.)
			serveReactAsset := func(c echo.Context) error {
				p := "assets/" + c.Param("*")
				f, err := reactFS.Open(p)
				if err == nil {
					defer f.Close()
					stat, statErr := f.Stat()
					if statErr == nil && !stat.IsDir() {
						contentType := mime.TypeByExtension(filepath.Ext(p))
						if contentType == "" {
							contentType = echo.MIMEOctetStream
						}
						return c.Stream(http.StatusOK, contentType, f)
					}
				}
				return echo.NewHTTPError(http.StatusNotFound)
			}
			e.GET("/assets/*", serveReactAsset)

			// Backward compatibility: redirect /app/* to /*
			e.GET("/app", func(c echo.Context) error {
				return c.Redirect(http.StatusMovedPermanently, "/")
			})
			e.GET("/app/*", func(c echo.Context) error {
				p := c.Param("*")
				return c.Redirect(http.StatusMovedPermanently, "/"+p)
			})
		}
	}
	routes.RegisterJINARoutes(e, requestExtractor, application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())

	// Note: 404 handling is done via HTTPErrorHandler above, no need for catch-all route

	// Log startup message
	e.Server.RegisterOnShutdown(func() {
		xlog.Info("LocalAI API server shutting down")
	})

	return e, nil
}
