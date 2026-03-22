package middleware

import (
	"bytes"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/emirpasic/gods/v2/queues/circularbuffer"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/xlog"
)

type APIExchangeRequest struct {
	Method  string       `json:"method"`
	Path    string       `json:"path"`
	Headers *http.Header `json:"headers"`
	Body    *[]byte      `json:"body"`
}

type APIExchangeResponse struct {
	Status  int          `json:"status"`
	Headers *http.Header `json:"headers"`
	Body    *[]byte      `json:"body"`
}

type APIExchange struct {
	Timestamp time.Time           `json:"timestamp"`
	Duration  time.Duration       `json:"duration"`
	Request   APIExchangeRequest  `json:"request"`
	Response  APIExchangeResponse `json:"response"`
	Error     string              `json:"error,omitempty"`
	UserID    string              `json:"user_id,omitempty"`
	UserName  string              `json:"user_name,omitempty"`
}

var traceBuffer *circularbuffer.Queue[APIExchange]
var mu sync.Mutex
var logChan = make(chan APIExchange, 100)
var initOnce sync.Once

type bodyWriter struct {
	http.ResponseWriter
	body *bytes.Buffer
}

func (w *bodyWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *bodyWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func initializeTracing(maxItems int) {
	initOnce.Do(func() {
		if maxItems <= 0 {
			maxItems = 100
		}
		mu.Lock()
		traceBuffer = circularbuffer.New[APIExchange](maxItems)
		mu.Unlock()

		go func() {
			for exchange := range logChan {
				mu.Lock()
				if traceBuffer != nil {
					traceBuffer.Enqueue(exchange)
				}
				mu.Unlock()
			}
		}()
	})
}

// TraceMiddleware intercepts and logs JSON API requests and responses
func TraceMiddleware(app *application.Application) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !app.ApplicationConfig().EnableTracing {
				return next(c)
			}

			initializeTracing(app.ApplicationConfig().TracingMaxItems)

			if c.Request().Header.Get("Content-Type") != "application/json" {
				return next(c)
			}

			body, err := io.ReadAll(c.Request().Body)
			if err != nil {
				xlog.Error("Failed to read request body")
				return err
			}

			// Restore the body for downstream handlers
			c.Request().Body = io.NopCloser(bytes.NewBuffer(body))

			startTime := time.Now()

			// Wrap response writer to capture body
			resBody := new(bytes.Buffer)
			mw := &bodyWriter{
				ResponseWriter: c.Response().Writer,
				body:           resBody,
			}
			c.Response().Writer = mw

			handlerErr := next(c)

			// Restore original writer unconditionally
			c.Response().Writer = mw.ResponseWriter

			// Determine response status (use 500 if handler errored and no status was set)
			status := c.Response().Status
			if status == 0 && handlerErr != nil {
				status = http.StatusInternalServerError
			}

			// Create exchange log (always, even on error)
			requestHeaders := c.Request().Header.Clone()
			requestBody := make([]byte, len(body))
			copy(requestBody, body)
			responseHeaders := c.Response().Header().Clone()
			responseBody := make([]byte, resBody.Len())
			copy(responseBody, resBody.Bytes())
			exchange := APIExchange{
				Timestamp: startTime,
				Duration:  time.Since(startTime),
				Request: APIExchangeRequest{
					Method:  c.Request().Method,
					Path:    c.Path(),
					Headers: &requestHeaders,
					Body:    &requestBody,
				},
				Response: APIExchangeResponse{
					Status:  status,
					Headers: &responseHeaders,
					Body:    &responseBody,
				},
			}
			if handlerErr != nil {
				exchange.Error = handlerErr.Error()
			}

			if user := auth.GetUser(c); user != nil {
				exchange.UserID = user.ID
				exchange.UserName = user.Name
			}

			select {
			case logChan <- exchange:
			default:
				xlog.Warn("Trace channel full, dropping trace")
			}

			return handlerErr
		}
	}
}

// GetTraces returns a copy of the logged API exchanges for display
func GetTraces() []APIExchange {
	mu.Lock()
	if traceBuffer == nil {
		mu.Unlock()
		return []APIExchange{}
	}
	traces := traceBuffer.Values()
	mu.Unlock()

	sort.Slice(traces, func(i, j int) bool {
		return traces[i].Timestamp.After(traces[j].Timestamp)
	})

	return traces
}

// ClearTraces clears the in-memory logs
func ClearTraces() {
	mu.Lock()
	if traceBuffer != nil {
		traceBuffer.Clear()
	}
	mu.Unlock()
}
