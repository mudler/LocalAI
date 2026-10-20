// SPDX-License-Identifier: MIT
package application

import (
	"slices"
	"testing"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Embedding the interfaces satisfies them; registration never calls a method.
type noFineTune struct{ hostops.FineTuner }
type noQuant struct{ hostops.Quantizer }
type noBackends struct{ hostops.BackendLister }

func TestRegisterAddonEngineJobsWithoutABrokerRegistersNothing(t *testing.T) {
	a := &Application{}
	if err := a.RegisterAddonEngineJobs(noFineTune{}, noQuant{}, noBackends{}); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterAddonEngineJobsRegistersThroughTheBroker(t *testing.T) {
	b := hostops.New(hostops.NewInFlight(), nil, contract.DefaultLimits())
	a := &Application{addonHostOps: &addonruntime.HostOps{Broker: b}}
	if err := a.RegisterAddonEngineJobs(noFineTune{}, noQuant{}, noBackends{}); err != nil {
		t.Fatal(err)
	}
	ops := b.Ops()
	for _, want := range []string{"finetune.start", "finetune.get", "finetune.list", "finetune.checkpoints", "quantize.start", "quantize.get", "quantize.list"} {
		if !slices.Contains(ops, want) {
			t.Fatalf("%s is not registered: %v", want, ops)
		}
	}
}
