// Package telemetry configures OpenTelemetry trace export for LocalAI.
//
// Export is strictly opt-in: LocalAI only creates a tracer provider and starts
// exporting when OTEL_TRACES_EXPORTER contains "otlp". Without that variable
// the package installs nothing, all spans are the OpenTelemetry no-op spans,
// and there is no network traffic — which keeps a stock LocalAI install and
// the test suite unaffected.
//
// The configuration is the standard OTLP environment contract
// (OTEL_EXPORTER_OTLP_*, OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES,
// OTEL_TRACES_SAMPLER), so an operator can point LocalAI at any OTLP
// collector without a LocalAI-specific knob. Plaintext OTLP is refused for
// non-loopback endpoints: trace payloads carry operation names and token
// counts, and silently downgrading to cleartext would be worse than dropping
// telemetry.
package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/xlog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/credentials"

	"github.com/mudler/LocalAI/internal"
)

// Environment variables read by ConfigFromEnv. The OTLP spec defines both a
// generic and a signal-specific form; the signal-specific one wins.
const (
	EnvTracesExporter = "OTEL_TRACES_EXPORTER"
	EnvSDKDisabled    = "OTEL_SDK_DISABLED"
	EnvServiceName    = "OTEL_SERVICE_NAME"

	envEndpoint          = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envTracesEndpoint    = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	envProtocol          = "OTEL_EXPORTER_OTLP_PROTOCOL"
	envTracesProtocol    = "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"
	envHeaders           = "OTEL_EXPORTER_OTLP_HEADERS"
	envTracesHeaders     = "OTEL_EXPORTER_OTLP_TRACES_HEADERS"
	envInsecure          = "OTEL_EXPORTER_OTLP_INSECURE"
	envTracesInsecure    = "OTEL_EXPORTER_OTLP_TRACES_INSECURE"
	envCertificate       = "OTEL_EXPORTER_OTLP_CERTIFICATE"
	envTracesCertificate = "OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE"
	envTimeout           = "OTEL_EXPORTER_OTLP_TIMEOUT"
	envTracesTimeout     = "OTEL_EXPORTER_OTLP_TRACES_TIMEOUT"
	envCompression       = "OTEL_EXPORTER_OTLP_COMPRESSION"
	envTracesCompression = "OTEL_EXPORTER_OTLP_TRACES_COMPRESSION"

	envResourceAttributes = "OTEL_RESOURCE_ATTRIBUTES"
	envTracesSampler      = "OTEL_TRACES_SAMPLER"
	envTracesSamplerArg   = "OTEL_TRACES_SAMPLER_ARG"
)

// instrumentationName is the tracer scope reported on every span LocalAI
// creates.
const instrumentationName = "github.com/mudler/LocalAI"

// Config is the resolved trace-export configuration.
type Config struct {
	// Enabled is false when no OTLP exporter was requested. The remaining
	// fields are only meaningful when it is true.
	Enabled     bool
	Protocol    string // "grpc" or "http/protobuf"
	Endpoint    string // host:port, scheme removed
	URLPath     string // path for the HTTP exporter, usually /v1/traces
	TLS         bool
	Insecure    bool
	CACert      string
	Headers     map[string]string
	Compression string
	Timeout     time.Duration

	ServiceName        string
	ResourceAttributes map[string]string
	Sampler            string
	SamplerArg         float64
}

var enabled atomic.Bool

// Enabled reports whether trace export is active. Callers use it to skip
// work that only exists to feed a span (per-token timestamps, attribute
// assembly) when telemetry is off.
//
// Setting a real SDK provider directly (tests, embedders) also counts: the
// global default is a proxy that is never an *sdktrace.TracerProvider.
func Enabled() bool {
	if enabled.Load() {
		return true
	}
	_, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	return ok
}

// Tracer returns the tracer used by LocalAI's instrumentation.
func Tracer() trace.Tracer { return otel.Tracer(instrumentationName) }

// Service owns the tracer provider for the lifetime of the process.
type Service struct {
	provider *sdktrace.TracerProvider
	cfg      Config
}

