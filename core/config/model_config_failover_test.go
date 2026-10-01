package config

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("ModelConfig failover", func() {
	chain := func(targets ...string) ModelConfig {
		c := ModelConfig{Name: "chain", Failover: &FailoverConfig{}}
		for _, t := range targets {
			c.Failover.Targets = append(c.Failover.Targets, FailoverTarget{Model: t})
		}
		return c
	}

	It("parses the YAML block and applies defaults", func() {
		var c ModelConfig
		Expect(yaml.Unmarshal([]byte(`
name: assistant-llm
failover:
  targets:
    - model: argus-llm
    - model: gemma-local
      warm: true
  recovery:
    probes: 5
`), &c)).To(Succeed())
		Expect(c.IsFailover()).To(BeTrue())
		Expect(c.Failover.Targets).To(Equal([]FailoverTarget{{Model: "argus-llm"}, {Model: "gemma-local", Warm: true}}))
		Expect(c.Failover.ProbeInterval()).To(Equal(15 * time.Second))
		Expect(c.Failover.ProbeTimeout()).To(Equal(5 * time.Second))
		Expect(c.Failover.TripErrors()).To(Equal(1))
		Expect(c.Failover.TripWindow()).To(Equal(30 * time.Second))
		Expect(c.Failover.RecoveryProbes()).To(Equal(5))
		Expect(c.Failover.MinDwell()).To(Equal(60 * time.Second))
		Expect(c.WarmFailoverTargets()).To(Equal([]string{"gemma-local"}))
	})

	It("accepts a valid chain", func() {
		c := chain("a", "b")
		ok, err := c.Validate()
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
	})

	DescribeTable("rejects invalid chains",
		func(mutate func(*ModelConfig), want string) {
			c := chain("a", "b")
			mutate(&c)
			ok, err := c.Validate()
			Expect(ok).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring(want)))
		},
		Entry("alias and failover", func(c *ModelConfig) { c.Alias = "x" }, "both alias and failover"),
		Entry("backend set", func(c *ModelConfig) { c.Backend = "llama-cpp" }, "must not set backend"),
		Entry("one target", func(c *ModelConfig) { c.Failover.Targets = c.Failover.Targets[:1] }, "at least 2 targets"),
		Entry("empty target", func(c *ModelConfig) { c.Failover.Targets[1].Model = "" }, "no model"),
		Entry("self target", func(c *ModelConfig) { c.Failover.Targets[1].Model = "chain" }, "cannot list itself"),
		Entry("duplicate target", func(c *ModelConfig) { c.Failover.Targets[1].Model = "a" }, "twice"),
		Entry("bad duration", func(c *ModelConfig) { c.Failover.Probe.Interval = "soon" }, "invalid probe.interval"),
		Entry("negative errors", func(c *ModelConfig) { c.Failover.Trip.Errors = -1 }, "trip.errors"),
		Entry("no name", func(c *ModelConfig) { c.Name = "" }, "requires a name"),
	)
})

var _ = Describe("ProxyConfig.ResolveAPIKey", func() {
	It("reads the key through the application lookup", func() {
		app := NewApplicationConfig(WithProxyAPIKeyEnvLookup(func(name string) string {
			Expect(name).To(Equal("FAILOVER_TEST_KEY"))
			return "k1"
		}))
		Expect(ProxyConfig{APIKeyEnv: "FAILOVER_TEST_KEY"}.ResolveAPIKey(app.ProxyAPIKeyEnvLookup)).To(Equal("k1"))
	})
	It("fails without an environment lookup", func() {
		_, err := ProxyConfig{APIKeyEnv: "FAILOVER_TEST_KEY"}.ResolveAPIKey(nil)
		Expect(err).To(HaveOccurred())
	})
	It("fails on an unset env var", func() {
		_, err := ProxyConfig{APIKeyEnv: "FAILOVER_TEST_UNSET_KEY"}.ResolveAPIKey(os.Getenv)
		Expect(err).To(HaveOccurred())
	})
	It("fails on a set-but-empty env var", func() {
		GinkgoT().Setenv("FAILOVER_TEST_EMPTY_KEY", "")
		_, err := ProxyConfig{APIKeyEnv: "FAILOVER_TEST_EMPTY_KEY"}.ResolveAPIKey(os.Getenv)
		Expect(err).To(HaveOccurred())
	})
	It("reads and trims the key file", func() {
		f := filepath.Join(GinkgoT().TempDir(), "key")
		Expect(os.WriteFile(f, []byte(" k2\n"), 0o600)).To(Succeed())
		Expect(ProxyConfig{APIKeyFile: f}.ResolveAPIKey(os.Getenv)).To(Equal("k2"))
	})
	It("returns empty when nothing is set", func() {
		Expect(ProxyConfig{}.ResolveAPIKey(os.Getenv)).To(Equal(""))
	})
})
