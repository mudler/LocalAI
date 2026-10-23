// SPDX-License-Identifier: MIT
package application

import (
	"slices"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Embedding the interfaces satisfies them; registration never calls a method.
type noFineTune struct{ hostops.FineTuner }
type noQuant struct{ hostops.Quantizer }
type noBackends struct{ hostops.BackendLister }

var _ = Describe("RegisterAddonEngineJobs", func() {
	It("registers nothing without a broker", func() {
		a := &Application{}
		Expect(a.RegisterAddonEngineJobs(noFineTune{}, noQuant{}, noBackends{})).To(Succeed())
	})

	It("registers through the broker", func() {
		b := hostops.New(hostops.NewInFlight(), nil, contract.DefaultLimits())
		a := &Application{addonHostOps: &addonruntime.HostOps{Broker: b}}
		Expect(a.RegisterAddonEngineJobs(noFineTune{}, noQuant{}, noBackends{})).To(Succeed())
		ops := b.Ops()
		for _, want := range []string{"finetune.start", "finetune.get", "finetune.list", "finetune.checkpoints", "quantize.start", "quantize.get", "quantize.list"} {
			Expect(slices.Contains(ops, want)).To(BeTrue(), "%s is not registered: %v", want, ops)
		}
	})
})
