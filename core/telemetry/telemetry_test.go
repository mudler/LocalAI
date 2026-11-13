package telemetry

import (
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/internal"
)

// envMap turns a map into the getenv function ConfigFromEnv expects.
func envMap(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestConfigDisabledByDefault(t *testing.T) {
	cfg, err := ConfigFromEnv(envMap(nil))
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("trace export must be off unless OTEL_TRACES_EXPORTER asks for otlp")
	}
}

func TestConfigEnabledByExporter(t *testing.T) {
	cfg, err := ConfigFromEnv(envMap(map[string]string{
		EnvTracesExporter: "otlp",
		envTracesEndpoint: "https://otel.example.test:4317",
		EnvServiceName:    "localai-test",
		envTracesSampler:  "always_on",
	}))
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if !cfg.Enabled || cfg.Protocol != "grpc" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.Endpoint != "otel.example.test:4317" || !cfg.TLS {
		t.Fatalf("endpoint not normalised: %+v", cfg)
	}
	if cfg.ServiceName != "localai-test" || cfg.Sampler != "always_on" {
		t.Fatalf("service/sampler not applied: %+v", cfg)
	}
}

// TestConfigRefusesPlaintextRemote is the safety property that matters most:
// trace payloads never leave the process unencrypted to a remote collector.
func TestConfigRefusesPlaintextRemote(t *testing.T) {
	_, err := ConfigFromEnv(envMap(map[string]string{
		EnvTracesExporter: "otlp",
		envTracesEndpoint: "http://otel.example.test:4317",
	}))
	if err == nil {
		t.Fatal("plaintext OTLP to a non-loopback endpoint must be refused")
	}
	if !strings.Contains(err.Error(), "plaintext") {
		t.Fatalf("error should name the problem, got %v", err)
	}
}

func TestConfigAllowsLoopbackPlaintext(t *testing.T) {
	cfg, err := ConfigFromEnv(envMap(map[string]string{
		EnvTracesExporter: "otlp",
	}))
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.TLS {
		t.Fatal("the default localhost collector should not require TLS")
	}
	if cfg.Endpoint != "localhost:4317" {
		t.Fatalf("unexpected default endpoint %q", cfg.Endpoint)
	}
}

func TestConfigBareHostMeansTLS(t *testing.T) {
	cfg, err := ConfigFromEnv(envMap(map[string]string{
		EnvTracesExporter: "otlp",
		envTracesEndpoint: "otel.example.test:4317",
	}))
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if !cfg.TLS {
		t.Fatal("an endpoint without a scheme must be treated as TLS, not plaintext")
	}
}

func TestConfigHTTPProtocol(t *testing.T) {
	cfg, err := ConfigFromEnv(envMap(map[string]string{
		EnvTracesExporter: "otlp",
		envTracesProtocol: "http/protobuf",
		envTracesEndpoint: "https://otel.example.test",
	}))
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.Endpoint != "otel.example.test:4318" {
		t.Fatalf("expected the HTTP default port, got %q", cfg.Endpoint)
	}
	if cfg.URLPath != "/v1/traces" {
		t.Fatalf("expected /v1/traces, got %q", cfg.URLPath)
	}
}

func TestConfigHeadersAndResourceAttributes(t *testing.T) {
	cfg, err := ConfigFromEnv(envMap(map[string]string{
		EnvTracesExporter:     "otlp",
		envTracesEndpoint:     "https://otel.example.test:4317",
		envTracesHeaders:      "authorization=Bearer abc,x-tenant=a",
		envResourceAttributes: "deployment.environment.name=staging,service.name=override",
	}))
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.Headers["authorization"] != "Bearer abc" || cfg.Headers["x-tenant"] != "a" {
		t.Fatalf("headers not parsed: %+v", cfg.Headers)
	}
	if cfg.ServiceName != "override" {
		t.Fatalf("service.name from resource attributes should win, got %q", cfg.ServiceName)
	}
	if cfg.ResourceAttributes["deployment.environment.name"] != "staging" {
		t.Fatalf("resource attributes not parsed: %+v", cfg.ResourceAttributes)
	}
}

func TestConfigServiceVersionIsNotOverridable(t *testing.T) {
	cfg, err := ConfigFromEnv(envMap(map[string]string{
		EnvTracesExporter:     "otlp",
		envTracesEndpoint:     "https://otel.example.test:4317",
		envResourceAttributes: "service.version=spoofed",
	}))
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if _, ok := cfg.ResourceAttributes["service.version"]; ok {
		t.Fatal("the binary's own version must not be overridden by the environment")
	}
}

