// SPDX-License-Identifier: MIT
package application

import (
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func TestAFrontendWithoutTheAddonProcessOffersNoHostOperations(t *testing.T) {
	named := map[string][]string{"fine-tuning": {"finetune."}}
	a := &Application{applicationConfig: &config.ApplicationConfig{AddonHostOps: named}}
	if a.newAddonHostOps(nil, contract.DefaultLimits()) != nil {
		t.Fatal("a process without the add-on manager built a broker")
	}
	if a.AddonHostOps() != nil {
		t.Fatal("a process that never started the supervisor offers host operations")
	}
}

func TestAHostThatNamesNoAddonOffersNoHostOperations(t *testing.T) {
	for _, ops := range []map[string][]string{nil, {}} {
		a := &Application{applicationConfig: &config.ApplicationConfig{AddonHostOps: ops}}
		if a.newAddonHostOps(nil, contract.DefaultLimits()) != nil {
			t.Fatalf("%v built a broker", ops)
		}
	}
}

func TestTheFeatureCheckRefusesWithoutAnAccountDatabase(t *testing.T) {
	if addonFeatureCheck(nil)("u1", "fine_tuning") {
		t.Fatal("no database must mean no feature")
	}
}
