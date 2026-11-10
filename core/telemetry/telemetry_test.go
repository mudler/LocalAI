package telemetry

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/internal"
)

// envMap turns a map into the getenv function ConfigFromEnv expects.
func envMap(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

var _ = Describe("OTLP configuration", func() {
	Describe("ConfigFromEnv", func() {
		It("keeps trace export off unless OTEL_TRACES_EXPORTER asks for otlp", func() {
			cfg, err := ConfigFromEnv(envMap(nil))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Enabled).To(BeFalse(), "trace export must be off unless OTEL_TRACES_EXPORTER asks for otlp")
		})

		It("resolves endpoint, protocol, service name and sampler when enabled", func() {
			cfg, err := ConfigFromEnv(envMap(map[string]string{
				EnvTracesExporter: "otlp",
				envTracesProtocol: "grpc",
				envTracesEndpoint: "https://otel.example.test:4317",
				EnvServiceName:    "localai-test",
				envTracesSampler:  "always_on",
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Enabled).To(BeTrue())
			Expect(cfg.Protocol).To(Equal("grpc"))
			Expect(cfg.Endpoint).To(Equal("otel.example.test:4317"))
			Expect(cfg.TLS).To(BeTrue())
			Expect(cfg.ServiceName).To(Equal("localai-test"))
			Expect(cfg.Sampler).To(Equal("always_on"))
		})

		// TLS is the default for remote collectors. Plaintext to a non-loopback
		// host is refused, because trace payloads carry operation names, model
		// names and token counts.
		It("refuses plaintext OTLP to a non-loopback endpoint", func() {
			_, err := ConfigFromEnv(envMap(map[string]string{
				EnvTracesExporter: "otlp",
				envTracesEndpoint: "http://otel.example.test:4317",
			}))
			Expect(err).To(HaveOccurred(), "plaintext OTLP to a non-loopback endpoint must be refused")
			Expect(err.Error()).To(ContainSubstring("plaintext"))
		})

		It("allows plaintext to the default loopback collector", func() {
			cfg, err := ConfigFromEnv(envMap(map[string]string{EnvTracesExporter: "otlp"}))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.TLS).To(BeFalse(), "the default localhost collector should not require TLS")
			Expect(cfg.Endpoint).To(Equal("localhost:4318"))
		})

		It("treats an endpoint without a scheme as TLS", func() {
			cfg, err := ConfigFromEnv(envMap(map[string]string{
				EnvTracesExporter: "otlp",
				envTracesEndpoint: "otel.example.test:4317",
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.TLS).To(BeTrue(), "an endpoint without a scheme must be treated as TLS, not plaintext")
		})

		// The OTLP spec default protocol is http/protobuf, so a bare otlp exporter
		// with no endpoint talks to the local HTTP collector port.
		It("defaults to http/protobuf on the local HTTP collector port", func() {
			cfg, err := ConfigFromEnv(envMap(map[string]string{EnvTracesExporter: "otlp"}))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Protocol).To(Equal("http/protobuf"))
			Expect(cfg.Endpoint).To(Equal("localhost:4318"))
			Expect(cfg.URLPath).To(Equal("/v1/traces"))
			Expect(cfg.TLS).To(BeFalse())
		})

		// A generic endpoint is a base URL: the signal path is appended to it. A
		// traces-specific endpoint is the full URL and is used as given.
		It("appends the signal path to a generic endpoint but not to a traces endpoint", func() {
			generic, err := ConfigFromEnv(envMap(map[string]string{
				EnvTracesExporter: "otlp",
				envEndpoint:       "https://otel.example.test/otel",
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(generic.URLPath).To(Equal("/otel/v1/traces"))

			specific, err := ConfigFromEnv(envMap(map[string]string{
				EnvTracesExporter: "otlp",
				envTracesEndpoint: "https://otel.example.test/custom/traces",
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(specific.URLPath).To(Equal("/custom/traces"))
		})

		It("uses the HTTP default port and path for http/protobuf", func() {
			cfg, err := ConfigFromEnv(envMap(map[string]string{
				EnvTracesExporter: "otlp",
				envTracesProtocol: "http/protobuf",
				envTracesEndpoint: "https://otel.example.test",
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Endpoint).To(Equal("otel.example.test:4318"))
			Expect(cfg.URLPath).To(Equal("/v1/traces"))
		})

		It("parses headers and resource attributes, and lets resource service.name win", func() {
			cfg, err := ConfigFromEnv(envMap(map[string]string{
				EnvTracesExporter:     "otlp",
				envTracesEndpoint:     "https://otel.example.test:4317",
				envTracesHeaders:      "authorization=Bearer abc,x-tenant=a",
				envResourceAttributes: "deployment.environment.name=staging,service.name=override",
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Headers).To(HaveKeyWithValue("authorization", "Bearer abc"))
			Expect(cfg.Headers).To(HaveKeyWithValue("x-tenant", "a"))
			Expect(cfg.ServiceName).To(Equal("override"))
			Expect(cfg.ResourceAttributes).To(HaveKeyWithValue("deployment.environment.name", "staging"))
		})

		It("does not let the environment override service.version", func() {
			cfg, err := ConfigFromEnv(envMap(map[string]string{
				EnvTracesExporter:     "otlp",
				envTracesEndpoint:     "https://otel.example.test:4317",
				envResourceAttributes: "service.version=spoofed",
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.ResourceAttributes).NotTo(HaveKey("service.version"),
				"the binary's own version must not be overridden by the environment")
		})

		It("turns export off when OTEL_SDK_DISABLED is true", func() {
			cfg, err := ConfigFromEnv(envMap(map[string]string{
				EnvTracesExporter: "otlp",
				EnvSDKDisabled:    "true",
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Enabled).To(BeFalse(), "OTEL_SDK_DISABLED=true must turn export off")
		})

		It("rejects an unknown protocol", func() {
			_, err := ConfigFromEnv(envMap(map[string]string{
				EnvTracesExporter: "otlp",
				envTracesProtocol: "carrier-pigeon",
			}))
			Expect(err).To(HaveOccurred())
		})

		It("rejects a non-numeric sampler argument", func() {
			_, err := ConfigFromEnv(envMap(map[string]string{
				EnvTracesExporter:   "otlp",
				envTracesSampler:    "traceidratio",
				envTracesSamplerArg: "not-a-number",
			}))
			Expect(err).To(HaveOccurred())
		})

		// The timeout is milliseconds. A seconds value here would silently make
		// every export time out.
		It("reads the export timeout in milliseconds", func() {
			cfg, err := ConfigFromEnv(envMap(map[string]string{
				EnvTracesExporter: "otlp",
				envTracesEndpoint: "https://otel.example.test:4317",
				envTracesTimeout:  "2500",
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Timeout).To(Equal(2500 * time.Millisecond))
		})
	})

	Describe("parseEndpoint", func() {
		type endpointCase struct {
			raw      string
			protocol string
			insecure bool
			specific bool // from OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
			host     string
			path     string
			tls      bool
			wantErr  bool
		}

		DescribeTable("normalises the endpoint",
			func(tc endpointCase) {
				host, path, tls, err := parseEndpoint(tc.raw, tc.protocol, tc.insecure, tc.specific)
				if tc.wantErr {
					Expect(err).To(HaveOccurred(), "%q", tc.raw)
					return
				}
				Expect(err).NotTo(HaveOccurred(), "%q", tc.raw)
				Expect(host).To(Equal(tc.host), "%q", tc.raw)
				Expect(path).To(Equal(tc.path), "%q", tc.raw)
				Expect(tls).To(Equal(tc.tls), "%q", tc.raw)
			},
			Entry("grpc with an explicit https scheme", endpointCase{raw: "https://otel.example.test:4317", protocol: "grpc", host: "otel.example.test:4317", tls: true}),
			Entry("grpc with a bare host", endpointCase{raw: "otel.example.test", protocol: "grpc", host: "otel.example.test:4317", tls: true}),
			Entry("grpc on loopback over http", endpointCase{raw: "http://127.0.0.1:4317", protocol: "grpc", host: "127.0.0.1:4317"}),
			Entry("http/protobuf default path", endpointCase{raw: "https://otel.example.test", protocol: "http/protobuf", host: "otel.example.test:4318", path: "/v1/traces", tls: true}),
			Entry("generic endpoint appends the signal path", endpointCase{raw: "https://otel.example.test/otel", protocol: "http/protobuf", host: "otel.example.test:4318", path: "/otel/v1/traces", tls: true}),
			Entry("generic endpoint with a trailing slash", endpointCase{raw: "https://otel.example.test/otel/", protocol: "http/protobuf", host: "otel.example.test:4318", path: "/otel/v1/traces", tls: true}),
			Entry("traces endpoint is used as given", endpointCase{raw: "https://otel.example.test/custom/v1/traces", protocol: "http/protobuf", specific: true, host: "otel.example.test:4318", path: "/custom/v1/traces", tls: true}),
			Entry("insecure flag allows plaintext for a bare host", endpointCase{raw: "otel.example.test:4317", protocol: "grpc", insecure: true, host: "otel.example.test:4317"}),
			Entry("https is not downgraded by the insecure flag", endpointCase{raw: "https://otel.example.test:4317", protocol: "grpc", insecure: true, wantErr: true}),
			Entry("unknown scheme is rejected", endpointCase{raw: "ftp://otel.example.test", protocol: "grpc", wantErr: true}),
		)
	})

	Describe("parseKeyValues", func() {
		It("parses pairs, including an empty value", func() {
			got, err := parseKeyValues("a=1,b=two,empty=")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(map[string]string{"a": "1", "b": "two", "empty": ""}))
		})

		It("rejects an entry without =", func() {
			_, err := parseKeyValues("broken")
			Expect(err).To(HaveOccurred())
		})

		It("rejects an empty key", func() {
			_, err := parseKeyValues("=value")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("samplerFor", func() {
		It("returns a sampler for every supported name and for unknown names", func() {
			for _, name := range []string{
				"always_on", "always_off", "traceidratio",
				"parentbased_always_on", "parentbased_always_off", "parentbased_traceidratio",
				"", "something-unknown",
			} {
				Expect(samplerFor(name, 0.5)).NotTo(BeNil(), "sampler for %q", name)
			}
		})
	})

	Describe("serviceVersion", func() {
		BeforeEach(func() {
			savedVersion, savedCommit := internal.Version, internal.Commit
			DeferCleanup(func() { internal.Version, internal.Commit = savedVersion, savedCommit })
		})

		It("reports dev for a source build and version with commit otherwise", func() {
			internal.Version, internal.Commit = "", ""
			Expect(serviceVersion()).To(Equal("dev"))

			internal.Version, internal.Commit = "v4.12.0", "abc123"
			Expect(serviceVersion()).To(Equal("v4.12.0 (abc123)"))
		})
	})
})
