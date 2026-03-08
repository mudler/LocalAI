package cambai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
)

// AudioSeparationEndpoint returns 501 Not Implemented for audio separation.
func AudioSeparationEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusNotImplemented, schema.CambAIErrorResponse{
			Detail: "Audio separation is not currently supported. No backend available.",
		})
	}
}
