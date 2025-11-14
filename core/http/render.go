package http

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/Masterminds/sprig/v3"
	"github.com/labstack/echo/v4"
	"github.com/microcosm-cc/bluemonday"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/russross/blackfriday"
)

//go:embed views/*
var viewsfs embed.FS

// TemplateRenderer is a custom template renderer for Echo
type TemplateRenderer struct {
	templates *template.Template
}

// Render renders a template document
func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

func notFoundHandler(c echo.Context) error {
	// Check if the request accepts JSON
	contentType := c.Request().Header.Get("Content-Type")
	accept := c.Request().Header.Get("Accept")
	if strings.Contains(contentType, "application/json") || !strings.Contains(accept, "text/html") {
		// The client expects a JSON response
		return c.JSON(http.StatusNotFound, schema.ErrorResponse{
			Error: &schema.APIError{Message: "Resource not found", Code: http.StatusNotFound},
		})
	} else {
		// The client expects an HTML response
		return c.Render(http.StatusNotFound, "views/404", map[string]interface{}{
			"BaseURL": middleware.BaseURL(c),
		})
	}
}

func renderEngine() *TemplateRenderer {
	// Parse all templates from embedded filesystem
	tmpl := template.New("").Funcs(sprig.FuncMap())
	tmpl = tmpl.Funcs(template.FuncMap{
		"MDToHTML": markDowner,
	})

	// Recursively walk through embedded filesystem and parse all HTML templates
	err := fs.WalkDir(viewsfs, "views", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".html") {
			data, err := viewsfs.ReadFile(path)
			if err == nil {
				// Remove .html extension to get template name (e.g., "views/index.html" -> "views/index")
				templateName := strings.TrimSuffix(path, ".html")
				_, err := tmpl.New(templateName).Parse(string(data))
				if err != nil {
					// If parsing fails, try parsing without explicit name (for templates with {{define}})
					tmpl.Parse(string(data))
				}
			}
		}
		return nil
	})
	if err != nil {
		// Log error but continue - templates might still work
		fmt.Printf("Error walking views directory: %v\n", err)
	}

	return &TemplateRenderer{
		templates: tmpl,
	}
}

func markDowner(args ...interface{}) template.HTML {
	s := blackfriday.MarkdownCommon([]byte(fmt.Sprintf("%s", args...)))
	return template.HTML(bluemonday.UGCPolicy().Sanitize(string(s)))
}
