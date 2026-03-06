package openresponses

import (
	"context"
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
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

const (
	wsMaxMessageSize  = 10 * 1024 * 1024 // 10MB
	wsConnectionLimit = 60 * time.Minute
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// lockedConn wraps a websocket connection with a mutex for safe concurrent writes
type lockedConn struct {
	*websocket.Conn
	sync.Mutex
}

func (lc *lockedConn) writeJSON(v any) error {
	lc.Lock()
	defer lc.Unlock()
	return lc.Conn.WriteJSON(v)
}

// WebSocketEndpoint handles WebSocket mode for the Responses API.
// Clients connect via ws://<host>:<port>/v1/responses and send response.create messages.
// Events are streamed back over the WebSocket connection instead of SSE.
func WebSocketEndpoint(application *application.Application) echo.HandlerFunc {
	cl := application.ModelConfigLoader()
	ml := application.ModelLoader()
	evaluator := application.TemplatesEvaluator()
	appConfig := application.ApplicationConfig()

	return func(c echo.Context) error {
		ws, err := wsUpgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		ws.SetReadLimit(wsMaxMessageSize)

		// Set absolute deadline so blocking ReadMessage unblocks after the limit
		deadline := time.Now().Add(wsConnectionLimit)
		ws.SetReadDeadline(deadline)
		ws.SetWriteDeadline(deadline)

		conn := &lockedConn{Conn: ws}

		// Context for cancelling in-flight work when the connection closes
		connCtx, connCancel := context.WithDeadline(context.Background(), deadline)
		defer connCancel()

		xlog.Debug("WebSocket Responses connection established", "address", ws.RemoteAddr().String())

		handleWebSocketConnection(connCtx, conn, cl, ml, evaluator, appConfig)
		return nil
	}
}

// handleWebSocketConnection runs the read loop for a single WebSocket connection.
func handleWebSocketConnection(connCtx context.Context, conn *lockedConn, cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) {
	// Track in-flight response to enforce one-at-a-time
	var inflight sync.Mutex

	// Read loop
	for {
		select {
		case <-connCtx.Done():
			sendWSError(conn, "websocket_connection_limit_reached", "Connection exceeded maximum duration", "")
			return
		default:
		}

		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				xlog.Debug("WebSocket Responses read error", "error", err)
			}
			return
		}

		// Parse the envelope to determine message type
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(msgBytes, &envelope); err != nil {
			sendWSError(conn, "invalid_request", "invalid JSON message", "")
			continue
		}

		if envelope.Type != "response.create" {
			sendWSError(conn, "invalid_request", fmt.Sprintf("unsupported message type: %s", envelope.Type), "type")
			continue
		}

		// Parse the full request
		var wsMsg schema.ORWebSocketMessage
		if err := json.Unmarshal(msgBytes, &wsMsg); err != nil {
			sendWSError(conn, "invalid_request", fmt.Sprintf("failed to parse request: %v", err), "")
			continue
		}

		// Enforce one in-flight response at a time (non-blocking check)
		if !inflight.TryLock() {
			sendWSError(conn, "invalid_request", "a response is already in progress on this connection", "")
			continue
		}

		go func() {
			defer inflight.Unlock()
			handleWSResponseCreate(connCtx, conn, &wsMsg.OpenResponsesRequest, cl, ml, evaluator, appConfig)
		}()
	}
}

