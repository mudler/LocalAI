package telemetry

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// TestExportRoundTrip exercises the real OTLP/gRPC exporter against a local
// collector: TLS with a pinned development certificate, a span over the wire,
// and the no-body policy.
//
// The certificate is test-only material: ECDSA, self-signed, loopback-only,
// and named dev-selfsigned so it can never be mistaken for CA-issued material.
// It is never used outside this test.
func TestExportRoundTrip(t *testing.T) {
	certPEM, keyPEM := devSelfSignedCert(t)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("load test certificate: %v", err)
	}

	collector := &recordingCollector{}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	})))
	coltracepb.RegisterTraceServiceServer(server, collector)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	caPath := filepath.Join(t.TempDir(), "dev-selfsigned-ca.pem")
	if err := os.WriteFile(caPath, certPEM, 0o600); err != nil {
		t.Fatalf("write test CA: %v", err)
	}

	cfg := Config{
		Enabled:     true,
		Protocol:    "grpc",
		Endpoint:    lis.Addr().String(),
		TLS:         true,
		CACert:      caPath,
		Headers:     map[string]string{},
		Timeout:     5 * time.Second,
		ServiceName: "localai-test",
	}
	exp, err := newExporter(t.Context(), cfg)
	if err != nil {
		t.Fatalf("newExporter: %v", err)
	}
	res, err := newResource(t.Context(), cfg)
	if err != nil {
		t.Fatalf("newResource: %v", err)
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	t.Cleanup(func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = provider.Shutdown(flushCtx)
	})

	_, span := provider.Tracer(instrumentationName).Start(t.Context(), "gen_ai.chat test-model",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("gen_ai.operation.name", "chat"),
			attribute.Int("gen_ai.usage.input_tokens", 11),
		),
	)
	span.End()

	flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := provider.ForceFlush(flushCtx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	collector.mu.Lock()
	defer collector.mu.Unlock()
	if len(collector.requests) == 0 {
		t.Fatal("collector received no export request; the OTLP/gRPC path did not deliver a span")
	}

	var (
		sawSpan     bool
		sawTokens   bool
		sawResource bool
	)
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
						if strings.Contains(strings.ToLower(attr.GetKey()), "prompt") {
							t.Fatalf("payload attribute %q exported to the collector", attr.GetKey())
						}
					}
				}
			}
		}
	}
	if !sawSpan || !sawTokens || !sawResource {
		t.Fatalf("incomplete export: span=%v tokens=%v resource=%v", sawSpan, sawTokens, sawResource)
	}
}

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

// devSelfSignedCert returns a throwaway loopback certificate and its key.
func devSelfSignedCert(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "dev-selfsigned-otel-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}
