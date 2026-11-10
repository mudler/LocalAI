package routes

import (
	"net/http"
	"net/url"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/middleware"
)

// The server-rendered model pages were replaced by the React app. These
// handlers keep the old URLs working: they redirect to the React route and
// keep the reverse-proxy path prefix.

// ImportModelRedirect sends the old /import-model page to the React import page.
func ImportModelRedirect(c echo.Context) error {
	return c.Redirect(http.StatusMovedPermanently, middleware.BasePathPrefix(c)+"app/import-model")
}

// EditModelRedirect sends the old /models/edit/:name page to the React model editor.
func EditModelRedirect(c echo.Context) error {
	name := c.Param("name")
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	return c.Redirect(http.StatusMovedPermanently, middleware.BasePathPrefix(c)+"app/model-editor/"+url.PathEscape(name))
}
