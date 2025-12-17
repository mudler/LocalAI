package middleware

import (
	"bytes"
	"github.com/emirpasic/gods/v2/queues/circularbuffer"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
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
	Request   APIExchangeRequest  `json:"request"`
	Response  APIExchangeResponse `json:"response"`
}

var traceBuffer *circularbuffer.Queue[APIExchange]
var mu sync.Mutex
var logChan = make(chan APIExchange, 100)

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

// TraceMiddleware intercepts and logs JSON API requests and responses
func TraceMiddleware(app *application.Application) echo.MiddlewareFunc {
	if app.ApplicationConfig().EnableTracing && traceBuffer == nil {
		traceBuffer = circularbuffer.New[APIExchange](app.ApplicationConfig().TracingMaxItems)

		go func() {
			for exchange := range logChan {
				mu.Lock()
				traceBuffer.Enqueue(exchange)
				mu.Unlock()
			}
		}()
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !app.ApplicationConfig().EnableTracing {
				return next(c)
			}

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

			err = next(c)
			if err != nil {
				c.Response().Writer = mw.ResponseWriter // Restore original writer if error
				return err
			}

			// Create exchange log
			requestHeaders := c.Request().Header.Clone()
			requestBody := make([]byte, len(body))
			copy(requestBody, body)
			responseHeaders := c.Response().Header().Clone()
			responseBody := make([]byte, resBody.Len())
			copy(responseBody, resBody.Bytes())
			exchange := APIExchange{
				Timestamp: startTime,
				Request: APIExchangeRequest{
					Method:  c.Request().Method,
					Path:    c.Path(),
					Headers: &requestHeaders,
					Body:    &requestBody,
				},
				Response: APIExchangeResponse{
					Status:  c.Response().Status,
					Headers: &responseHeaders,
					Body:    &responseBody,
				},
			}

			select {
			case logChan <- exchange:
			default:
				xlog.Warn("Trace channel full, dropping trace")
			}

			return nil
		}
	}
}

// GetTraces returns a copy of the logged API exchanges for display
func GetTraces() []APIExchange {
	mu.Lock()
	traces := traceBuffer.Values()
	mu.Unlock()

	sort.Slice(traces, func(i, j int) bool {
		return traces[i].Timestamp.Before(traces[j].Timestamp)
	})

	return traces
}

// ClearTraces clears the in-memory logs
func ClearTraces() {
	mu.Lock()
	traceBuffer.Clear()
	mu.Unlock()
}
