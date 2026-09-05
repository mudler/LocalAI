package config_test

import (
	"reflect"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

var _ = Describe("DistributedConfig backend NATS timeouts", func() {
	Context("BackendInstallTimeoutOrDefault", func() {
		It("returns 15 minutes when unset", func() {
			c := config.DistributedConfig{}
			Expect(c.BackendInstallTimeoutOrDefault()).To(Equal(15 * time.Minute))
		})

		It("returns the configured value when set", func() {
			c := config.DistributedConfig{BackendInstallTimeout: 42 * time.Minute}
			Expect(c.BackendInstallTimeoutOrDefault()).To(Equal(42 * time.Minute))
		})
	})

	Context("BackendUpgradeTimeoutOrDefault", func() {
		It("returns 15 minutes when unset", func() {
			c := config.DistributedConfig{}
			Expect(c.BackendUpgradeTimeoutOrDefault()).To(Equal(15 * time.Minute))
		})

		It("returns the configured value when set", func() {
			c := config.DistributedConfig{BackendUpgradeTimeout: 30 * time.Minute}
			Expect(c.BackendUpgradeTimeoutOrDefault()).To(Equal(30 * time.Minute))
		})
	})

	Context("ModelLoadTimeoutOrDefault", func() {
		It("returns 5 minutes when unset so existing clusters keep today's behaviour", func() {
			c := config.DistributedConfig{}
			Expect(c.ModelLoadTimeoutOrDefault()).To(Equal(5 * time.Minute))
		})

		It("returns the configured value when set", func() {
			c := config.DistributedConfig{ModelLoadTimeout: 45 * time.Minute}
			Expect(c.ModelLoadTimeoutOrDefault()).To(Equal(45 * time.Minute))
		})
	})
})

var _ = Describe("DistributedConfig flag-name constants", func() {
	// Pin the kebab-case strings so a rename of the Go field name (or a
	// CLI flag naming convention change) forces the constant to update,
	// keeping the Validate error messages and any future operator-facing
	// surface in sync with the actual CLI flag.
	DescribeTable("flag name constants",
		func(actual, expected string) {
			Expect(actual).To(Equal(expected))
		},
		Entry("MCP tool timeout", config.FlagMCPToolTimeout, "mcp-tool-timeout"),
		Entry("MCP discovery timeout", config.FlagMCPDiscoveryTimeout, "mcp-discovery-timeout"),
		Entry("worker wait timeout", config.FlagWorkerWaitTimeout, "worker-wait-timeout"),
		Entry("drain timeout", config.FlagDrainTimeout, "drain-timeout"),
		Entry("health check interval", config.FlagHealthCheckInterval, "health-check-interval"),
		Entry("stale node threshold", config.FlagStaleNodeThreshold, "stale-node-threshold"),
		Entry("MCP CI job timeout", config.FlagMCPCIJobTimeout, "mcp-ci-job-timeout"),
		Entry("backend install timeout", config.FlagBackendInstallTimeout, "backend-install-timeout"),
		Entry("backend upgrade timeout", config.FlagBackendUpgradeTimeout, "backend-upgrade-timeout"),
		Entry("model load timeout", config.FlagModelLoadTimeout, "model-load-timeout"),
	)
})

var _ = Describe("DistributedConfig.Validate negative-duration errors", func() {
	It("rejects a negative BackendInstallTimeout with the flag name in the error", func() {
		c := config.DistributedConfig{
			Enabled:               true,
			BackendInstallTimeout: -1 * time.Second,
		}
		err := c.Validate()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(config.FlagBackendInstallTimeout))
		Expect(err.Error()).To(ContainSubstring("must not be negative"))
	})

	It("rejects a negative BackendUpgradeTimeout with the flag name in the error", func() {
		c := config.DistributedConfig{
			Enabled:               true,
			BackendUpgradeTimeout: -1 * time.Second,
		}
		err := c.Validate()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(config.FlagBackendUpgradeTimeout))
	})

	It("rejects a negative ModelLoadTimeout with the flag name in the error", func() {
		c := config.DistributedConfig{
			Enabled:          true,
			ModelLoadTimeout: -1 * time.Second,
		}
		err := c.Validate()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(config.FlagModelLoadTimeout))
		Expect(err.Error()).To(ContainSubstring("must not be negative"))
	})

	It("accepts all-zero durations as valid (defaults apply)", func() {
		c := config.DistributedConfig{
			Enabled: true,
		}
		Expect(c.Validate()).To(Succeed())
	})
})

