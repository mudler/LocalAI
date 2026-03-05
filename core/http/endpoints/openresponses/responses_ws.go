package openresponses

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

const (
	maxWebSocketMessageSize = 10 * 1024 * 1024 // 10MB
	connectionTimeout       = 60 * time.Minute
)

// Connection represents a WebSocket connection with its state
type Connection struct {
	conn                *websocket.Conn
	sessionID           string
	responseID          string
	previousID          string
	message             *schema.OpenResponsesRequest
	cache               sync.Map
	createdAt           time.Time
	lastActive          time.Time
	closeChan           chan struct{}
	done                chan struct{}
	appConfig           *config.ApplicationConfig
	modelLoader         *model.ModelLoader
	modelConfig         *config.ModelConfig
	modelConfigLoader   *config.ModelConfigLoader
	evaluator           *templates.Evaluator
}

// ConnectionPool manages all active WebSocket connections
type ConnectionPool struct {
	connections map[string]*Connection
	mu          sync.RWMutex
}

var pool = &ConnectionPool{
	connections: make(map[string]*Connection),
}

// LockedWebsocket wraps a websocket connection with a mutex for safe concurrent writes
type LockedWebsocket struct {
	*websocket.Conn
	sync.Mutex
}

func (l *LockedWebsocket) WriteMessage(messageType int, data []byte) error {
	l.Lock()
	defer l.Unlock()
	return l.Conn.WriteMessage(messageType, data)
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins
	},
}

// ResponsesWebSocketEndpoint handles WebSocket connections for /v1/responses
func ResponsesWebSocketEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Upgrade to WebSocket
		ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		// Set maximum message size to prevent DoS attacks
		ws.SetReadLimit(maxWebSocketMessageSize)

		// Extract query parameters
		modelName := c.QueryParam("model")

		xlog.Debug("WebSocket connection established", "address", ws.RemoteAddr().String(), "model", modelName)

		// Create new connection handler
		return handleWebSocketConnection(ws, modelName, app)
	}
}

func handleWebSocketConnection(ws *websocket.Conn, model string, app *application.Application) error {
	// Create locked websocket for safe concurrent writes
	lws := &LockedWebsocket{Conn: ws}

	// Load model config
	cl := app.ModelConfigLoader()
	cfg, err := cl.LoadModelConfigFileByNameDefaultOptions(model, app.ApplicationConfig())
	if err != nil {
		xlog.Error("failed to load model config", "error", err)
		sendError(lws, "model_load_error", "Failed to load model config", "", "")
		return nil
	}

	// Create new connection
	conn := &Connection{
		conn:              lws,
		sessionID:         fmt.Sprintf("conn_%s", uuid.New().String()),
		createdAt:         time.Now(),
		lastActive:        time.Now(),
		closeChan:         make(chan struct{}),
		done:              make(chan struct{}),
		appConfig:         app.ApplicationConfig(),
		modelLoader:       app.ModelLoader(),
		modelConfigLoader: app.ModelConfigLoader(),
		evaluator:         app.TemplatesEvaluator(),
		modelConfig:       cfg,
	}

	// Add to pool
	pool.addConnection(conn)
	defer pool.removeConnection(conn.sessionID)

	// Start timeout goroutine
	go conn.timeoutMonitor()

	// Start message handler
	return conn.readMessages()
}

func (c *Connection) timeoutMonitor() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Since(c.lastActive) > connectionTimeout {
				xlog.Info("Connection timeout", "sessionID", c.sessionID)
				c.conn.Close()
				return
			}
		case <-c.closeChan:
			return
		}
	}
}

func (c *Connection) readMessages() error {
	for {
		select {
		case <-c.closeChan:
			return nil
		default:
			// Read message
			_, message, err := c.conn.ReadMessage()
			if err != nil {
				if !websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					return err
				}
				xlog.Info("WebSocket closed", "error", err)
				return nil
			}

			// Update last active time
			c.lastActive = time.Now()

			// Parse message
			var msg map[string]interface{}
			if err := json.Unmarshal(message, &msg); err != nil {
				sendError(c.conn, "invalid_message", "Failed to parse message", "", "")
				continue
			}

			// Route to appropriate handler
			if eventType, ok := msg["type"].(string); ok {
				switch eventType {
				case "response.create":
					c.handleResponseCreate(msg)
				case "response.continue":
					c.handleResponseContinue(msg)
				case "response.cancel":
					c.handleResponseCancel(msg)
				default:
					sendError(c.conn, "unknown_message_type", fmt.Sprintf("Unknown message type: %s", eventType), "", "")
				}
			}
		}
	}
}

