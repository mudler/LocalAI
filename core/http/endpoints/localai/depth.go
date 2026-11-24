package localai

import (
	"encoding/base64"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// DepthEndpoint is the LocalAI Depth endpoint exposing the full Depth Anything 3
// output (per-pixel metric depth + confidence + sky, camera pose, 3D point cloud
// and optional glb/COLMAP exports).
// @Summary Estimates per-pixel depth (and optionally pose/points) from an image.
// @Tags depth
// @Param request body schema.DepthRequest true "query params"
// @Success 200 {object} schema.DepthResponse "Response"
// @Router /v1/depth [post]
func DepthEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {

		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.DepthRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		xlog.Debug("Depth", "image", input.Image, "backend", cfg.Backend)

		image, err := decodeImageInput(input.Image)
		if err != nil {
			return err
		}

		// Default to returning everything the model can produce when the
		// caller hasn't asked for any specific subset, so a bare request is
		// still useful.
		includeDepth := input.IncludeDepth
		includeConfidence := input.IncludeConfidence
		includePose := input.IncludePose
		includeSky := input.IncludeSky
		includePoints := input.IncludePoints
		if !includeDepth && !includeConfidence && !includePose && !includeSky && !includePoints {
			includeDepth = true
			includeConfidence = true
			includePose = true
			includeSky = true
		}

		req := &proto.DepthRequest{
			Src:               image,
			Dst:               input.Dst,
			IncludeDepth:      includeDepth,
			IncludeConfidence: includeConfidence,
			IncludePose:       includePose,
			IncludeSky:        includeSky,
			IncludePoints:     includePoints,
			PointsConfThresh:  input.PointsConfThresh,
			Exports:           input.Exports,
		}

		res, err := backend.Depth(c.Request().Context(), req, ml, appConfig, *cfg)
		if err != nil {
			return mapBackendError(err)
		}

		response := schema.DepthResponse{
			Width:       res.GetWidth(),
			Height:      res.GetHeight(),
			Depth:       res.GetDepth(),
			Confidence:  res.GetConfidence(),
			Sky:         res.GetSky(),
			Extrinsics:  res.GetExtrinsics(),
			Intrinsics:  res.GetIntrinsics(),
			NumPoints:   res.GetNumPoints(),
			Points:      res.GetPoints(),
			ExportPaths: res.GetExportPaths(),
			IsMetric:    res.GetIsMetric(),
		}
		if len(res.GetPointColors()) > 0 {
			response.PointColors = base64.StdEncoding.EncodeToString(res.GetPointColors())
		}

		return c.JSON(200, response)
	}
}