func TestConfigSDKDisabledWins(t *testing.T) {
	cfg, err := ConfigFromEnv(envMap(map[string]string{
		EnvTracesExporter: "otlp",
		EnvSDKDisabled:    "true",
	}))
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("OTEL_SDK_DISABLED=true must turn export off")
	}
}

func TestConfigRejectsUnknownProtocol(t *testing.T) {
	if _, err := ConfigFromEnv(envMap(map[string]string{
		EnvTracesExporter: "otlp",
		envTracesProtocol: "carrier-pigeon",
	})); err == nil {
		t.Fatal("unknown protocol should be rejected")
	}
}

func TestConfigRejectsBadSamplerArg(t *testing.T) {
	if _, err := ConfigFromEnv(envMap(map[string]string{
		EnvTracesExporter:   "otlp",
		envTracesSampler:    "traceidratio",
		envTracesSamplerArg: "not-a-number",
	})); err == nil {
		t.Fatal("non-numeric sampler arg should be rejected")
	}
}

func TestSamplerFor(t *testing.T) {
	for _, name := range []string{
		"always_on", "always_off", "traceidratio",
		"parentbased_always_on", "parentbased_always_off", "parentbased_traceidratio",
		"", "something-unknown",
	} {
		if samplerFor(name, 0.5) == nil {
			t.Fatalf("sampler for %q is nil", name)
		}
	}
}

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		raw      string
		protocol string
		insecure bool
		host     string
		path     string
		tls      bool
		wantErr  bool
	}{
		{raw: "https://otel.example.test:4317", protocol: "grpc", host: "otel.example.test:4317", tls: true},
		{raw: "otel.example.test", protocol: "grpc", host: "otel.example.test:4317", tls: true},
		{raw: "http://127.0.0.1:4317", protocol: "grpc", host: "127.0.0.1:4317"},
		{raw: "https://otel.example.test", protocol: "http/protobuf", host: "otel.example.test:4318", path: "/v1/traces", tls: true},
		{raw: "https://otel.example.test/custom/v1/traces", protocol: "http/protobuf", host: "otel.example.test:4318", path: "/custom/v1/traces", tls: true},
		{raw: "otel.example.test:4317", protocol: "grpc", insecure: true, host: "otel.example.test:4317"},
		// An endpoint that says https must not be silently downgraded by the
		// insecure flag; that combination is a configuration conflict.
		{raw: "https://otel.example.test:4317", protocol: "grpc", insecure: true, wantErr: true},
		{raw: "ftp://otel.example.test", protocol: "grpc", wantErr: true},
	}
	for _, tc := range tests {
		host, path, tls, err := parseEndpoint(tc.raw, tc.protocol, tc.insecure)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%q: expected an error", tc.raw)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", tc.raw, err)
		}
		if host != tc.host || path != tc.path || tls != tc.tls {
			t.Fatalf("%q: got (%q, %q, %v), want (%q, %q, %v)", tc.raw, host, path, tls, tc.host, tc.path, tc.tls)
		}
	}
}

func TestParseKeyValues(t *testing.T) {
	got, err := parseKeyValues("a=1,b=two,empty=")
	if err != nil {
		t.Fatalf("parseKeyValues: %v", err)
	}
	if got["a"] != "1" || got["b"] != "two" || got["empty"] != "" {
		t.Fatalf("unexpected map %+v", got)
	}
	if _, err := parseKeyValues("broken"); err == nil {
		t.Fatal("an entry without = should be rejected")
	}
	if _, err := parseKeyValues("=value"); err == nil {
		t.Fatal("an empty key should be rejected")
	}
}

func TestServiceVersion(t *testing.T) {
	savedVersion, savedCommit := internal.Version, internal.Commit
	defer func() { internal.Version, internal.Commit = savedVersion, savedCommit }()

	internal.Version, internal.Commit = "", ""
	if serviceVersion() != "dev" {
		t.Fatalf("source build should report dev, got %q", serviceVersion())
	}
	internal.Version, internal.Commit = "v4.12.0", "abc123"
	if serviceVersion() != "v4.12.0 (abc123)" {
		t.Fatalf("unexpected version %q", serviceVersion())
	}
}

// TestConfigTimeout keeps the millisecond parsing honest: a seconds value
// here would silently make every export time out.
func TestConfigTimeout(t *testing.T) {
	cfg, err := ConfigFromEnv(envMap(map[string]string{
		EnvTracesExporter: "otlp",
		envTracesEndpoint: "https://otel.example.test:4317",
		envTracesTimeout:  "2500",
	}))
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.Timeout != 2500*time.Millisecond {
		t.Fatalf("unexpected timeout %v", cfg.Timeout)
	}
}