// Setup builds the tracer provider and installs it as the OTel global.
//
// A configuration error is returned without a usable Service; callers should
// log it and continue. Telemetry must never prevent LocalAI from serving.
func Setup(ctx context.Context) (*Service, error) {
	cfg, err := ConfigFromEnv(os.Getenv) //nolint:forbidigo // OTEL_* is the OpenTelemetry environment contract, read at process start like TZ; it is not LocalAI config to plumb through ApplicationConfig
	if err != nil {
		return nil, err
	}
	svc := &Service{cfg: cfg}
	if !cfg.Enabled {
		// Extraction still needs a propagator so an inbound traceparent from
		// a gateway is visible to code that reads the span context.
		otel.SetTextMapPropagator(newPropagator())
		return svc, nil
	}

	exp, err := newExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}

	res, err := newResource(ctx, cfg)
	if err != nil {
		_ = exp.Shutdown(ctx)
		return nil, err
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp,
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithMaxQueueSize(2048),
			sdktrace.WithMaxExportBatchSize(512),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(samplerFor(cfg.Sampler, cfg.SamplerArg)),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(newPropagator())
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		xlog.Warn("OpenTelemetry trace export error", "error", err)
	}))

	svc.provider = provider
	enabled.Store(true)
	xlog.Info("OpenTelemetry trace export enabled",
		"endpoint", cfg.Endpoint, "protocol", cfg.Protocol, "tls", cfg.TLS,
		"service", cfg.ServiceName, "sampler", cfg.Sampler)
	return svc, nil
}

// Shutdown flushes buffered spans and stops the exporter.
func (s *Service) Shutdown(ctx context.Context) error {
	if s == nil || s.provider == nil {
		return nil
	}
	enabled.Store(false)
	return s.provider.Shutdown(ctx)
}

// Config returns the resolved configuration. Exposed for diagnostics.
func (s *Service) Config() Config {
	if s == nil {
		return Config{}
	}
	return s.cfg
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

// ConfigFromEnv resolves the standard OTLP environment contract.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := Config{
		ServiceName:        "localai",
		ResourceAttributes: map[string]string{},
		Headers:            map[string]string{},
		Sampler:            "parentbased_always_on",
		Timeout:            10 * time.Second,
	}

	exporters := splitList(getenv(EnvTracesExporter))
	cfg.Enabled = contains(exporters, "otlp")
	if getenv(EnvSDKDisabled) == "true" {
		cfg.Enabled = false
	}
	if !cfg.Enabled {
		return cfg, nil
	}

	cfg.Protocol = strings.TrimSpace(firstNonEmpty(
		getenv(envTracesProtocol), getenv(envProtocol)))
	if cfg.Protocol == "" {
		// The OTLP spec default. An operator on a 4317 (gRPC) collector sets
		// OTEL_EXPORTER_OTLP_PROTOCOL=grpc explicitly.
		cfg.Protocol = "http/protobuf"
	}
	switch cfg.Protocol {
	case "grpc", "http/protobuf":
	default:
		return cfg, fmt.Errorf("unsupported OTLP protocol %q (want grpc or http/protobuf)", cfg.Protocol)
	}

	cfg.Insecure = parseBool(firstNonEmpty(getenv(envTracesInsecure), getenv(envInsecure)))
	cfg.CACert = firstNonEmpty(getenv(envTracesCertificate), getenv(envCertificate))
	cfg.Compression = firstNonEmpty(getenv(envTracesCompression), getenv(envCompression))

	if raw := firstNonEmpty(getenv(envTracesTimeout), getenv(envTimeout)); raw != "" {
		ms, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || ms <= 0 {
			return cfg, fmt.Errorf("invalid OTLP timeout %q: want milliseconds", raw)
		}
		cfg.Timeout = time.Duration(ms) * time.Millisecond
	}

	headers, err := parseKeyValues(firstNonEmpty(getenv(envTracesHeaders), getenv(envHeaders)))
	if err != nil {
		return cfg, fmt.Errorf("invalid OTLP headers: %w", err)
	}
	cfg.Headers = headers

	if name := strings.TrimSpace(getenv(EnvServiceName)); name != "" {
		cfg.ServiceName = name
	}
	attrs, err := parseKeyValues(getenv(envResourceAttributes))
	if err != nil {
		return cfg, fmt.Errorf("invalid OTEL_RESOURCE_ATTRIBUTES: %w", err)
	}
	cfg.ResourceAttributes = attrs
	for key, value := range attrs {
		switch key {
		case "service.name":
			cfg.ServiceName = value
		case "service.version":
			// The build's version wins: an operator cannot know the binary
			// version better than the binary itself.
			delete(attrs, key)
		}
	}

	if name := strings.TrimSpace(getenv(envTracesSampler)); name != "" {
		cfg.Sampler = name
	}
	if raw := strings.TrimSpace(getenv(envTracesSamplerArg)); raw != "" {
		arg, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return cfg, fmt.Errorf("invalid OTEL_TRACES_SAMPLER_ARG %q: %w", raw, err)
		}
		cfg.SamplerArg = arg
	}

	// The signal-specific endpoint is the complete URL for traces; the generic
	// one is a base URL that the signal path is appended to. The spec treats
	// them differently, so the source of the value matters.
	tracesEndpoint := strings.TrimSpace(getenv(envTracesEndpoint))
	endpoint := firstNonEmpty(tracesEndpoint, getenv(envEndpoint))
	hostPort, urlPath, useTLS, err := parseEndpoint(endpoint, cfg.Protocol, cfg.Insecure, tracesEndpoint != "")
	if err != nil {
		return cfg, err
	}
	cfg.Endpoint = hostPort
	cfg.URLPath = urlPath
	cfg.TLS = useTLS

	if !cfg.TLS {
		// Plaintext is only ever acceptable on loopback: trace payloads
		// carry operation names, model names and token counts, and a
		// cleartext export off the host would be worse than no export.
		host, _, splitErr := net.SplitHostPort(hostPort)
		if splitErr != nil || !isLoopback(host) {
			return cfg, fmt.Errorf("refusing plaintext OTLP to %q: plaintext is only allowed for a loopback collector, use https", hostPort)
		}
	}
	return cfg, nil
}

