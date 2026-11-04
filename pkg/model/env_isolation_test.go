// SPDX-License-Identifier: MIT
package model

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Model env isolation", func() {
	It("copies the caller map for each options instance", func() {
		env := map[string]string{"KEY": "original"}
		option := WithEnvVars(env)
		first, second := NewOptions(option), NewOptions(option)
		env["KEY"] = "caller mutation"
		first.envVars["KEY"] = "first mutation"
		Expect(second.envVars["KEY"]).To(Equal("original"))
	})
})
