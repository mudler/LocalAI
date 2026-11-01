// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func env(addon, op, scope string, exp time.Time) contract.Envelope {
	return contract.Envelope{
		Request:   contract.Request{AddonID: addon, OperationID: op, Generation: 1, Action: "start", Arguments: json.RawMessage(`{}`)},
		Authority: contract.Authority{Actor: contract.Actor{UserID: "u1", CredentialID: "c1", Mode: "named-user"}, Scope: scope, ExpiresAt: exp},
	}
}

type fixture struct {
	b        *Broker
	inflight *InFlight
	h        http.Handler
	seen     *Call
}

func newFixture(has func(user, feature string) bool) *fixture {
	f := &fixture{inflight: NewInFlight(), seen: &Call{}}
	f.b = New(f.inflight, has, contract.DefaultLimits())
	err := f.b.Register(Spec{Op: "finetune.get", Scope: "read", Feature: "fine_tuning", Handler: func(_ context.Context, c Call) (json.RawMessage, error) {
		*f.seen = c
		return json.RawMessage(`{"ok":true}`), nil
	}})
	Expect(err).ToNot(HaveOccurred())
	err = f.b.Register(Spec{Op: "finetune.start", Scope: "apply", Feature: "fine_tuning", Handler: func(context.Context, Call) (json.RawMessage, error) {
		return json.RawMessage(`{"id":"j1"}`), nil
	}})
	Expect(err).ToNot(HaveOccurred())
	f.h = f.b.Handler("fine-tuning", "secret", []string{"finetune."})
	return f
}

