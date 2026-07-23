package localai

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/trace"
)

// DefaultTraceListLimit bounds the trace list responses. The ring buffer holds
// up to LOCALAI_TRACING_MAX_ITEMS entries (1024 in a typical deployment) and
// each one can embed a full request/response payload, so returning the whole
// buffer made /api/traces a multi-megabyte response that the admin UI
// re-fetched on every poll. Callers that genuinely want everything can pass
// limit=0.
const DefaultTraceListLimit = 50

// MaxTraceListLimit caps an explicit limit so a client cannot ask for an
// unbounded page by accident.
const MaxTraceListLimit = 1000

// tracePageParams reads the limit/offset/full query parameters. Invalid values
// fall back to the bounded defaults rather than erroring, so existing clients
// keep working.
func tracePageParams(c echo.Context) (offset, limit int, full bool) {
	limit = DefaultTraceListLimit
	if raw := c.QueryParam("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			limit = n
		}
	}
	if limit > MaxTraceListLimit {
		limit = MaxTraceListLimit
	}
	if raw := c.QueryParam("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			offset = n
		}
	}
	if raw := c.QueryParam("full"); raw != "" {
		full, _ = strconv.ParseBool(raw)
	}
	return offset, limit, full
}

// setTracePageHeaders publishes the paging metadata out-of-band so the
// response body stays a plain JSON array for existing consumers.
func setTracePageHeaders(c echo.Context, total, offset, limit int) {
	h := c.Response().Header()
	h.Set("X-Total-Count", strconv.Itoa(total))
	h.Set("X-Trace-Offset", strconv.Itoa(offset))
	h.Set("X-Trace-Limit", strconv.Itoa(limit))
}

func traceNotFound(c echo.Context) error {
	return c.JSON(http.StatusNotFound, schema.ErrorResponse{
		Error: &schema.APIError{Message: "trace not found", Code: http.StatusNotFound},
	})
}

// GetAPITracesEndpoint returns a bounded page of API request/response traces
// @Summary List API request/response traces
// @Description Returns a bounded, newest-first page of captured API exchange traces. Request and response bodies plus headers are omitted unless full=true; fetch them per-trace from /api/traces/{id}. Paging metadata is returned in the X-Total-Count, X-Trace-Offset and X-Trace-Limit headers.
// @Tags monitoring
// @Produce json
// @Param limit query int false "Maximum entries to return (default 50, max 1000, 0 for all)"
// @Param offset query int false "Number of entries to skip (default 0)"
// @Param full query bool false "Include request/response bodies and headers (default false)"
// @Success 200 {object} map[string]any "Traced API exchanges"
// @Router /api/traces [get]
func GetAPITracesEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		offset, limit, full := tracePageParams(c)
		page, total := middleware.GetTracesPage(offset, limit)
		if !full {
			for i := range page {
				page[i] = middleware.SummarizeExchange(page[i])
			}
		}
		setTracePageHeaders(c, total, offset, limit)
		return c.JSON(http.StatusOK, page)
	}
}

// GetAPITraceEndpoint returns a single API trace with its full payload
// @Summary Get one API trace
// @Description Returns a single captured API exchange, including the request and response bodies omitted from the list response
// @Tags monitoring
// @Produce json
// @Param id path string true "Trace ID"
// @Success 200 {object} map[string]any "Traced API exchange"
// @Failure 404 {object} schema.ErrorResponse "Trace not found"
// @Router /api/traces/{id} [get]
func GetAPITraceEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		exchange, ok := middleware.GetTrace(c.Param("id"))
		if !ok {
			return traceNotFound(c)
		}
		return c.JSON(http.StatusOK, exchange)
	}
}

// ClearAPITracesEndpoint clears all API traces
// @Summary Clear API traces
// @Description Removes all captured API request/response traces from the buffer
// @Tags monitoring
// @Success 204 "Traces cleared"
// @Router /api/traces/clear [post]
func ClearAPITracesEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		middleware.ClearTraces()
		return c.NoContent(http.StatusNoContent)
	}
}

// GetBackendTracesEndpoint returns a bounded page of backend operation traces
// @Summary List backend operation traces
// @Description Returns a bounded, newest-first page of captured backend traces (LLM calls, embeddings, TTS, etc). The heavy body and data fields are omitted unless full=true; fetch them per-trace from /api/backend-traces/{id}. Paging metadata is returned in the X-Total-Count, X-Trace-Offset and X-Trace-Limit headers.
// @Tags monitoring
// @Produce json
// @Param limit query int false "Maximum entries to return (default 50, max 1000, 0 for all)"
// @Param offset query int false "Number of entries to skip (default 0)"
// @Param full query bool false "Include the body and data payloads (default false)"
// @Success 200 {object} map[string]any "Backend operation traces"
// @Router /api/backend-traces [get]
func GetBackendTracesEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		offset, limit, full := tracePageParams(c)
		page, total := trace.GetBackendTracesPage(offset, limit)
		if !full {
			for i := range page {
				page[i] = trace.SummarizeBackendTrace(page[i])
			}
		}
		setTracePageHeaders(c, total, offset, limit)
		return c.JSON(http.StatusOK, page)
	}
}

// GetBackendTraceEndpoint returns a single backend trace with its full payload
// @Summary Get one backend operation trace
// @Description Returns a single captured backend trace, including the body and data payloads omitted from the list response
// @Tags monitoring
// @Produce json
// @Param id path string true "Trace ID"
// @Success 200 {object} map[string]any "Backend operation trace"
// @Failure 404 {object} schema.ErrorResponse "Trace not found"
// @Router /api/backend-traces/{id} [get]
func GetBackendTraceEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		t, ok := trace.GetBackendTrace(c.Param("id"))
		if !ok {
			return traceNotFound(c)
		}
		return c.JSON(http.StatusOK, t)
	}
}

// ClearBackendTracesEndpoint clears all backend traces
// @Summary Clear backend traces
// @Description Removes all captured backend operation traces from the buffer
// @Tags monitoring
// @Success 204 "Traces cleared"
// @Router /api/backend-traces/clear [post]
func ClearBackendTracesEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		trace.ClearBackendTraces()
		return c.NoContent(http.StatusNoContent)
	}
}