var _ = Describe("DistributedConfig.Validate registration auth", func() {
	It("rejects an empty registration token when RequireAuth is set", func() {
		c := config.DistributedConfig{
			Enabled:                 true,
			RegistrationRequireAuth: true,
		}
		err := c.Validate()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("LOCALAI_REGISTRATION_REQUIRE_AUTH"))
		Expect(err.Error()).To(ContainSubstring("LOCALAI_REGISTRATION_TOKEN"))
	})

	It("accepts a set registration token when RequireAuth is set", func() {
		c := config.DistributedConfig{
			Enabled:                 true,
			RegistrationToken:       "s3cret",
			RegistrationRequireAuth: true,
		}
		Expect(c.Validate()).To(Succeed())
	})

	It("warns but succeeds with an empty token when RequireAuth is unset", func() {
		c := config.DistributedConfig{
			Enabled: true,
		}
		Expect(c.Validate()).To(Succeed())
	})

	It("rejects an empty token when the umbrella RequireAuth is set", func() {
		c := config.DistributedConfig{
			Enabled:     true,
			RequireAuth: true,
		}
		err := c.Validate()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("LOCALAI_DISTRIBUTED_REQUIRE_AUTH"))
		Expect(err.Error()).To(ContainSubstring("LOCALAI_REGISTRATION_TOKEN"))
	})

	// The umbrella used to imply two things, and now implies one.
	//
	// The It that stood here pinned "LOCALAI_DISTRIBUTED_REQUIRE_AUTH makes a
	// missing broker service JWT fatal". That is retired, not moved: there is
	// no broker connection to demand a credential for, so a startup that failed
	// on a missing one would fail on the absence of something nothing uses. The
	// half of the umbrella that survives is the registration layer, and the It
	// above it pins exactly that: an empty registration token under the
	// umbrella is still fatal, and still says which knob to set.
	It("implies the registration layer and nothing else", func() {
		c := config.DistributedConfig{
			Enabled:           true,
			RegistrationToken: "tok",
			RequireAuth:       true,
		}
		Expect(c.RegistrationAuthRequired()).To(BeTrue())
		// And with the registration layer satisfied there is nothing else left
		// for the umbrella to demand, so startup succeeds.
		Expect(c.Validate()).To(Succeed())
	})
})

var _ = Describe("DistributedConfig worker reconnect grace", func() {
	It("defaults clear of two ceiling backoffs plus the dial between them", func() {
		// The worker's own numbers (core/services/worker/tunnel.go): a 30s
		// backoff ceiling and a 10s dial budget, so two ceiling waits with a
		// hung dial between them puts the worker back at 70s. 60s would sit
		// under that and condemn a worker reconnecting exactly as designed;
		// 90s clears it with margin.
		Expect(config.DistributedConfig{}.ReconnectGraceOrDefault()).To(Equal(90 * time.Second))
		Expect(config.DefaultWorkerReconnectGrace).To(BeNumerically(">", 70*time.Second),
			"the default must clear two ceiling backoffs plus one handshake timeout")
	})

	It("takes a configured worker reconnect grace verbatim", func() {
		cfg := config.DistributedConfig{WorkerReconnectGrace: 5 * time.Minute}
		Expect(cfg.ReconnectGraceOrDefault()).To(Equal(5 * time.Minute))
	})

	It("falls back to the default rather than condemning every worker on a negative value", func() {
		// A negative grace makes every departure older than the window the
		// instant it is stamped, which reports a worker that has been gone for
		// two seconds as GONE, and gone is the one value a caller may reap on.
		cfg := config.DistributedConfig{WorkerReconnectGrace: -1 * time.Second}
		Expect(cfg.ReconnectGraceOrDefault()).To(Equal(config.DefaultWorkerReconnectGrace))
	})

	It("refuses to start on a negative grace rather than reaping on it", func() {
		// The flag is in Validate's negative-duration table, and this is what
		// says so. A negative grace makes every departure older than the window
		// the instant it is stamped, so the deployment would answer GONE for
		// every worker that has ever lost a tunnel, and gone is the one answer
		// a caller may reap and evict on.
		c := config.DistributedConfig{
			Enabled:              true,
			RegistrationToken:    "tok",
			WorkerReconnectGrace: -1 * time.Second,
		}
		err := c.Validate()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(config.FlagWorkerReconnectGrace))
	})

	It("is settable through the application option", func() {
		o := &config.ApplicationConfig{}
		config.WithWorkerReconnectGrace(90 * time.Second)(o)
		Expect(o.Distributed.ReconnectGraceOrDefault()).To(Equal(90 * time.Second))
	})
})

// The frontend's distributed configuration has nowhere to put a broker URL, and
// that absence is what makes the accepted-and-ignored CLI flags ignored.
//
// A help string saying "ignored" is a promise; a missing field is the mechanism.
// Asserted by reflection rather than by reading a value, because a value
// assertion needs a field to read and would therefore stop compiling exactly
// when the property it guards is restored, which is the failure mode this
// replaces: a spec that vanishes with the regression it was meant to catch.
var _ = Describe("the distributed configuration's broker surface", func() {
	It("carries no NATS credential, TLS or URL field", func() {
		t := reflect.TypeOf(config.DistributedConfig{})
		var carried []string
		for i := range t.NumField() {
			if strings.HasPrefix(t.Field(i).Name, "Nats") {
				carried = append(carried, t.Field(i).Name)
			}
		}
		Expect(carried).To(BeEmpty(),
			"DistributedConfig grew %v back: a value the frontend can store is a value something can dial, and no component of a distributed deployment dials a message bus", carried)
	})
})
