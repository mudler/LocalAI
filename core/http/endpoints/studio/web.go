package studio

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
)

// AdminTestWebSearch runs one web search with the saved settings.
// @Summary Run a test web search with the saved settings (administrators).
// @Description Runs the query with the search provider of the built-in web_search tool (DuckDuckGo, or the SearXNG instance of chats_web_searxng_url) and returns the first three results, or the error a user would read. Save the settings first. The search counts against the rate limits like any other.
// @Tags chats
// @Param request body schema.ChatsWebTestRequest true "the query (default LocalAI)"
// @Success 200 {object} schema.ChatsWebTestResponse "The provider and its results, or the error"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 403 {object} schema.ErrorResponse "Not an administrator"
// @Failure 503 {object} schema.ErrorResponse "The built-in web tools are not available on this instance"
// @Router /api/chats/admin/web/test [post]
func (h *Handlers) AdminTestWebSearch(c echo.Context) error {
	var req schema.ChatsWebTestRequest
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, "the body must be a JSON object")
	}
	q := strings.TrimSpace(req.Query)
	if q == "" {
		q = "LocalAI"
	}
	provider, host, results, err := h.svc.TestWebSearch(c.Request().Context(), userID(c), q)
	if errors.Is(err, studiosvc.ErrWebOff) {
		return fail(c, http.StatusServiceUnavailable, "unavailable", "the built-in web tools are not available on this instance")
	}
	out := schema.ChatsWebTestResponse{OK: err == nil, Provider: provider, Host: host, Results: make([]schema.ChatsWebResult, 0, len(results))}
	if err != nil {
		out.Error = err.Error()
	}
	for _, r := range results {
		out.Results = append(out.Results, schema.ChatsWebResult{Title: r.Title, URL: r.URL, Snippet: r.Snippet})
	}
	return c.JSON(http.StatusOK, out)
}
