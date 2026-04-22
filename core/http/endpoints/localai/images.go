package localai

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/pkg/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// decodeImageInput resolves a URL, data-URI, or plain-string image
// input to a base64 payload ready for the gRPC surface. Errors from
// the underlying utils helper (bad URL, not a data-URI, download
// failure, etc.) are all caused by what the client sent — we surface
// them as 400 rather than the default 500 so API consumers can
// distinguish "you sent bad input" from "our server broke".
//
// This is the single-input path for endpoints where the image IS the
// request (detection, face recognition, etc.). The multi-modal message
// paths (chat completions, responses API, realtime) intentionally
// log-and-skip individual media parts; they don't use this helper.
func decodeImageInput(s string) (string, error) {
	img, err := utils.GetContentURIAsBase64(s)
	if err != nil {
		return "", echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("invalid image input: %v", err))
	}
	return img, nil
}

// mapBackendError converts the gRPC status code a backend returns into
// a matching HTTP status. Without this, every backend error defaults
// to 500 — which lies to API consumers when the backend is telling us
// "your input was bad" (INVALID_ARGUMENT) or "the resource doesn't
// exist" (NOT_FOUND). Pass any err from a `core/backend/*` call
// through this before returning from a handler.
func mapBackendError(err error) error {
	if err == nil {
		return nil
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.InvalidArgument:
			return echo.NewHTTPError(http.StatusBadRequest, st.Message())
		case codes.NotFound:
			return echo.NewHTTPError(http.StatusNotFound, st.Message())
		case codes.FailedPrecondition:
			return echo.NewHTTPError(http.StatusPreconditionFailed, st.Message())
		case codes.Unimplemented:
			return echo.NewHTTPError(http.StatusNotImplemented, st.Message())
		}
	}
	return err
}
