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