func (f *fixture) call(credential, operationID, op, body string) (int, contract.HostResponse) {
	b, _ := json.Marshal(contract.HostRequest{OperationID: operationID, Op: op, Arguments: json.RawMessage(body)})
	req := httptest.NewRequest(http.MethodPost, "/host", bytes.NewReader(b))
	req.Header.Set("X-Addon-Host-Credential", credential)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	var out contract.HostResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func always(string, string) bool { return true }

var _ = Describe("Host operation broker", func() {
	live := func(f *fixture, scope string) {
		end := f.inflight.Begin(env("fine-tuning", "op1", scope, time.Now().Add(time.Minute)))
		DeferCleanup(end)
	}

	It("refuses a wrong credential", func() {
		f := newFixture(always)
		code, _ := f.call("nope", "op1", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusUnauthorized))
	})

	It("refuses a call without a live envelope", func() {
		f := newFixture(always)
		code, out := f.call("secret", "op1", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal("no_live_operation"))
	})

	It("refuses an expired authority", func() {
		f := newFixture(always)
		end := f.inflight.Begin(env("fine-tuning", "op1", "read", time.Now().Add(-time.Second)))
		DeferCleanup(end)
		code, out := f.call("secret", "op1", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal("no_live_operation"))
	})

	It("does not let a read scope run an apply operation", func() {
		f := newFixture(always)
		live(f, "read")
		code, out := f.call("secret", "op1", "finetune.start", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal("scope"))
	})

	It("checks the feature of the actor", func() {
		f := newFixture(func(user, feature string) bool { return feature != "fine_tuning" })
		live(f, "read")
		code, out := f.call("secret", "op1", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal("feature"))
	})

	It("runs the operation as the actor of the envelope", func() {
		f := newFixture(always)
		live(f, "read")
		// An argument that names a user is refused, whatever the schema says.
		code, out := f.call("secret", "op1", "finetune.get", `{"userId":"admin"}`)
		Expect(code).To(Equal(http.StatusBadRequest))
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal("bad_request"))
		code, _ = f.call("secret", "op1", "finetune.get", `{"jobId":"j1"}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(f.seen.Actor.UserID).To(Equal("u1"), "the operation did not run as the envelope's actor: %+v", f.seen)
		Expect(f.seen.AddonID).To(Equal("fine-tuning"))
	})

	It("refuses an operation outside the allowed prefix", func() {
		f := newFixture(always)
		_ = f.b.Register(Spec{Op: "quantize.get", Scope: "read", Feature: "quantization", Handler: func(context.Context, Call) (json.RawMessage, error) { return nil, nil }})
		live(f, "read")
		code, out := f.call("secret", "op1", "quantize.get", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal("unknown_op"))
	})

	It("refuses an oversized body", func() {
		f := newFixture(always)
		live(f, "read")
		big := `{"x":"` + string(bytes.Repeat([]byte("a"), int(contract.DefaultLimits().PayloadBytes)+1)) + `"}`
		code, out := f.call("secret", "op1", "finetune.get", big)
		Expect(code).To(Equal(http.StatusRequestEntityTooLarge))
		Expect(out.Error).ToNot(BeNil())
		Expect(out.Error.Code).To(Equal("too_large"))
	})

	It("runs an idempotent operation once per operation ID", func() {
		f := newFixture(always)
		runs := 0
		_ = f.b.Register(Spec{Op: "finetune.once", Scope: "apply", Feature: "fine_tuning", Idempotent: true, Handler: func(context.Context, Call) (json.RawMessage, error) {
			runs++
			return json.RawMessage(`{"id":"j9"}`), nil
		}})
		live(f, "apply")
		for i := 0; i < 2; i++ {
			code, _ := f.call("secret", "op1", "finetune.once", `{}`)
			Expect(code).To(Equal(http.StatusOK), "call %d", i)
		}
		Expect(runs).To(Equal(1), "a repeated operationId must not run the operation twice")
	})
})

var _ = Describe("Host operation broker, inbound class", func() {
	newInbound := func() (*Broker, http.Handler, *Call) {
		seen := &Call{}
		b := New(NewInFlight(), func(string, string) bool { return false }, contract.DefaultLimits())
		Expect(b.Register(Spec{Op: "connector.inbound", Scope: ScopeInbound, Handler: func(_ context.Context, c Call) (json.RawMessage, error) {
			*seen = c
			return json.RawMessage(`{"ok":true}`), nil
		}})).To(Succeed())
		return b, b.Handler("telegram", "secret", []string{"connector."}), seen
	}
	post := func(h http.Handler, credential, body string) (int, contract.HostResponse) {
		raw, _ := json.Marshal(contract.HostRequest{OperationID: "u1", Op: "connector.inbound", Arguments: json.RawMessage(body)})
		req := httptest.NewRequest(http.MethodPost, "/host", bytes.NewReader(raw))
		req.Header.Set(contract.HostCredentialHeader, credential)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out contract.HostResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	It("runs with the incarnation credential alone, with no live request and no actor", func() {
		_, h, seen := newInbound()
		code, _ := post(h, "secret", `{"bindingId":"b1"}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(seen.AddonID).To(Equal("telegram"))
		Expect(seen.Actor).To(Equal(contract.Actor{}), "an inbound call has no actor")
	})

	It("refuses an absent or foreign credential", func() {
		_, h, seen := newInbound()
		for _, c := range []string{"", "other-incarnation", "secre"} {
			code, out := post(h, c, `{"bindingId":"b1"}`)
			Expect(code).To(Equal(http.StatusUnauthorized), "credential %q", c)
			Expect(out.Error.Code).To(Equal(contract.HostNoLiveOperation))
		}
		Expect(seen.AddonID).To(BeEmpty(), "the handler must not run")
	})

	It("takes the add-on ID from the broker, not from the request", func() {
		b, _, seen := newInbound()
		h := b.Handler("slack", "secret-2", []string{"connector."})
		code, _ := post(h, "secret-2", `{"bindingId":"b1","addonId":"telegram"}`)
		Expect(code).To(Equal(http.StatusBadRequest), "an add-on name in the arguments is refused")
		code, _ = post(h, "secret-2", `{"bindingId":"b1"}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(seen.AddonID).To(Equal("slack"))
	})

	It("refuses an argument that names a user, an actor or an owner", func() {
		_, h, seen := newInbound()
		for _, k := range []string{"userId", "actor", "owner", "user", "mode"} {
			code, _ := post(h, "secret", `{"bindingId":"b1","`+k+`":"admin"}`)
			Expect(code).To(Equal(http.StatusBadRequest), k)
		}
		Expect(seen.AddonID).To(BeEmpty())
	})

	It("refuses an op outside the prefix the host setting gives", func() {
		b, _, _ := newInbound()
		h := b.Handler("telegram", "secret", []string{"workspace."})
		code, out := post(h, "secret", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error.Code).To(Equal(contract.HostUnknownOp))
	})

	It("does not turn a normal operation into an inbound one", func() {
		f := newFixture(always)
		code, out := f.call("secret", "never-started", "finetune.get", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error.Code).To(Equal(contract.HostNoLiveOperation))
	})

	It("refuses to register an inbound operation that names a feature", func() {
		b := New(NewInFlight(), always, contract.DefaultLimits())
		Expect(b.Register(Spec{Op: "connector.x", Scope: ScopeInbound, Feature: "chats", Handler: func(context.Context, Call) (json.RawMessage, error) { return nil, nil }})).NotTo(Succeed())
	})

	It("maps a refusal of the handler to 403", func() {
		b := New(NewInFlight(), always, contract.DefaultLimits())
		Expect(b.Register(Spec{Op: "connector.inbound", Scope: ScopeInbound, Handler: func(context.Context, Call) (json.RawMessage, error) { return nil, ErrForbidden }})).To(Succeed())
		code, _ := post(b.Handler("telegram", "secret", []string{"connector."}), "secret", `{}`)
		Expect(code).To(Equal(http.StatusForbidden))
	})
})