// parseEndpoint normalises an OTLP endpoint into host:port plus the HTTP path
// the exporter should post to. signalSpecific is true when the endpoint came
// from OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: its path is used as given. A generic
// OTEL_EXPORTER_OTLP_ENDPOINT is a base URL, so /v1/traces is appended to any
// path it carries. An endpoint without a scheme is treated as TLS: the common
// misconfiguration is a bare host, and failing closed beats silently shipping
// traces in cleartext.
func parseEndpoint(raw, protocol string, insecure, signalSpecific bool) (hostPort, urlPath string, useTLS bool, err error) {
	defaultPort := "4317"
	if protocol == "http/protobuf" {
		defaultPort = "4318"
	}
	if strings.TrimSpace(raw) == "" {
		// The OTLP spec default: a collector on the same host. Loopback is
		// the one place plaintext is allowed.
		raw = "http://localhost:" + defaultPort
	}
	// An endpoint without a scheme is treated as TLS: a bare host is the
	// common misconfiguration, and failing closed beats silently shipping
	// traces in cleartext. OTEL_EXPORTER_OTLP_INSECURE=true is the explicit
	// opt-out.
	schemeExplicit := strings.Contains(raw, "://")
	if !schemeExplicit {
		if insecure {
			raw = "http://" + raw
		} else {
			raw = "https://" + raw
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", false, fmt.Errorf("invalid OTLP endpoint %q: %w", raw, err)
	}
	switch u.Scheme {
	case "https":
		useTLS = true
	case "http":
		useTLS = false
	default:
		return "", "", false, fmt.Errorf("invalid OTLP endpoint scheme %q: want http or https", u.Scheme)
	}
	if insecure && schemeExplicit && useTLS {
		// Never silently downgrade an endpoint that says https.
		return "", "", false, fmt.Errorf("OTLP endpoint %q is https but the insecure flag asks for plaintext; remove one", raw)
	}
	hostPort = u.Host
	if _, _, splitErr := net.SplitHostPort(hostPort); splitErr != nil {
		hostPort = net.JoinHostPort(hostPort, defaultPort)
	}
	if u.Hostname() == "" {
		return "", "", false, fmt.Errorf("invalid OTLP endpoint %q: missing host", raw)
	}
	if protocol == "http/protobuf" {
		urlPath = strings.TrimSuffix(u.Path, "/")
		if !signalSpecific {
			urlPath += "/v1/traces"
		} else if urlPath == "" {
			urlPath = "/v1/traces"
		}
	}
	return hostPort, urlPath, useTLS, nil
}

func newExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	tlsConfig, err := clientTLSConfig(cfg)
	if err != nil {
		return nil, err
	}

	switch cfg.Protocol {
	case "grpc":
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
			otlptracegrpc.WithTimeout(cfg.Timeout),
			otlptracegrpc.WithHeaders(cfg.Headers),
		}
		if tlsConfig != nil {
			opts = append(opts, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(tlsConfig)))
		} else {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		if cfg.Compression == "gzip" {
			opts = append(opts, otlptracegrpc.WithCompressor("gzip"))
		}
		return otlptracegrpc.New(ctx, opts...)
	case "http/protobuf":
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(cfg.Endpoint),
			otlptracehttp.WithURLPath(cfg.URLPath),
			otlptracehttp.WithTimeout(cfg.Timeout),
			otlptracehttp.WithHeaders(cfg.Headers),
		}
		if tlsConfig != nil {
			opts = append(opts, otlptracehttp.WithTLSClientConfig(tlsConfig))
		} else {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		if cfg.Compression == "gzip" {
			opts = append(opts, otlptracehttp.WithCompression(otlptracehttp.GzipCompression))
		}
		return otlptracehttp.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("unsupported OTLP protocol %q", cfg.Protocol)
	}
}

