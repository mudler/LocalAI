package http

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"

	"github.com/Masterminds/sprig/v3"
	"github.com/gofiber/fiber/v2"
	fiberhtml "github.com/gofiber/template/html/v2"
	"github.com/microcosm-cc/bluemonday"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/russross/blackfriday"
)

//go:embed views/*
var viewsfs embed.FS

func notFoundHandler(c *fiber.Ctx) error {
	// Check if the request accepts JSON
	if string(c.Context().Request.Header.ContentType()) == "application/json" || len(c.Accepts("html")) == 0 {
		// The client expects a JSON response
		return c.Status(fiber.StatusNotFound).JSON(schema.ErrorResponse{
			Error: &schema.APIError{Message: "Resource not found", Code: fiber.StatusNotFound},
		})
	} else {
		// The client expects an HTML response
		return c.Status(fiber.StatusNotFound).Render("views/404", fiber.Map{})
	}
}

func renderEngine() *fiberhtml.Engine {
	engine := fiberhtml.NewFileSystem(http.FS(viewsfs), ".html")
	engine.AddFuncMap(sprig.FuncMap())
	engine.AddFunc("MDToHTML", markDowner)
	return engine
}

func markDowner(args ...interface{}) template.HTML {
	s := blackfriday.MarkdownCommon([]byte(fmt.Sprintf("%s", args...)))
	return template.HTML(bluemonday.UGCPolicy().Sanitize(string(s)))
}
