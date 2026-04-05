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

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"

	httpMiddleware "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/http/routes"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/finetune"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/monitoring"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/quantization"

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
// @schemes http https
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @tag.name inference
// @tag.description Chat completions, text completions, edits, and responses (OpenAI-compatible)
// @tag.name embeddings
// @tag.description Vector embeddings (OpenAI-compatible)
// @tag.name audio
// @tag.description Text-to-speech, transcription, voice activity detection, sound generation
// @tag.name images
// @tag.description Image generation and inpainting
// @tag.name video
// @tag.description Video generation from prompts
// @tag.name detection
// @tag.description Object detection in images
// @tag.name tokenize
// @tag.description Tokenization and token metrics
// @tag.name models
// @tag.description Model gallery browsing, installation, deletion, and listing
// @tag.name backends
// @tag.description Backend gallery browsing, installation, deletion, and listing
// @tag.name config
// @tag.description Model configuration metadata, autocomplete, PATCH updates, VRAM estimation
// @tag.name monitoring
// @tag.description Prometheus metrics, backend status, system information
// @tag.name mcp
// @tag.description Model Context Protocol — tool-augmented chat with MCP servers
// @tag.name agent-jobs
// @tag.description Agent task and job management
// @tag.name p2p
// @tag.description Peer-to-peer networking nodes and tokens
// @tag.name rerank
// @tag.description Document reranking
// @tag.name instructions
// @tag.description API instruction discovery — browse instruction areas and get endpoint guides

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

	// IP restriction middleware
	if application.ApplicationConfig().IPAllowListHelper != nil {
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				clientIP := c.RealIP()
				if !application.ApplicationConfig().IPAllowListHelper.IsAllowed(clientIP) {
					return c.JSON(http.StatusForbidden, schema.ErrorResponse{
						Error: &schema.APIError{Message: "Forbidden: your IP is not allowed", Code: http.StatusForbidden},
					})
				}
				return next(c)
			}
		})
	}

	// Metrics middleware
	if !application.ApplicationConfig().DisableMetrics {
		metricsService, err := monitoring.NewLocalAIMetricsService()
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

	// Build auth middleware: use the new auth.Middleware when auth is enabled or
	// as a unified replacement for the legacy key-auth middleware.
	authMiddleware := auth.Middleware(application.AuthDB(), application.ApplicationConfig())

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

	// Initialize usage recording when auth DB is available
	if application.AuthDB() != nil {
		httpMiddleware.InitUsageRecorder(application.AuthDB())
	}

	// Auth is applied to _all_ endpoints. Filtering out endpoints to bypass is
	// the role of the exempt-path logic inside the middleware.
	e.Use(authMiddleware)

	// Feature and model access control (after auth middleware, before routes)
	if application.AuthDB() != nil {
		e.Use(auth.RequireRouteFeature(application.AuthDB()))
		e.Use(auth.RequireModelAccess(application.AuthDB()))
		e.Use(auth.RequireQuota(application.AuthDB()))
	}

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

	// CSRF middleware (enabled by default, disable with LOCALAI_DISABLE_CSRF=true)
	//
	// Protection relies on Echo's Sec-Fetch-Site header check (supported by all
	// modern browsers). The legacy cookie+token approach is removed because
	// Echo's Sec-Fetch-Site short-circuit never sets the cookie, so the frontend
	// could never read a token to send back.
	if !application.ApplicationConfig().DisableCSRF {
		xlog.Debug("Enabling CSRF middleware (Sec-Fetch-Site mode)")
		e.Use(middleware.CSRFWithConfig(middleware.CSRFConfig{
			Skipper: func(c echo.Context) bool {
				// Skip CSRF for API clients using auth headers (may be cross-origin)
				if c.Request().Header.Get("Authorization") != "" {
					return true
				}
				if c.Request().Header.Get("x-api-key") != "" || c.Request().Header.Get("xi-api-key") != "" {
					return true
				}
				// Skip when Sec-Fetch-Site header is absent (older browsers, reverse
				// proxies that strip the header). The SameSite=Lax cookie attribute
				// provides baseline CSRF protection for these clients.
				if c.Request().Header.Get("Sec-Fetch-Site") == "" {
					return true
				}
				return false
			},
			// Allow same-site requests (subdomains / different ports) in addition
			// to same-origin which Echo already permits by default.
			AllowSecFetchSiteFunc: func(c echo.Context) (bool, error) {
				secFetchSite := c.Request().Header.Get("Sec-Fetch-Site")
				if secFetchSite == "same-site" {
					return true, nil
				}
				// cross-site: block
				return false, nil
			},
		}))
	}

	// Admin middleware: enforces admin role when auth is enabled, no-op otherwise
	var adminMiddleware echo.MiddlewareFunc
	if application.AuthDB() != nil {
		adminMiddleware = auth.RequireAdmin()
	} else {
		adminMiddleware = auth.NoopMiddleware()
	}

	// Feature middlewares: per-feature access control
	agentsMw := auth.RequireFeature(application.AuthDB(), auth.FeatureAgents)
	skillsMw := auth.RequireFeature(application.AuthDB(), auth.FeatureSkills)
	collectionsMw := auth.RequireFeature(application.AuthDB(), auth.FeatureCollections)
	mcpJobsMw := auth.RequireFeature(application.AuthDB(), auth.FeatureMCPJobs)

	requestExtractor := httpMiddleware.NewRequestExtractor(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())

	// Register auth routes (login, callback, API keys, user management)
	routes.RegisterAuthRoutes(e, application)

	routes.RegisterElevenLabsRoutes(e, requestExtractor, application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())

	// Create opcache for tracking UI operations (used by both UI and LocalAI routes)
	var opcache *galleryop.OpCache
	if !application.ApplicationConfig().DisableWebUI {
		opcache = galleryop.NewOpCache(application.GalleryService())
	}

	mcpMw := auth.RequireFeature(application.AuthDB(), auth.FeatureMCP)
	routes.RegisterLocalAIRoutes(e, requestExtractor, application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig(), application.GalleryService(), opcache, application.TemplatesEvaluator(), application, adminMiddleware, mcpJobsMw, mcpMw)
	routes.RegisterAgentPoolRoutes(e, application, agentsMw, skillsMw, collectionsMw)
	// Fine-tuning routes
	fineTuningMw := auth.RequireFeature(application.AuthDB(), auth.FeatureFineTuning)
	ftService := finetune.NewFineTuneService(
		application.ApplicationConfig(),
		application.ModelLoader(),
		application.ModelConfigLoader(),
	)
	if d := application.Distributed(); d != nil {
		ftService.SetNATSClient(d.Nats)
		if d.DistStores != nil && d.DistStores.FineTune != nil {
			ftService.SetFineTuneStore(d.DistStores.FineTune)
		}
	}
	routes.RegisterFineTuningRoutes(e, ftService, application.ApplicationConfig(), fineTuningMw)

	// Quantization routes
	quantizationMw := auth.RequireFeature(application.AuthDB(), auth.FeatureQuantization)
	qService := quantization.NewQuantizationService(
		application.ApplicationConfig(),
		application.ModelLoader(),
		application.ModelConfigLoader(),
	)
	routes.RegisterQuantizationRoutes(e, qService, application.ApplicationConfig(), quantizationMw)

	// Node management routes (distributed mode)
	distCfg := application.ApplicationConfig().Distributed
	var registry *nodes.NodeRegistry
	var remoteUnloader nodes.NodeCommandSender
	if d := application.Distributed(); d != nil {
		registry = d.Registry
		if d.Router != nil {
			remoteUnloader = d.Router.Unloader()
		}
	}
	routes.RegisterNodeSelfServiceRoutes(e, registry, distCfg.RegistrationToken, distCfg.AutoApproveNodes, application.AuthDB(), application.ApplicationConfig().Auth.APIKeyHMACSecret)
	routes.RegisterNodeAdminRoutes(e, registry, remoteUnloader, adminMiddleware, application.AuthDB(), application.ApplicationConfig().Auth.APIKeyHMACSecret, application.ApplicationConfig().Distributed.RegistrationToken)

	// Distributed SSE routes (job progress + agent events via NATS)
	if d := application.Distributed(); d != nil {
		if d.Dispatcher != nil {
			e.GET("/api/agent/jobs/:id/progress", d.Dispatcher.SSEHandler(), mcpJobsMw)
		}
		if d.AgentBridge != nil {
			e.GET("/api/agents/:name/sse/distributed", d.AgentBridge.SSEHandler(), agentsMw)
		}
	}

	routes.RegisterOpenAIRoutes(e, requestExtractor, application)
	routes.RegisterAnthropicRoutes(e, requestExtractor, application)
	routes.RegisterOpenResponsesRoutes(e, requestExtractor, application)
	if !application.ApplicationConfig().DisableWebUI {
		routes.RegisterUIAPIRoutes(e, application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig(), application.GalleryService(), opcache, application, adminMiddleware)
		routes.RegisterUIRoutes(e, application.ModelConfigLoader(), application.ApplicationConfig(), application.GalleryService(), adminMiddleware)

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

			// Serve React SPA at /app
			e.GET("/app", serveIndex)
			e.GET("/app/*", serveIndex)

			// prefixRedirect performs a redirect that preserves X-Forwarded-Prefix for reverse-proxy support.
			prefixRedirect := func(c echo.Context, target string) error {
				if prefix := c.Request().Header.Get("X-Forwarded-Prefix"); prefix != "" {
					target = strings.TrimSuffix(prefix, "/") + target
				}
				return c.Redirect(http.StatusMovedPermanently, target)
			}

			// Redirect / to /app
			e.GET("/", func(c echo.Context) error {
				return prefixRedirect(c, "/app")
			})

			// Backward compatibility: redirect /browse/* to /app/*
			e.GET("/browse", func(c echo.Context) error {
				return prefixRedirect(c, "/app")
			})
			e.GET("/browse/*", func(c echo.Context) error {
				p := c.Param("*")
				return prefixRedirect(c, "/app/"+p)
			})

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
