package http

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
)

// The only server-rendered page left is the one of the standalone Explorer
// server (`local-ai explorer`). The main server has no HTML templates: its UI
// is the React app.
//
//go:embed views/*
var viewsfs embed.FS

// TemplateRenderer is a custom template renderer for Echo
type TemplateRenderer struct {
	templates *template.Template
}

// Render renders a template document
func (t *TemplateRenderer) Render(w io.Writer, name string, data any, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

// notFoundHandler answers a missing route with a JSON error.
func notFoundHandler(c echo.Context) error {
	return c.JSON(http.StatusNotFound, schema.ErrorResponse{
		Error: &schema.APIError{Message: "Resource not found", Code: http.StatusNotFound},
	})
}

func renderEngine() *TemplateRenderer {
	// Parse all templates from embedded filesystem
	tmpl := template.New("")

	// Recursively walk through embedded filesystem and parse all HTML templates
	err := fs.WalkDir(viewsfs, "views", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".html") {
			data, err := viewsfs.ReadFile(path)
			if err == nil {
				// Remove .html extension to get template name (e.g., "views/explorer.html" -> "views/explorer")
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
