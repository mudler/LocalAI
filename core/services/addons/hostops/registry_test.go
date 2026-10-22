// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func noop(context.Context, Call) (json.RawMessage, error) { return nil, nil }

var _ = Describe("Operation registry", func() {
	It("refuses what is malformed", func() {
		b := New(NewInFlight(), nil, contract.DefaultLimits())
		Expect(b.Register(Spec{Op: "finetune.get", Scope: "read", Feature: "fine_tuning", Handler: noop})).To(Succeed())
		for name, s := range map[string]Spec{
			"duplicate":  {Op: "finetune.get", Scope: "read", Feature: "fine_tuning", Handler: noop},
			"bad name":   {Op: "Finetune", Scope: "read", Feature: "fine_tuning", Handler: noop},
			"bad scope":  {Op: "finetune.x", Scope: "preview", Feature: "fine_tuning", Handler: noop},
			"no feature": {Op: "finetune.y", Scope: "read", Handler: noop},
			"no handler": {Op: "finetune.z", Scope: "read", Feature: "fine_tuning"},
		} {
			Expect(b.Register(s)).ToNot(Succeed(), "%s: accepted", name)
		}
	})

	It("refuses an unregistered operation under an allowed prefix", func() {
		f := newFixture(always)
		end := f.inflight.Begin(env("fine-tuning", "op1", "apply", time.Now().Add(time.Minute)))
		DeferCleanup(end)
		code, out := f.call("secret", "op1", "finetune.nothing", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal("unknown_op"))
	})

	It("refuses everything for a broker without a feature check", func() {
		f := newFixture(nil)
		end := f.inflight.Begin(env("fine-tuning", "op1", "read", time.Now().Add(time.Minute)))
		DeferCleanup(end)
		code, out := f.call("secret", "op1", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal("feature"))
	})

	It("needs no feature check for the local owner", func() {
		f := newFixture(func(string, string) bool { return false })
		e := env("fine-tuning", "op1", "read", time.Now().Add(time.Minute))
		e.Authority.Actor.Mode = "local-owner"
		end := f.inflight.Begin(e)
		DeferCleanup(end)
		code, _ := f.call("secret", "op1", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusOK))
	})

	It("does not let another add-on use the operation of a live request", func() {
		f := newFixture(always)
		end := f.inflight.Begin(env("quantization", "op1", "read", time.Now().Add(time.Minute)))
		DeferCleanup(end)
		code, out := f.call("secret", "op1", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal("no_live_operation"))
	})

	It("drops the envelope when the request ends", func() {
		in := NewInFlight()
		e := env("fine-tuning", "op1", "read", time.Now().Add(time.Minute))
		end1, end2 := in.Begin(e), in.Begin(e)
		end1()
		end1()
		_, ok := in.Lookup("fine-tuning", "op1")
		Expect(ok).To(BeTrue(), "the second request still runs")
		end2()
		_, ok = in.Lookup("fine-tuning", "op1")
		Expect(ok).To(BeFalse(), "the envelope stayed")
	})
})
