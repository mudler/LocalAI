package cloudproxy

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	corebackend "github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	pkggrpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// ForwardViaBackend loads the cloud-proxy gRPC backend, ships the
// request via the Forward RPC, and pumps the response back to the
// client. PII redaction runs request-side (the NER middleware + MITM
// input path); the response is forwarded unmodified.
func ForwardViaBackend(
	c echo.Context,
	cfg *config.ModelConfig,
	body []byte,
	loader *model.ModelLoader,
	appConfig *config.ApplicationConfig,
) (resultErr error) {
	// Passthrough forwards bypass core/backend/llm.go and therefore its
	// trace.RecordBackendTrace call — instrument here so passthrough
	// requests show up in the Traces UI alongside translate-mode ones.
	// Named return is unusual for this package but lets the defer capture
	// the final error across the function's many early-return paths
	// without rewriting them.
	var startTime time.Time
	statusCode := 0
	if appConfig.EnableTracing {
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		startTime = time.Now()
	}
	defer func() {
		if !appConfig.EnableTracing {
			return
		}
		errStr := ""
		if resultErr != nil {
			errStr = resultErr.Error()
		}
		data := map[string]any{
			"mode":           cfg.Proxy.Mode,
			"provider":       cfg.Proxy.Provider,
			"upstream":       cfg.Proxy.UpstreamURL,
			"upstream_model": cfg.Proxy.UpstreamModel,
		}
		if statusCode != 0 {
			data["status"] = statusCode
		}
		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: startTime,
			Duration:  time.Since(startTime),
			Type:      trace.BackendTraceLLM,
			ModelName: cfg.Name,
			Backend:   cfg.Backend,
			Summary:   trace.TruncateBytes(body, 200),
			Body:      trace.TruncateBytes(body, trace.MaxTraceBodyBytes),
			Error:     errStr,
			Data:      data,
		})
	}()

	if cfg.Proxy.UpstreamURL == "" {
		return echo.NewHTTPError(http.StatusInternalServerError,
			fmt.Sprintf("cloudproxy: proxy.upstream_url empty for model %q", cfg.Name))
	}

	body, err := rewriteModel(body, cfg.Proxy.UpstreamModel)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	opts := corebackend.ModelOptions(*cfg, appConfig)
	inferenceModel, err := loader.Load(opts...)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "cloudproxy: load cloud-proxy backend: "+err.Error())
	}
	be, ok := inferenceModel.(pkggrpc.Backend)
	if !ok {
		return echo.NewHTTPError(http.StatusInternalServerError, "cloudproxy: cloud-proxy backend doesn't speak gRPC")
	}

	ctx := c.Request().Context()
	stream, err := be.Forward(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, "cloudproxy: open Forward stream: "+err.Error())
	}

	// Single request message — first carries path/method/headers + the
	// full body. Cloud-proxy's upstream_url has the canonical path so
	// the Path field is informational; backend uses upstream_url.
	if err := stream.Send(&pb.ForwardRequest{
		Path:      "",
		Method:    http.MethodPost,
		Headers:   []*pb.ForwardHeader{{Name: "Content-Type", Value: "application/json"}},
		BodyChunk: body,
	}); err != nil {
		_ = stream.CloseSend()
		return echo.NewHTTPError(http.StatusBadGateway, "cloudproxy: send request: "+err.Error())
	}
	if err := stream.CloseSend(); err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, "cloudproxy: close send: "+err.Error())
	}

	// First reply carries status + response headers. Subsequent replies
	// carry body chunks. Wrap the remaining stream as an io.Reader so
	// the existing forwardStream / forwardBuffered code paths apply
	// unchanged.
	first, err := stream.Recv()
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, "cloudproxy: recv first reply: "+err.Error())
	}

	statusCode = int(first.GetStatus())
	contentType := ""
	for _, h := range first.GetHeaders() {
		if h != nil && h.GetName() != "" && http.CanonicalHeaderKey(h.GetName()) == "Content-Type" {
			contentType = h.GetValue()
			break
		}
	}
	bodyReader := &forwardReader{stream: stream}

	isStream := streaming(body)
	logFn := xlog.Info
	if statusCode >= 400 {
		logFn = xlog.Warn
	}
	logFn("cloudproxy: forwarding via backend",
		"model", cfg.Name,
		"upstream", cfg.Proxy.UpstreamURL,
		"upstream_model", cfg.Proxy.UpstreamModel,
		"status", statusCode,
		"stream", isStream)

	if statusCode >= 400 {
		return passthroughError(c, statusCode, contentType, bodyReader)
	}
	if isStream {
		return forwardStream(c, bodyReader)
	}
	return forwardBuffered(c, statusCode, contentType, bodyReader)
}

// forwardReader adapts a Backend_ForwardClient into an io.ReadCloser.
// Each ForwardReply carries a chunk of the upstream body; we accumulate
// into a single buffer and serve it through Read.
type forwardReader struct {
	stream pkggrpc.ForwardClient
	pos    int
	buf    []byte
	err    error
}

func (r *forwardReader) Read(p []byte) (int, error) {
	if r.err != nil && r.pos >= len(r.buf) {
		return 0, r.err
	}
	if r.pos >= len(r.buf) {
		// Need a new chunk.
		reply, err := r.stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				r.err = io.EOF
				return 0, io.EOF
			}
			r.err = err
			return 0, err
		}
		r.buf = reply.GetBodyChunk()
		r.pos = 0
		if len(r.buf) == 0 {
			// Zero-length chunk — try again rather than returning 0
			// (some readers treat that as EOF).
			return r.Read(p)
		}
	}
	n := copy(p, r.buf[r.pos:])
	r.pos += n
	return n, nil
}

func (r *forwardReader) Close() error {
	// Drain any remaining replies so the server-side goroutine isn't
	// left blocked. The stream is request-scoped; when the parent
	// context is cancelled (handler returns), Recv returns and we
	// exit. A misbehaving backend that keeps emitting replies after
	// cancellation is bounded by the iteration cap.
	for i := 0; i < 1024; i++ {
		if _, err := r.stream.Recv(); err != nil {
			return nil
		}
		if r.stream.Context().Err() != nil {
			return nil
		}
	}
	return nil
}
