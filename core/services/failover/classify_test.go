package failover

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

var _ = DescribeTable("IsRetryable",
	func(err error, status int, want bool) {
		Expect(IsRetryable(err, status)).To(Equal(want))
	},
	Entry("nil error, no status", nil, 0, false),
	Entry("held 503", nil, http.StatusServiceUnavailable, true),
	Entry("held 500", nil, http.StatusInternalServerError, true),
	Entry("held 501", nil, http.StatusNotImplemented, false),
	Entry("client cancel", context.Canceled, 0, false),
	Entry("wrapped client cancel", fmt.Errorf("predict: %w", context.Canceled), 0, false),
	Entry("deadline", context.DeadlineExceeded, 0, true),
	Entry("echo 502", echo.NewHTTPError(http.StatusBadGateway, "x"), 0, true),
	Entry("echo 400", echo.NewHTTPError(http.StatusBadRequest, "x"), 0, false),
	Entry("echo 404", echo.NewHTTPError(http.StatusNotFound, "x"), 0, false),
	Entry("grpc unavailable", grpcstatus.Error(codes.Unavailable, "x"), 0, true),
	Entry("grpc internal", grpcstatus.Error(codes.Internal, "x"), 0, true),
	Entry("grpc deadline", grpcstatus.Error(codes.DeadlineExceeded, "x"), 0, true),
	Entry("grpc unknown", grpcstatus.Error(codes.Unknown, "x"), 0, true),
	Entry("grpc invalid argument", grpcstatus.Error(codes.InvalidArgument, "x"), 0, false),
	Entry("grpc resource exhausted (rate limit, OOM)", grpcstatus.Error(codes.ResourceExhausted, "x"), 0, true),
	Entry("cloud-proxy upstream 503", errors.New("cloud-proxy: upstream 503: no healthy nodes"), 0, true),
	Entry("cloud-proxy upstream 429 stays 4xx", errors.New("cloud-proxy: upstream 429: slow down"), 0, false),
	Entry("context overflow", errors.New("the request exceeds the available context size"), 0, false),
	Entry("dial error", errors.New("dial tcp 10.0.0.1:8080: connect: connection refused"), 0, true),
)

var _ = DescribeTable("IsCapabilityGap",
	func(err error, want bool) {
		Expect(IsCapabilityGap(err)).To(Equal(want))
	},
	Entry("nil error", nil, false),
	Entry("grpc unimplemented", grpcstatus.Error(codes.Unimplemented, "x"), true),
	Entry("wrapped grpc unimplemented", fmt.Errorf("call: %w", grpcstatus.Error(codes.Unimplemented, "x")), true),
	Entry("echo 501 (a handler that mapped Unimplemented)", echo.NewHTTPError(http.StatusNotImplemented, "x"), true),
	Entry("echo 500 is a failure, not a gap", echo.NewHTTPError(http.StatusInternalServerError, "x"), false),
	Entry("grpc resource exhausted is a failure, not a gap", grpcstatus.Error(codes.ResourceExhausted, "x"), false),
	Entry("grpc unavailable", grpcstatus.Error(codes.Unavailable, "x"), false),
	Entry("plain error", errors.New("boom"), false),
)
