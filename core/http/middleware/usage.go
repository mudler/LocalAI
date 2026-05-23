package middleware

import (
	"bytes"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

const (
	usageFlushInterval = 5 * time.Second
	// usageMaxPending bounds the in-memory queue. Sized for bursty inference
	// traffic on a self-hosted instance with a slow or unavailable DB.
	usageMaxPending = 50000
)

// usageBatcher accumulates usage records and flushes them to the DB periodically.
type usageBatcher struct {
	mu       sync.Mutex
	pending  []*auth.UsageRecord
	db       *gorm.DB
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// droppedRecords counts records discarded because the in-memory queue was full.
// Used to rate-limit the warn log so a sustained outage doesn't flood it.
var droppedRecords atomic.Uint64

func (b *usageBatcher) add(r *auth.UsageRecord) {
	b.mu.Lock()
	if len(b.pending) >= usageMaxPending {
		b.mu.Unlock()
		// Rate-limit: one warn per 1024 drops keeps the log readable.
		n := droppedRecords.Add(1)
		if n&1023 == 1 {
			xlog.Warn("usage batcher full, dropping record",
				"cap", usageMaxPending, "total_dropped", n)
		}
		return
	}
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
		// Cap-aware re-queue: prepend as much of the failed batch as fits
		// alongside any records added concurrently with the failed write.
		b.mu.Lock()
		room := usageMaxPending - len(b.pending)
		if room > 0 {
			if room > len(batch) {
				room = len(batch)
			}
			b.pending = append(batch[:room], b.pending...)
		}
		b.mu.Unlock()
	}
}

func (b *usageBatcher) run() {
	defer close(b.done)
	ticker := time.NewTicker(usageFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			b.flush()
		case <-b.stop:
			b.flush() // final drain
			return
		}
	}
}

func (b *usageBatcher) shutdown() {
	b.stopOnce.Do(func() {
		close(b.stop)
		<-b.done
	})
}

// The package-level batcher is guarded by batcherMu so Init / Shutdown cycles
// (the test pattern) don't race against UsageMiddleware reads.
var (
	batcherMu sync.RWMutex
	batcher   *usageBatcher
)

func currentBatcher() *usageBatcher {
	batcherMu.RLock()
	defer batcherMu.RUnlock()
	return batcher
}

// InitUsageRecorder starts a background goroutine that periodically flushes
// accumulated usage records to the database. Calling it more than once
// shuts down the previous batcher first so its goroutine doesn't leak.
func InitUsageRecorder(db *gorm.DB) {
	if db == nil {
		return
	}

	batcherMu.Lock()
	old := batcher
	batcher = nil
	batcherMu.Unlock()
	if old != nil {
		old.shutdown()
	}

	b := &usageBatcher{
		db:   db,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	batcherMu.Lock()
	batcher = b
	batcherMu.Unlock()

	go b.run()
}

// ShutdownUsageRecorder stops the background flusher and synchronously drains
// pending records once. Safe to call multiple times. Not yet wired into the
// application lifecycle; intended for graceful process exit and tests.
func ShutdownUsageRecorder() {
	batcherMu.Lock()
	b := batcher
	batcher = nil
	batcherMu.Unlock()
	if b != nil {
		b.shutdown()
	}
}

// FlushNow synchronously flushes any pending usage records. Intended for tests
// that need deterministic behaviour without waiting for the ticker.
func FlushNow() {
	if b := currentBatcher(); b != nil {
		b.flush()
	}
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
			b := currentBatcher()
			if db == nil || b == nil {
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

			source := auth.GetSource(c)
			if source == "" {
				// Auth disabled or unrecognised path: classify as web so the row is still
				// bucketable rather than silently dropped from per-source aggregates.
				source = auth.UsageSourceWeb
			}

			record := &auth.UsageRecord{
				UserID:           user.ID,
				UserName:         user.Name,
				Source:           source,
				Model:            resp.Model,
				Endpoint:         c.Request().URL.Path,
				PromptTokens:     resp.Usage.PromptTokens,
				CompletionTokens: resp.Usage.CompletionTokens,
				TotalTokens:      resp.Usage.TotalTokens,
				Duration:         time.Since(startTime).Milliseconds(),
				CreatedAt:        startTime,
			}

			if key := auth.GetAPIKey(c); key != nil {
				id := key.ID
				record.APIKeyID = &id
				record.APIKeyName = key.Name
			}

			b.add(record)

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
