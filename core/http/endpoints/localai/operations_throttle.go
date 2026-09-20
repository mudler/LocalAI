package localai

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/xlog"
)

// ThrottleResponse is the JSON payload for the throttle endpoint.
type ThrottleResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ThrottleOperationEndpoint throttles an active gallery download without restarting it.
//
// @Summary      Throttle an active gallery download to a byte-per-second rate
// @Description  Throttle an active gallery download without restarting it. The limit applies to in-flight reads immediately and can be changed or cleared at any time. Use rate=0 to remove the limit.
// @Tags         operations
// @Accept       json
// @Produce      json
// @Param        jobID  path  string  true  "Operation job ID"
// @Param        rate   query string  true  "Human-readable rate (e.g. 2mb, 500kb) or 0 to remove the limit"
// @Success      200  {object}  ThrottleResponse  "success and message with the applied bytes/sec rate"
// @Failure      400  {object}  map[string]string  "missing rate, invalid rate, or unknown operation"
// @Router       /api/operations/{jobID}/throttle [post]
func ThrottleOperationEndpoint(galleryService *galleryop.GalleryService) echo.HandlerFunc {
	return func(c echo.Context) error {
		jobID := c.Param("jobID")
		rateStr := c.QueryParam("rate")
		if rateStr == "" {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": "query parameter 'rate' is required (e.g. rate=2mb, rate=500kb)",
			})
		}
		bytesPerSec, err := downloader.ParseRateString(rateStr)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": fmt.Sprintf("invalid rate %q: %v", rateStr, err),
			})
		}

		xlog.Debug("API request to throttle operation", "jobID", jobID, "rate", bytesPerSec)
		if err := galleryService.SetOperationRateLimit(jobID, bytesPerSec); err != nil {
			xlog.Error("Failed to throttle operation", "error", err, "jobID", jobID)
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": err.Error(),
			})
		}

		return c.JSON(http.StatusOK, ThrottleResponse{
			Success: true,
			Message: fmt.Sprintf("Operation throttled to %d bytes/sec", bytesPerSec),
		})
	}
}
