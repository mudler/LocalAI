package http

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
)

// notFoundHandler answers a missing route with a JSON error. The main server
// has no HTML templates: its UI is the React app.
func notFoundHandler(c echo.Context) error {
	return c.JSON(http.StatusNotFound, schema.ErrorResponse{
		Error: &schema.APIError{Message: "Resource not found", Code: http.StatusNotFound},
	})
}
