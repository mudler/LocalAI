package config_test

import (
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
	)
})

var _ = Describe("DistributedConfig.Validate negative-duration errors", func() {
	It("rejects a negative BackendInstallTimeout with the flag name in the error", func() {
		c := config.DistributedConfig{
			Enabled:               true,
			NatsURL:               "nats://localhost:4222",
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
			NatsURL:               "nats://localhost:4222",
			BackendUpgradeTimeout: -1 * time.Second,
		}
		err := c.Validate()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(config.FlagBackendUpgradeTimeout))
	})

	It("accepts all-zero durations as valid (defaults apply)", func() {
		c := config.DistributedConfig{
			Enabled: true,
			NatsURL: "nats://localhost:4222",
		}
		Expect(c.Validate()).To(Succeed())
	})
})

var _ = Describe("DistributedConfig.Validate registration auth", func() {
	It("rejects an empty registration token when RequireAuth is set", func() {
		c := config.DistributedConfig{
			Enabled:                 true,
			NatsURL:                 "nats://localhost:4222",
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
			NatsURL:                 "nats://localhost:4222",
			RegistrationToken:       "s3cret",
			RegistrationRequireAuth: true,
		}
		Expect(c.Validate()).To(Succeed())
	})

	It("warns but succeeds with an empty token when RequireAuth is unset", func() {
		c := config.DistributedConfig{
			Enabled: true,
			NatsURL: "nats://localhost:4222",
		}
		Expect(c.Validate()).To(Succeed())
	})

	It("rejects an empty token when the umbrella RequireAuth is set", func() {
		c := config.DistributedConfig{
			Enabled:     true,
			NatsURL:     "nats://localhost:4222",
			RequireAuth: true,
			// Provide NATS creds so only the registration-token gap remains.
			NatsServiceJWT:  "jwt",
			NatsServiceSeed: "seed",
			NatsAccountSeed: "acct",
		}
		err := c.Validate()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("LOCALAI_DISTRIBUTED_REQUIRE_AUTH"))
		Expect(err.Error()).To(ContainSubstring("LOCALAI_REGISTRATION_TOKEN"))
	})

	It("the umbrella implies NATS auth is required", func() {
		c := config.DistributedConfig{
			Enabled:           true,
			NatsURL:           "nats://localhost:4222",
			RegistrationToken: "tok", // registration layer satisfied
			RequireAuth:       true,  // umbrella → NATS creds now required
		}
		Expect(c.NatsAuthRequired()).To(BeTrue())
		Expect(c.RegistrationAuthRequired()).To(BeTrue())
		// Missing NATS service JWT/seed must now be fatal.
		err := c.Validate()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("LOCALAI_NATS_REQUIRE_AUTH"))
	})
})
