package localai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/services/agents"
	coreTypes "github.com/mudler/LocalAGI/core/types"
	"github.com/mudler/xlog"
	"github.com/sashabaranov/go-openai"
)

// agentResponsesRequest is the minimal subset of the OpenResponses request body
// needed to route to an agent.
type agentResponsesRequest struct {
	Model              string            `json:"model"`
	Input              json.RawMessage   `json:"input"`
	PreviousResponseID string            `json:"previous_response_id,omitempty"`
	Tools              []json.RawMessage `json:"tools,omitempty"`
	ToolChoice         json.RawMessage   `json:"tool_choice,omitempty"`
}

// AgentResponsesInterceptor returns a middleware that intercepts /v1/responses
// requests when the model name matches an agent in the pool. If no agent matches,
// it restores the request body and falls through to the normal responses pipeline.
func AgentResponsesInterceptor(app *application.Application) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			svc := app.AgentPoolService()
			if svc == nil {
				return next(c)
			}

			// Read and buffer the body so we can peek at the model name
			body, err := io.ReadAll(c.Request().Body)
			if err != nil {
				return next(c)
			}
			// Always restore the body for the next handler
			c.Request().Body = io.NopCloser(bytes.NewReader(body))

			var req agentResponsesRequest
			if err := json.Unmarshal(body, &req); err != nil || req.Model == "" {
				return next(c)
			}

			// Check if this model name is an agent
			messages := parseInputToMessages(req.Input)
			if len(messages) == 0 {
				// Can't determine if this is an agent without input
				ag := svc.GetAgent(req.Model)
				if ag == nil {
					return next(c)
				}
			}

			// Extract the last user message for the executor
			var userMessage string
			for i := len(messages) - 1; i >= 0; i-- {
				if messages[i].Role == "user" {
					userMessage = messages[i].Content
					break
				}
			}

			var responseText string

			// Distributed mode: dispatch via NATS + wait for response synchronously
			if svc.IsDistributed() {
				store := app.AgentStore()
				bridge := app.AgentEventBridge()
				if store == nil || bridge == nil {
					return next(c)
				}
				userID := effectiveUserID(c)
				rec, err := store.GetConfig(userID, req.Model)
				if err != nil || rec == nil {
					return next(c) // not an agent
				}

				// Dispatch via ChatForUser (publishes to NATS) and wait for the response via EventBridge
				messageID, err := svc.ChatForUser(userID, req.Model, userMessage)
				if err != nil {
					return c.JSON(http.StatusInternalServerError, map[string]any{
						"error": map[string]string{"type": "server_error", "message": err.Error()},
					})
				}

				// Subscribe to events and wait for the agent's response message
				ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Minute)
				defer cancel()

				responseCh := make(chan string, 1)
				sub, err := bridge.SubscribeEvents(req.Model, userID, func(evt agents.AgentEvent) {
					if evt.EventType == "json_message" && evt.Sender == "agent" {
						responseCh <- evt.Content
					}
				})
				if err != nil {
					return c.JSON(http.StatusInternalServerError, map[string]any{
						"error": map[string]string{"type": "server_error", "message": "failed to subscribe to agent events"},
					})
				}
				defer sub.Unsubscribe()

				select {
				case responseText = <-responseCh:
					// Got the response
				case <-ctx.Done():
					return c.JSON(http.StatusGatewayTimeout, map[string]any{
						"error": map[string]string{"type": "server_error", "message": "agent response timeout"},
					})
				}

				_ = messageID
			} else {
				// Standalone mode: use LocalAGI agent directly
				ag := svc.GetAgent(req.Model)
				if ag == nil {
					return next(c)
				}

				jobOptions := []coreTypes.JobOption{
					coreTypes.WithConversationHistory(messages),
				}

				res := ag.Ask(jobOptions...)
				if res == nil {
					return c.JSON(http.StatusInternalServerError, map[string]any{
						"error": map[string]string{
							"type":    "server_error",
							"message": "agent request failed or was cancelled",
						},
					})
				}
				if res.Error != nil {
					xlog.Error("Error asking agent via responses API", "agent", req.Model, "error", res.Error)
					return c.JSON(http.StatusInternalServerError, map[string]any{
						"error": map[string]string{
							"type":    "server_error",
							"message": res.Error.Error(),
						},
					})
				}
				responseText = res.Response
			}

			id := fmt.Sprintf("resp_%s", uuid.New().String())

			return c.JSON(http.StatusOK, map[string]any{
				"id":                  id,
				"object":              "response",
				"created_at":          time.Now().Unix(),
				"status":              "completed",
				"model":               req.Model,
				"previous_response_id": nil,
				"output": []any{
					map[string]any{
						"type":   "message",
						"id":     fmt.Sprintf("msg_%d", time.Now().UnixNano()),
						"status": "completed",
						"role":   "assistant",
						"content": []map[string]any{
							{
								"type":        "output_text",
								"text":        responseText,
								"annotations": []any{},
							},
						},
					},
				},
			})
		}
	}
}

// parseInputToMessages converts the raw JSON input (string or message array) to openai messages.
func parseInputToMessages(raw json.RawMessage) []openai.ChatCompletionMessage {
	if len(raw) == 0 {
		return nil
	}

	// Try as string first
	var text string
	if err := json.Unmarshal(raw, &text); err == nil && text != "" {
		return []openai.ChatCompletionMessage{
			{Role: "user", Content: text},
		}
	}

	// Try as array of message objects
	var messages []struct {
		Type      string          `json:"type,omitempty"`
		Role      string          `json:"role,omitempty"`
		Content   json.RawMessage `json:"content,omitempty"`
		CallId    string          `json:"call_id,omitempty"`
		Name      string          `json:"name,omitempty"`
		Arguments string          `json:"arguments,omitempty"`
		Output    string          `json:"output,omitempty"`
	}
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil
	}

	var result []openai.ChatCompletionMessage
	for _, m := range messages {
		switch m.Type {
		case "function_call":
			result = append(result, openai.ChatCompletionMessage{
				Role: "assistant",
				ToolCalls: []openai.ToolCall{
					{
						Type: "function",
						ID:   m.CallId,
						Function: openai.FunctionCall{
							Arguments: m.Arguments,
							Name:      m.Name,
						},
					},
				},
			})
		case "function_call_output":
			if m.CallId != "" && m.Output != "" {
				result = append(result, openai.ChatCompletionMessage{
					Role:       "tool",
					Content:    m.Output,
					ToolCallID: m.CallId,
				})
			}
		default:
			if m.Role == "" {
				continue
			}
			content := parseMessageContent(m.Content)
			if content != "" {
				result = append(result, openai.ChatCompletionMessage{
					Role:    m.Role,
					Content: content,
				})
			}
		}
	}
	return result
}

// parseMessageContent extracts text from either a string or array of content items.
func parseMessageContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var items []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}
	if err := json.Unmarshal(raw, &items); err == nil {
		for _, item := range items {
			if item.Type == "text" || item.Type == "input_text" {
				return item.Text
			}
		}
	}
	return ""
}