// handleWSResponseCreate processes a single response.create message and streams events over WebSocket.
// It reuses the existing background stream infrastructure: the request is processed via
// handleBackgroundStream which buffers events into the store, and a forwarder goroutine
// reads those events and sends them over the WebSocket.
func handleWSResponseCreate(connCtx context.Context, conn *lockedConn, input *schema.OpenResponsesRequest, cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) {
	createdAt := time.Now().Unix()
	responseID := fmt.Sprintf("resp_%s", uuid.New().String())

	if input.Model == "" {
		sendWSError(conn, "invalid_request", "model is required", "model")
		return
	}

	// Resolve model configuration (same logic as middleware.SetModelAndConfig)
	cfg, err := cl.LoadModelConfigFileByNameDefaultOptions(input.Model, appConfig)
	if err != nil {
		xlog.Warn("WebSocket Responses: model config not found", "model", input.Model, "error", err)
		sendWSError(conn, "invalid_request", fmt.Sprintf("model not found: %s", input.Model), "model")
		return
	}
	if cfg.Model == "" {
		cfg.Model = input.Model
	}

	// Merge request params into config (same as mergeOpenResponsesRequestAndModelConfig)
	if err := middleware.MergeOpenResponsesConfig(cfg, input); err != nil {
		sendWSError(conn, "invalid_request", fmt.Sprintf("invalid configuration: %v", err), "")
		return
	}

	// Set up context with cancellation tied to connection lifetime
	reqCtx, reqCancel := context.WithCancel(connCtx)
	defer reqCancel()

	input.Context = reqCtx
	input.Cancel = reqCancel

	store := GetGlobalStore()
	if appConfig.OpenResponsesStoreTTL > 0 {
		store.SetTTL(appConfig.OpenResponsesStoreTTL)
	}

	shouldStore := true
	if input.Store != nil && !*input.Store {
		shouldStore = false
	}

	// Handle previous_response_id
	var messages []schema.Message
	if input.PreviousResponseID != "" {
		stored, err := store.Get(input.PreviousResponseID)
		if err != nil {
			sendWSErrorEvent(conn, "previous_response_not_found",
				fmt.Sprintf("previous response not found: %s", input.PreviousResponseID), "previous_response_id")
			return
		}

		previousInputMessages, err := convertORInputToMessages(stored.Request.Input, cfg)
		if err != nil {
			sendWSError(conn, "invalid_request", fmt.Sprintf("failed to convert previous input: %v", err), "")
			return
		}

		previousOutputMessages, err := convertOROutputItemsToMessages(stored.Response.Output)
		if err != nil {
			sendWSError(conn, "invalid_request", fmt.Sprintf("failed to convert previous response: %v", err), "")
			return
		}

		messages = previousInputMessages
		messages = append(messages, previousOutputMessages...)
	}

	// Convert current input to messages
	newMessages, err := convertORInputToMessages(input.Input, cfg)
	if err != nil {
		sendWSError(conn, "invalid_request", fmt.Sprintf("failed to parse input: %v", err), "")
		return
	}
	messages = append(messages, newMessages...)

	if input.Instructions != "" {
		messages = append([]schema.Message{{Role: "system", StringContent: input.Instructions}}, messages...)
	}

	// Handle tools
	var funcs functions.Functions
	var shouldUseFn bool

	if len(input.Tools) > 0 {
		funcs, shouldUseFn = convertORToolsToFunctions(input, cfg)
	}

	// Create OpenAI-compatible request
	openAIReq := &schema.OpenAIRequest{
		PredictionOptions: schema.PredictionOptions{
			BasicModelRequest: schema.BasicModelRequest{Model: input.Model},
			Temperature:       input.Temperature,
			TopP:              input.TopP,
			Maxtokens:         input.MaxOutputTokens,
		},
		Messages:  messages,
		Stream:    true, // WebSocket mode always streams
		Context:   reqCtx,
		Cancel:    reqCancel,
		Functions: funcs,
	}

	if input.TextFormat != nil {
		openAIReq.ResponseFormat = convertTextFormatToResponseFormat(input.TextFormat)
	}

	// Generate grammar for function calling
	if shouldUseFn && !cfg.FunctionsConfig.GrammarConfig.NoGrammar {
		noActionName := "answer"
		noActionDescription := "use this action to answer without performing any action"
		if cfg.FunctionsConfig.NoActionFunctionName != "" {
			noActionName = cfg.FunctionsConfig.NoActionFunctionName
		}
		if cfg.FunctionsConfig.NoActionDescriptionName != "" {
			noActionDescription = cfg.FunctionsConfig.NoActionDescriptionName
		}

		noActionGrammar := functions.Function{
			Name:        noActionName,
			Description: noActionDescription,
			Parameters: map[string]interface{}{
				"properties": map[string]interface{}{
					"message": map[string]interface{}{
						"type":        "string",
						"description": "The message to reply the user with",
					},
				},
			},
		}

		funcsWithNoAction := make(functions.Functions, len(funcs))
		copy(funcsWithNoAction, funcs)

		if !cfg.FunctionsConfig.DisableNoAction {
			funcsWithNoAction = append(funcsWithNoAction, noActionGrammar)
		}

		if cfg.FunctionToCall() != "" {
			funcsWithNoAction = funcsWithNoAction.Select(cfg.FunctionToCall())
		}

		jsStruct := funcsWithNoAction.ToJSONStructure(cfg.FunctionsConfig.FunctionNameKey, cfg.FunctionsConfig.FunctionNameKey)
		g, err := jsStruct.Grammar(cfg.FunctionsConfig.GrammarOptions()...)
		if err == nil {
			cfg.Grammar = g
		} else {
			xlog.Error("WebSocket Responses: failed generating grammar", "error", err)
		}
	}

	// Merge contiguous assistant messages
	openAIReq.Messages = mergeContiguousAssistantMessages(openAIReq.Messages)

	predInput := evaluator.TemplateMessages(*openAIReq, openAIReq.Messages, cfg, funcs, shouldUseFn)

	// Use the background stream infrastructure: store the request as a background task,
	// process it via handleBackgroundStream, and forward buffered events over WebSocket.
	queuedResponse := buildORResponse(responseID, createdAt, nil, schema.ORStatusQueued, input, []schema.ORItemField{}, nil, shouldStore)
	store.StoreBackground(responseID, input, queuedResponse, reqCancel, true)

	// Start processing in a goroutine
	processDone := make(chan struct{})
	go func() {
		defer close(processDone)
		store.UpdateStatus(responseID, schema.ORStatusInProgress, nil)

		finalResponse, bgErr := handleBackgroundStream(reqCtx, store, responseID, createdAt, input, cfg, ml, cl, appConfig, predInput, openAIReq, funcs, shouldUseFn)
		if bgErr != nil {
			xlog.Error("WebSocket Responses: processing failed", "response_id", responseID, "error", bgErr)
			now := time.Now().Unix()
			store.UpdateStatus(responseID, schema.ORStatusFailed, &now)

			// Buffer an error event so the client sees the failure
			failedResponse := buildORResponse(responseID, createdAt, &now, schema.ORStatusFailed, input, []schema.ORItemField{}, nil, shouldStore)
			bufferEvent(store, responseID, &schema.ORStreamEvent{
				Type:     "response.failed",
				Response: failedResponse,
				Error: &schema.ORErrorPayload{
					Type:    "server_error",
					Message: bgErr.Error(),
				},
			})
			return
		}
		if finalResponse != nil {
			store.UpdateResponse(responseID, finalResponse)
		}
	}()

	// Forward events from the store to the WebSocket connection
	forwardEvents(reqCtx, conn, store, responseID, processDone, shouldStore)
}

