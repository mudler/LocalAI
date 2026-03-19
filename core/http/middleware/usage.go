package middleware

import (
	"bytes"
	"encoding/json"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

const (
	usageFlushInterval = 5 * time.Second
	usageMaxPending    = 5000
)

// usageBatcher accumulates usage records and flushes them to the DB periodically.
type usageBatcher struct {
	mu      sync.Mutex
	pending []*auth.UsageRecord
	db      *gorm.DB
}

func (b *usageBatcher) add(r *auth.UsageRecord) {
	b.mu.Lock()
	b.pending = append(b.pending, r)
	b.mu.Unlock()
}

func (b *usageBatcher) flush() {
	b.mu.Lock()
	batch := b.pending
	b.pending = nil
	b.mu.Unlock()

	if len(batch) == 0 {
		return
	}

	if err := b.db.Create(&batch).Error; err != nil {
		xlog.Error("Failed to flush usage batch", "count", len(batch), "error", err)
		// Re-queue failed records with a cap to avoid unbounded growth
		b.mu.Lock()
		if len(b.pending) < usageMaxPending {
			b.pending = append(batch, b.pending...)
		}
		b.mu.Unlock()
	}
}

var batcher *usageBatcher

// InitUsageRecorder starts a background goroutine that periodically flushes
// accumulated usage records to the database.
func InitUsageRecorder(db *gorm.DB) {
	if db == nil {
		return
	}
	batcher = &usageBatcher{db: db}
	go func() {
		ticker := time.NewTicker(usageFlushInterval)
		defer ticker.Stop()
		for range ticker.C {
			batcher.flush()
		}
	}()
}

// usageResponseBody is the minimal structure we need from the response JSON.
type usageResponseBody struct {
	Model string `json:"model"`
	Usage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
	} `json:"usage"`
}

// UsageMiddleware extracts token usage from OpenAI-compatible response JSON
// and records it per-user.
func UsageMiddleware(db *gorm.DB) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if db == nil || batcher == nil {
				return next(c)
			}

			startTime := time.Now()

			// Wrap response writer to capture body
			resBody := new(bytes.Buffer)
			origWriter := c.Response().Writer
			mw := &bodyWriter{
				ResponseWriter: origWriter,
				body:           resBody,
			}
			c.Response().Writer = mw

			handlerErr := next(c)

			// Restore original writer
			c.Response().Writer = origWriter

			// Only record on successful responses
			if c.Response().Status < 200 || c.Response().Status >= 300 {
				return handlerErr
			}

			// Get authenticated user
			user := auth.GetUser(c)
			if user == nil {
				return handlerErr
			}

			// Try to parse usage from response
			responseBytes := resBody.Bytes()
			if len(responseBytes) == 0 {
				return handlerErr
			}

			// Check content type
			ct := c.Response().Header().Get("Content-Type")
			isJSON := ct == "" || ct == "application/json" || bytes.HasPrefix([]byte(ct), []byte("application/json"))
			isSSE := bytes.HasPrefix([]byte(ct), []byte("text/event-stream"))

			if !isJSON && !isSSE {
				return handlerErr
			}

			var resp usageResponseBody
			if isSSE {
				last, ok := lastSSEData(responseBytes)
				if !ok {
					return handlerErr
				}
				if err := json.Unmarshal(last, &resp); err != nil {
					return handlerErr
				}
			} else {
				if err := json.Unmarshal(responseBytes, &resp); err != nil {
					return handlerErr
				}
			}

			if resp.Usage == nil {
				return handlerErr
			}

			record := &auth.UsageRecord{
				UserID:           user.ID,
				UserName:         user.Name,
				Model:            resp.Model,
				Endpoint:         c.Request().URL.Path,
				PromptTokens:     resp.Usage.PromptTokens,
				CompletionTokens: resp.Usage.CompletionTokens,
				TotalTokens:      resp.Usage.TotalTokens,
				Duration:         time.Since(startTime).Milliseconds(),
				CreatedAt:        startTime,
			}

			batcher.add(record)

			return handlerErr
		}
	}
}

// lastSSEData returns the payload of the last "data: " line whose content is not "[DONE]".
func lastSSEData(b []byte) ([]byte, bool) {
	prefix := []byte("data: ")
	var last []byte
	for _, line := range bytes.Split(b, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if bytes.HasPrefix(line, prefix) {
			payload := line[len(prefix):]
			if !bytes.Equal(payload, []byte("[DONE]")) {
				last = payload
			}
		}
	}
	return last, last != nil
}
