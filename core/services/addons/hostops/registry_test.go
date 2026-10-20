// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func noop(context.Context, Call) (json.RawMessage, error) { return nil, nil }

func TestRegisterRefusesWhatIsMalformed(t *testing.T) {
	b := New(NewInFlight(), nil, contract.DefaultLimits())
	if err := b.Register(Spec{Op: "finetune.get", Scope: "read", Feature: "fine_tuning", Handler: noop}); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]Spec{
		"duplicate":  {Op: "finetune.get", Scope: "read", Feature: "fine_tuning", Handler: noop},
		"bad name":   {Op: "Finetune", Scope: "read", Feature: "fine_tuning", Handler: noop},
		"bad scope":  {Op: "finetune.x", Scope: "preview", Feature: "fine_tuning", Handler: noop},
		"no feature": {Op: "finetune.y", Scope: "read", Handler: noop},
		"no handler": {Op: "finetune.z", Scope: "read", Feature: "fine_tuning"},
	} {
		if b.Register(s) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAnUnregisteredOperationUnderAnAllowedPrefixIsRefused(t *testing.T) {
	f := newFixture(t, always)
	end := f.inflight.Begin(env("fine-tuning", "op1", "apply", time.Now().Add(time.Minute)))
	defer end()
	if code, out := f.call("secret", "op1", "finetune.nothing", `{}`); code != http.StatusForbidden || out.Error.Code != "unknown_op" {
		t.Fatalf("%d %+v", code, out.Error)
	}
}

func TestABrokerWithoutAFeatureCheckRefusesEverything(t *testing.T) {
	f := newFixture(t, nil)
	end := f.inflight.Begin(env("fine-tuning", "op1", "read", time.Now().Add(time.Minute)))
	defer end()
	if code, out := f.call("secret", "op1", "finetune.get", `{}`); code != http.StatusForbidden || out.Error.Code != "feature" {
		t.Fatalf("%d %+v", code, out.Error)
	}
}

func TestTheLocalOwnerNeedsNoFeatureCheck(t *testing.T) {
	f := newFixture(t, func(string, string) bool { return false })
	e := env("fine-tuning", "op1", "read", time.Now().Add(time.Minute))
	e.Authority.Actor.Mode = "local-owner"
	end := f.inflight.Begin(e)
	defer end()
	if code, _ := f.call("secret", "op1", "finetune.get", `{}`); code != http.StatusOK {
		t.Fatalf("%d", code)
	}
}

func TestAnotherAddonCannotUseTheOperationOfALiveRequest(t *testing.T) {
	f := newFixture(t, always)
	end := f.inflight.Begin(env("quantization", "op1", "read", time.Now().Add(time.Minute)))
	defer end()
	if code, out := f.call("secret", "op1", "finetune.get", `{}`); code != http.StatusForbidden || out.Error.Code != "no_live_operation" {
		t.Fatalf("%d %+v", code, out.Error)
	}
}

func TestTheEnvelopeIsGoneWhenTheRequestEnds(t *testing.T) {
	in := NewInFlight()
	e := env("fine-tuning", "op1", "read", time.Now().Add(time.Minute))
	end1, end2 := in.Begin(e), in.Begin(e)
	end1()
	end1()
	if _, ok := in.Lookup("fine-tuning", "op1"); !ok {
		t.Fatal("the second request still runs")
	}
	end2()
	if _, ok := in.Lookup("fine-tuning", "op1"); ok {
		t.Fatal("the envelope stayed")
	}
}