// forwardEvents subscribes to events for a response and sends them over the WebSocket.
// This mirrors handleStreamResume but writes JSON to WebSocket instead of SSE.
func forwardEvents(ctx context.Context, conn *lockedConn, store *ResponseStore, responseID string, done <-chan struct{}, shouldStore bool) {
	eventsChan, err := store.GetEventsChan(responseID)
	if err != nil {
		return
	}

	lastSeq := -1

	for {
		// Drain all available events
		events, err := store.GetEventsAfter(responseID, lastSeq)
		if err != nil {
			return
		}
		for _, event := range events {
			var parsed schema.ORStreamEvent
			if err := json.Unmarshal(event.Data, &parsed); err != nil {
				continue
			}
			if err := conn.writeJSON(&parsed); err != nil {
				return
			}
			lastSeq = event.SequenceNumber
		}

		// Check if processing is done and all events have been sent
		select {
		case <-done:
			// Drain any final events
			finalEvents, err := store.GetEventsAfter(responseID, lastSeq)
			if err == nil {
				for _, event := range finalEvents {
					var parsed schema.ORStreamEvent
					if err := json.Unmarshal(event.Data, &parsed); err != nil {
						continue
					}
					if err := conn.writeJSON(&parsed); err != nil {
						return
					}
				}
			}
			// Clean up non-stored responses from the cache
			if !shouldStore {
				store.Delete(responseID)
			}
			return
		default:
		}

		// Wait for new events, completion, or context cancellation
		select {
		case <-ctx.Done():
			return
		case <-done:
			// Will drain in next iteration
		case <-eventsChan:
			// New events available
		}
	}
}

func sendWSError(conn *lockedConn, errType, message, param string) {
	event := schema.ORStreamEvent{
		Type: "error",
		Error: &schema.ORErrorPayload{
			Type:    errType,
			Message: message,
			Param:   param,
		},
	}
	conn.writeJSON(&event)
}

func sendWSErrorEvent(conn *lockedConn, code, message, param string) {
	event := schema.ORStreamEvent{
		Type: "error",
		Error: &schema.ORErrorPayload{
			Type:    "invalid_request_error",
			Code:    code,
			Message: message,
			Param:   param,
		},
	}
	conn.writeJSON(&event)
}