func (c *Connection) handleResponseCreate(msg map[string]interface{}) {
	// Parse request
	reqData, ok := msg["input"].(map[string]interface{})
	if !ok {
		sendError(c.conn, "invalid_request", "Missing input field", "", "")
		return
	}

	// Convert to schema
	req := &schema.OpenResponsesRequest{}
	if jsonBytes, err := json.Marshal(reqData); err == nil {
		json.Unmarshal(jsonBytes, req)
	}

	// Set response ID
	responseID := fmt.Sprintf("resp_%s", uuid.New().String())
	c.responseID = responseID

	// Store in cache if needed
	if req.Store != nil && *req.Store {
		store := GetGlobalStore()
		store.Set(responseID, RequestStoreItem{
			Request:  *req,
			Response: nil, // Will be filled after processing
		})
	}

	// Send response.created event
	c.sendEvent("response.created", map[string]interface{}{
		"id":              responseID,
		"object":          "response",
		"status":          "in_progress",
		"model":           req.Model,
		"created_at":      time.Now().Unix(),
		"input":           req.Input,
		"instructions":    req.Instructions,
		"max_output_tokens": req.MaxOutputTokens,
		"temperature":     req.Temperature,
		"tool_choice":     req.ToolChoice,
		"tools":           req.Tools,
		"top_p":           req.TopP,
		"metadata":        req.Metadata,
	})

	// Process the response
	go func() {
		// TODO: Implement actual response processing using existing responses.go logic
		// This would involve:
		// 1. Converting input to messages
		// 2. Running inference
		// 3. Streaming results back
		// 4. Sending response.done event

		// For now, send a placeholder done event
		c.sendEvent("response.done", map[string]interface{}{
			"id":      responseID,
			"object":  "response",
			"status":  "completed",
			"output":  []interface{}{},
			"usage":   map[string]interface{}{},
		})
	}()
}

func (c *Connection) handleResponseContinue(msg map[string]interface{}) {
	// Get previous response ID
	previousID, ok := msg["previous_response_id"].(string)
	if !ok || previousID == "" {
		sendError(c.conn, "invalid_request", "Missing previous_response_id", "previous_response_id", "")
		return
	}

	// Retrieve from store
	store := GetGlobalStore()
	stored, err := store.Get(previousID)
	if err != nil {
		sendError(c.conn, "not_found", fmt.Sprintf("previous response not found: %s", previousID), "previous_response_id", "")
		return
	}

	c.previousID = previousID
	c.responseID = fmt.Sprintf("resp_%s", uuid.New().String())

	// Send response.created event for continuation
	c.sendEvent("response.created", map[string]interface{}{
		"id":           c.responseID,
		"object":       "response",
		"status":       "in_progress",
		"previous_id":  previousID,
	})

	// Process continuation
	go func() {
		// TODO: Implement continuation logic
		c.sendEvent("response.done", map[string]interface{}{
			"id":      c.responseID,
			"object":  "response",
			"status":  "completed",
			"output":  []interface{}{},
			"usage":   map[string]interface{}{},
		})
	}()
}

func (c *Connection) handleResponseCancel(msg map[string]interface{}) {
	responseID, ok := msg["response_id"].(string)
	if !ok || responseID == "" {
		sendError(c.conn, "invalid_request", "Missing response_id", "", "")
		return
	}

	// Send cancellation event
	c.sendEvent("response.cancelled", map[string]interface{}{
		"id": responseID,
	})

	xlog.Info("Response cancelled", "responseID", responseID)
}

func (c *Connection) sendEvent(eventType string, data map[string]interface{}) {
	event := map[string]interface{}{
		"type": eventType,
	}

	// Merge event-specific data
	for k, v := range data {
		event[k] = v
	}

	eventBytes, err := json.Marshal(event)
	if err != nil {
		xlog.Error("failed to marshal event", "error", err)
		return
	}

	if err := c.conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		xlog.Error("failed to write message", "error", err)
	}
}

func sendError(conn *LockedWebsocket, errorCode, message, param, requestId string) {
	errorEvent := map[string]interface{}{
		"type":        "error",
		"error":       map[string]interface{}{},
	}

	errorInfo := map[string]interface{}{
		"type":    errorCode,
		"message": message,
	}

	if param != "" {
		errorInfo["param"] = param
	}
	if requestId != "" {
		errorInfo["request_id"] = requestId
	}

	errorEvent["error"] = errorInfo

	eventBytes, err := json.Marshal(errorEvent)
	if err != nil {
		xlog.Error("failed to marshal error event", "error", err)
		return
	}

	conn.WriteMessage(websocket.TextMessage, eventBytes)
}

// ConnectionPool methods
func (p *ConnectionPool) addConnection(conn *Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connections[conn.sessionID] = conn
	xlog.Info("Connection added", "sessionID", conn.sessionID)
}

func (p *ConnectionPool) removeConnection(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if conn, ok := p.connections[sessionID]; ok {
		close(conn.closeChan)
		delete(p.connections, sessionID)
		xlog.Info("Connection removed", "sessionID", sessionID)
	}
}

func (p *ConnectionPool) getConnection(sessionID string) (*Connection, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	conn, ok := p.connections[sessionID]
	return conn, ok
}

func (p *ConnectionPool) cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for sessionID, conn := range p.connections {
		if time.Since(conn.lastActive) > connectionTimeout {
			close(conn.closeChan)
			delete(p.connections, sessionID)
			xlog.Info("Connection cleaned up (timeout)", "sessionID", sessionID)
		}
	}
}
