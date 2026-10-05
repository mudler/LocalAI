// SPDX-License-Identifier: MIT
package config_test

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"math"
)

var _ = Describe("decisions router validation", func() {
	It("rejects invalid thresholds and duplicate policies at config validation", func() {
		c := config.ModelConfig{Name: "route", Router: config.RouterConfig{Classifier: "decisions", ClassifierModel: "native", Policies: []config.RouterPolicy{{Label: "x", Description: "x"}}}}
		for _, v := range []float64{-.1, 1.1, math.NaN(), math.Inf(1)} {
			c.Router.ActivationThreshold = v
			_, err := c.Validate()
			Expect(err).To(HaveOccurred())
		}
		c.Router.ActivationThreshold = .5
		c.Router.Policies = append(c.Router.Policies, c.Router.Policies[0])
		_, err := c.Validate()
		Expect(err).To(HaveOccurred())
	})
})
