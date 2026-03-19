package routes

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

var backendLogsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func RegisterUIRoutes(app *echo.Echo,
	cl *config.ModelConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	galleryService *services.GalleryService,
	adminMiddleware echo.MiddlewareFunc) {

	// SPA routes are handled by the 404 fallback in app.go which serves
	// index.html for any unmatched HTML request, enabling client-side routing.

	// Pipeline models API (for the Talk page WebRTC interface)
	app.GET("/api/pipeline-models", func(c echo.Context) error {
		type pipelineModelInfo struct {
			Name          string `json:"name"`
			VAD           string `json:"vad"`
			Transcription string `json:"transcription"`
			LLM           string `json:"llm"`
			TTS           string `json:"tts"`
			Voice         string `json:"voice"`
		}

		pipelineModels := cl.GetModelConfigsByFilter(func(_ string, cfg *config.ModelConfig) bool {
			p := cfg.Pipeline
			return p.VAD != "" && p.Transcription != "" && p.LLM != "" && p.TTS != ""
		})

		slices.SortFunc(pipelineModels, func(a, b config.ModelConfig) int {
			return cmp.Compare(a.Name, b.Name)
		})

		var models []pipelineModelInfo
		for _, cfg := range pipelineModels {
			models = append(models, pipelineModelInfo{
				Name:          cfg.Name,
				VAD:           cfg.Pipeline.VAD,
				Transcription: cfg.Pipeline.Transcription,
				LLM:           cfg.Pipeline.LLM,
				TTS:           cfg.Pipeline.TTS,
				Voice:         cfg.TTSConfig.Voice,
			})
		}

		return c.JSON(200, models)
	}, adminMiddleware)

	app.GET("/api/traces", func(c echo.Context) error {
		return c.JSON(200, middleware.GetTraces())
	}, adminMiddleware)

	app.POST("/api/traces/clear", func(c echo.Context) error {
		middleware.ClearTraces()
		return c.NoContent(204)
	}, adminMiddleware)

	app.GET("/api/backend-traces", func(c echo.Context) error {
		return c.JSON(200, trace.GetBackendTraces())
	}, adminMiddleware)

	app.POST("/api/backend-traces/clear", func(c echo.Context) error {
		trace.ClearBackendTraces()
		return c.NoContent(204)
	}, adminMiddleware)

	// Backend logs REST endpoints
	app.GET("/api/backend-logs", func(c echo.Context) error {
		return c.JSON(200, ml.BackendLogs().ListModels())
	}, adminMiddleware)

	app.GET("/api/backend-logs/:modelId", func(c echo.Context) error {
		modelID := c.Param("modelId")
		return c.JSON(200, ml.BackendLogs().GetLines(modelID))
	}, adminMiddleware)

	app.POST("/api/backend-logs/:modelId/clear", func(c echo.Context) error {
		ml.BackendLogs().Clear(c.Param("modelId"))
		return c.NoContent(204)
	}, adminMiddleware)

	// Backend logs WebSocket endpoint for real-time streaming
	app.GET("/ws/backend-logs/:modelId", func(c echo.Context) error {
		modelID := c.Param("modelId")

		ws, err := backendLogsUpgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		ws.SetReadLimit(4096)

		// Set up ping/pong for keepalive
		ws.SetReadDeadline(time.Now().Add(90 * time.Second))
		ws.SetPongHandler(func(string) error {
			ws.SetReadDeadline(time.Now().Add(90 * time.Second))
			return nil
		})

		conn := &backendLogsConn{Conn: ws}

		// Send existing lines as initial batch
		existingLines := ml.BackendLogs().GetLines(modelID)
		initialMsg := map[string]any{
			"type":  "initial",
			"lines": existingLines,
		}
		if err := conn.writeJSON(initialMsg); err != nil {
			xlog.Debug("WebSocket backend-logs initial write failed", "error", err)
			return nil
		}

		// Subscribe to new lines
		lineCh, unsubscribe := ml.BackendLogs().Subscribe(modelID)
		defer unsubscribe()

		// Handle close from client side
		closeCh := make(chan struct{})
		go func() {
			for {
				_, _, err := ws.ReadMessage()
				if err != nil {
					close(closeCh)
					return
				}
			}
		}()

		// Ping ticker for keepalive
		pingTicker := time.NewTicker(30 * time.Second)
		defer pingTicker.Stop()

		// Forward new lines to WebSocket
		for {
			select {
			case line, ok := <-lineCh:
				if !ok {
					return nil
				}
				lineMsg := map[string]any{
					"type": "line",
					"line": line,
				}
				if err := conn.writeJSON(lineMsg); err != nil {
					xlog.Debug("WebSocket backend-logs write error", "error", err)
					return nil
				}
			case <-pingTicker.C:
				if err := conn.writePing(); err != nil {
					return nil
				}
			case <-closeCh:
				return nil
			}
		}
	}, adminMiddleware)
}

// backendLogsConn wraps a websocket connection with a mutex for safe concurrent writes
type backendLogsConn struct {
	*websocket.Conn
	mu sync.Mutex
}

func (c *backendLogsConn) writeJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}
	return c.Conn.WriteMessage(websocket.TextMessage, data)
}

func (c *backendLogsConn) writePing() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return c.Conn.WriteMessage(websocket.PingMessage, nil)
}
