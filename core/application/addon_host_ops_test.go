// SPDX-License-Identifier: MIT
package application

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("add-on host operations", func() {
	It("are not offered by a frontend without the add-on process", func() {
		named := map[string][]string{"fine-tuning": {"finetune."}}
		a := &Application{applicationConfig: &config.ApplicationConfig{AddonHostOps: named}}
		Expect(a.newAddonHostOps(nil, contract.DefaultLimits())).To(BeNil(), "a process without the add-on manager built a broker")
		Expect(a.AddonHostOps()).To(BeNil(), "a process that never started the supervisor offers host operations")
	})

	It("are not offered by a host that names no add-on", func() {
		for _, ops := range []map[string][]string{nil, {}} {
			a := &Application{applicationConfig: &config.ApplicationConfig{AddonHostOps: ops}}
			Expect(a.newAddonHostOps(nil, contract.DefaultLimits())).To(BeNil(), "%v built a broker", ops)
		}
	})

	It("refuses the feature check without an account database", func() {
		Expect(addonFeatureCheck(nil)("u1", "fine_tuning")).To(BeFalse(), "no database must mean no feature")
	})
})
