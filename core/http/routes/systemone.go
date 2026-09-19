package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
)

// RegisterSystemOneRoutes wires the kev-compatible SystemOne endpoints.
// These provide zero-shot structured extraction over arbitrary state text
// using a GLiNER2-backed NER model. The API mirrors the kev project
// (jaredpalmer/kev serve.py): POST /v1/systemone answers all questions in
// one NER pass; POST /v1/systemone/permute re-runs one choice question
// under n_perm option orders; POST /v1/systemone/separate answers each
// question in its own NER pass.
func RegisterSystemOneRoutes(e *echo.Echo, app *application.Application) {
	e.POST("/v1/systemone", localai.SystemOneEndpoint(app))
	e.POST("/v1/systemone/permute", localai.SystemOnePermuteEndpoint(app))
	e.POST("/v1/systemone/separate", localai.SystemOneSeparateEndpoint(app))
}
