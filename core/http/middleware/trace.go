package middleware

import (
	"bytes"
	"io"
	"net/http"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/rs/zerolog/log"
)

type APIExchange struct {
	Request struct {
		Method  string
		Path    string
		Headers http.Header
		Body    []byte
	}
	Response struct {
		Status  int
		Headers http.Header
		Body    []byte
	}
}

var apiLogs []APIExchange
var mu sync.Mutex
var logChan = make(chan APIExchange, 100) // Buffered channel for serialization

func init() {
	go func() {
		for exchange := range logChan {
			mu.Lock()
			apiLogs = append(apiLogs, exchange)
			mu.Unlock()
			log.Debug().Msgf("Logged exchange: %s %s - Status: %d", exchange.Request.Method, exchange.Request.Path, exchange.Response.Status)
		}
	}()
}

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
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !app.ApplicationConfig().EnableTracing {
				return next(c)
			}

			// Only log if Content-Type is application/json
			if c.Request().Header.Get("Content-Type") != "application/json" {
				return next(c)
			}

			body, err := io.ReadAll(c.Request().Body)
			if err != nil {
				log.Error().Err(err).Msg("Failed to read request body")
				return err
			}

			// Restore the body for downstream handlers
			c.Request().Body = io.NopCloser(bytes.NewBuffer(body))

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
			exchange := APIExchange{
				Request: struct {
					Method  string
					Path    string
					Headers http.Header
					Body    []byte
				}{
					Method:  c.Request().Method,
					Path:    c.Path(),
					Headers: c.Request().Header.Clone(),
					Body:    body,
				},
				Response: struct {
					Status  int
					Headers http.Header
					Body    []byte
				}{
					Status:  c.Response().Status,
					Headers: c.Response().Header().Clone(),
					Body:    resBody.Bytes(),
				},
			}

			// Send to channel (non-blocking)
			select {
			case logChan <- exchange:
			default:
				log.Warn().Msg("API log channel full, dropping log")
			}

			return nil
		}
	}
}

// GetAPILogs returns a copy of the logged API exchanges for display
func GetAPILogs() []APIExchange {
	mu.Lock()
	defer mu.Unlock()
	return append([]APIExchange{}, apiLogs...)
}

// ClearAPILogs clears the in-memory logs
func ClearAPILogs() {
	mu.Lock()
	apiLogs = nil
	mu.Unlock()
}