// clientTLSConfig returns nil when the exporter should run without TLS.
// Verification is never disabled: an untrusted collector certificate is a
// misconfiguration to fix, not to skip past.
func clientTLSConfig(cfg Config) (*tls.Config, error) {
	if !cfg.TLS {
		return nil, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if cfg.CACert != "" {
		pem, err := os.ReadFile(cfg.CACert)
		if err != nil {
			return nil, fmt.Errorf("read OTLP CA certificate %q: %w", cfg.CACert, err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("OTLP CA certificate %q contains no PEM certificates", cfg.CACert)
		}
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    pool,
	}, nil
}

func newResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		attribute.String("service.name", cfg.ServiceName),
		attribute.String("service.version", serviceVersion()),
	}
	for key, value := range cfg.ResourceAttributes {
		if key == "service.name" || key == "service.version" {
			continue
		}
		attrs = append(attrs, attribute.String(key, value))
	}
	return resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithAttributes(attrs...),
	)
}

func samplerFor(name string, arg float64) sdktrace.Sampler {
	switch name {
	case "always_on":
		return sdktrace.AlwaysSample()
	case "always_off":
		return sdktrace.NeverSample()
	case "traceidratio":
		return sdktrace.TraceIDRatioBased(arg)
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample())
	case "parentbased_traceidratio":
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(arg))
	case "parentbased_always_on", "":
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	default:
		xlog.Warn("Unknown OTEL_TRACES_SAMPLER, using parentbased_always_on", "sampler", name)
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// parseKeyValues parses the comma-separated k=v form used by
// OTEL_RESOURCE_ATTRIBUTES and the OTLP headers variable.
func parseKeyValues(raw string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("entry %q is not key=value", pair)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("entry %q has an empty key", pair)
		}
		out[key] = strings.TrimSpace(value)
	}
	return out, nil
}

func isLoopback(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// serviceVersion is the release identity reported in service.version. A
// source build reports "dev" rather than the " ()" that PrintableVersion
// produces when the linker never stamped a version.
func serviceVersion() string {
	switch {
	case internal.Version == "":
		return "dev"
	case internal.Commit == "":
		return internal.Version
	default:
		return internal.Version + " (" + internal.Commit + ")"
	}
}
