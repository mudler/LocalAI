package telemetry

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

// recordingCollector is an OTLP/gRPC trace receiver that keeps every request
// it is sent, so the test can inspect what crossed the wire.
type recordingCollector struct {
	coltracepb.UnimplementedTraceServiceServer
	mu       sync.Mutex
	requests []*coltracepb.ExportTraceServiceRequest
}

func (c *recordingCollector) Export(_ context.Context, req *coltracepb.ExportTraceServiceRequest) (*coltracepb.ExportTraceServiceResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	return &coltracepb.ExportTraceServiceResponse{}, nil
}

var _ = Describe("OTLP export", func() {
	// The round trip runs over plaintext on loopback, the one path the
	// configuration allows without TLS. Test certificates are not generated
	// here: the TLS handshake is the collector's concern, and a self-signed
	// fixture is not something this repository creates.
	It("delivers a span to a loopback collector without payload attributes", func() {
		collector := &recordingCollector{}
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		server := grpc.NewServer()
		coltracepb.RegisterTraceServiceServer(server, collector)
		go func() { _ = server.Serve(lis) }()
		DeferCleanup(server.Stop)

		cfg := Config{
			Enabled:     true,
			Protocol:    "grpc",
			Endpoint:    lis.Addr().String(),
			TLS:         false,
			Headers:     map[string]string{},
			Timeout:     5 * time.Second,
			ServiceName: "localai-test",
		}
		exp, err := newExporter(context.Background(), cfg)
		Expect(err).NotTo(HaveOccurred())
		res, err := newResource(context.Background(), cfg)
		Expect(err).NotTo(HaveOccurred())

		provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
		DeferCleanup(func() {
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = provider.Shutdown(flushCtx)
		})

		_, span := provider.Tracer(instrumentationName).Start(context.Background(), "gen_ai.chat test-model",
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				attribute.String("gen_ai.operation.name", "chat"),
				attribute.Int("gen_ai.usage.input_tokens", 11),
			),
		)
		span.End()

		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		Expect(provider.ForceFlush(flushCtx)).To(Succeed())

		collector.mu.Lock()
		defer collector.mu.Unlock()
		Expect(collector.requests).NotTo(BeEmpty(),
			"collector received no export request; the OTLP/gRPC path did not deliver a span")

		var sawSpan, sawTokens, sawResource bool
		for _, req := range collector.requests {
			for _, rs := range req.GetResourceSpans() {
				for _, attr := range rs.GetResource().GetAttributes() {
					if attr.GetKey() == "service.name" && attr.GetValue().GetStringValue() == "localai-test" {
						sawResource = true
					}
				}
				for _, ss := range rs.GetScopeSpans() {
					for _, s := range ss.GetSpans() {
						if s.GetName() != "gen_ai.chat test-model" {
							continue
						}
						sawSpan = true
						for _, attr := range s.GetAttributes() {
							if attr.GetKey() == "gen_ai.usage.input_tokens" && attr.GetValue().GetIntValue() == 11 {
								sawTokens = true
							}
							Expect(strings.ToLower(attr.GetKey())).NotTo(ContainSubstring("prompt"),
								"payload attribute exported to the collector")
						}
					}
				}
			}
		}
		Expect(sawSpan).To(BeTrue(), "span was not exported")
		Expect(sawTokens).To(BeTrue(), "token usage was not exported")
		Expect(sawResource).To(BeTrue(), "service.name resource attribute was not exported")
	})
})
