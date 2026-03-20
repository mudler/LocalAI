package localai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

var backendLogsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // no origin header = same-origin or non-browser
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return u.Host == r.Host
	},
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

// ListBackendLogsEndpoint returns model IDs that have log buffers
// @Summary List models with backend logs
// @Description Returns a sorted list of model IDs that have captured backend process output
// @Tags monitoring
// @Produce json
// @Success 200 {array} string "Model IDs with logs"
// @Router /api/backend-logs [get]
func ListBackendLogsEndpoint(ml *model.ModelLoader) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(200, ml.BackendLogs().ListModels())
	}
}

// GetBackendLogsEndpoint returns log lines for a specific model
// @Summary Get backend logs for a model
// @Description Returns all captured log lines (stdout/stderr) for the specified model's backend process
// @Tags monitoring
// @Produce json
// @Param modelId path string true "Model ID"
// @Success 200 {array} model.BackendLogLine "Log lines"
// @Router /api/backend-logs/{modelId} [get]
func GetBackendLogsEndpoint(ml *model.ModelLoader) echo.HandlerFunc {
	return func(c echo.Context) error {
		modelID := c.Param("modelId")
		return c.JSON(200, ml.BackendLogs().GetLines(modelID))
	}
}

// ClearBackendLogsEndpoint clears log lines for a specific model
// @Summary Clear backend logs for a model
// @Description Removes all captured log lines for the specified model's backend process
// @Tags monitoring
// @Param modelId path string true "Model ID"
// @Success 204 "Logs cleared"
// @Router /api/backend-logs/{modelId}/clear [post]
func ClearBackendLogsEndpoint(ml *model.ModelLoader) echo.HandlerFunc {
	return func(c echo.Context) error {
		ml.BackendLogs().Clear(c.Param("modelId"))
		return c.NoContent(204)
	}
}

// BackendLogsWebSocketEndpoint streams backend logs in real-time over WebSocket
// @Summary Stream backend logs via WebSocket
// @Description Opens a WebSocket connection for real-time backend log streaming. Sends an initial batch of existing lines (type "initial"), then streams new lines as they appear (type "line"). Supports ping/pong keepalive.
// @Tags monitoring
// @Param modelId path string true "Model ID"
// @Router /ws/backend-logs/{modelId} [get]
func BackendLogsWebSocketEndpoint(ml *model.ModelLoader) echo.HandlerFunc {
	return func(c echo.Context) error {
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
	}
}
