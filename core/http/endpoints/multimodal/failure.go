// SPDX-License-Identifier: MIT

// Package multimodal holds what the embedding and rerank endpoints share for
// typed multimodal requests: the mapping from backend and acquisition errors to
// HTTP statuses. Only fixed codes reach a response, never user input.
package multimodal

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/embeddingmedia"
	"github.com/mudler/LocalAI/pkg/model"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// statusClientClosed is the conventional status for a request the client
// abandoned. Nobody reads it, but it keeps cancellation out of the 5xx counters.
const statusClientClosed = 499

// Message prefixes a backend uses to refine a gRPC status code. The message
// text itself never reaches the client.
const (
	backendUnsupportedMediaFormat = "unsupported_media_format"
	backendLimitExceeded          = "limit_exceeded"
)

// grpcStatus returns the gRPC status carried by err, possibly wrapped. The
// status package reports a wrapped error's whole text as the message, so the
// status error is found with errors.As first.
func grpcStatus(err error) (*status.Status, bool) {
	var carrier interface{ GRPCStatus() *status.Status }
	if errors.As(err, &carrier) {
		if st := carrier.GRPCStatus(); st != nil {
			return st, true
		}
	}
	return nil, false
}

// hasReason reports whether msg starts with reason and the reason ends there:
// at the end of the message, or before ':' or a space. A longer word that only
// starts with the reason is a different reason.
func hasReason(msg, reason string) bool {
	rest, ok := strings.CutPrefix(msg, reason)
	return ok && (rest == "" || rest[0] == ':' || rest[0] == ' ')
}

// Failure classifies an error without ever echoing user input, URLs
// or backend text. Only fixed codes reach the response.
func Failure(c echo.Context, err error) error {
	var httpErr *echo.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	// Capacity, cooldown and loading errors are answers to retry. The
	// application error handler maps them to 429/503 with Retry-After, exactly
	// as for the legacy path, so they leave here unchanged.
	var admission *backend.BackendAdmissionError
	var cooldown *model.ModelLoadCooldownError
	var loading *nodes.ModelLoadingError
	if errors.As(err, &admission) || errors.As(err, &cooldown) || errors.As(err, &loading) || errors.Is(err, nodes.ErrNoAvailableNodes) {
		return err
	}
	var media *embeddingmedia.Error
	if errors.As(err, &media) {
		return echo.NewHTTPError(media.Status, media.Code)
	}
	if backend.IsEmbeddingPoolingCompatibilityError(err) {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	var typed *backend.MultimodalEmbeddingError
	if errors.As(err, &typed) {
		return echo.NewHTTPError(typed.StatusCode(), typed.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) || c.Request().Context().Err() == context.DeadlineExceeded {
		return echo.NewHTTPError(http.StatusGatewayTimeout, "embedding_deadline_exceeded")
	}
	// 499 only when this client actually went away. A cancellation that
	// originates in the backend or the session is a backend failure.
	clientGone := c.Request().Context().Err() != nil
	if errors.Is(err, context.Canceled) && clientGone {
		return echo.NewHTTPError(statusClientClosed, "request_canceled")
	}
	if st, ok := grpcStatus(err); ok {
		switch st.Code() {
		case codes.InvalidArgument:
			if hasReason(st.Message(), backendUnsupportedMediaFormat) {
				return echo.NewHTTPError(http.StatusUnsupportedMediaType, "unsupported_media_format")
			}
			return echo.NewHTTPError(http.StatusBadRequest, "invalid_embedding_input")
		case codes.ResourceExhausted:
			if hasReason(st.Message(), backendLimitExceeded) {
				return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "embedding_resource_limit")
			}
			return echo.NewHTTPError(http.StatusTooManyRequests, "embedding_backend_busy")
		case codes.OutOfRange:
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "embedding_resource_limit")
		case codes.FailedPrecondition:
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "embedding_unsupported_modality")
		case codes.Unimplemented:
			return echo.NewHTTPError(http.StatusNotImplemented, "multimodal_embedding_unavailable")
		case codes.DeadlineExceeded:
			return echo.NewHTTPError(http.StatusGatewayTimeout, "embedding_deadline_exceeded")
		}
	}
	return echo.NewHTTPError(http.StatusBadGateway, "embedding_backend_failure")
}
