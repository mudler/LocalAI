// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"net/http"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeEgress struct {
	calls  []EgressCall
	checks []EgressCheck
	events []EgressEvent
}

func (f *fakeEgress) Check(_ context.Context, c EgressCall, a EgressCheck) (EgressVerdict, error) {
	f.calls = append(f.calls, c)
	f.checks = append(f.checks, a)
	return EgressVerdict{Action: "block", Reason: "off"}, nil
}

func (f *fakeEgress) Audit(_ context.Context, c EgressCall, evs []EgressEvent) error {
	f.calls = append(f.calls, c)
	f.events = append(f.events, evs...)
	return nil
}

var _ = Describe("Egress operations", func() {
	var (
		f    *fixture
		host *fakeEgress
		has  bool
	)
	BeforeEach(func() {
		has = true
		host = &fakeEgress{}
		f = &fixture{inflight: NewInFlight(), seen: &Call{}}
		f.b = New(f.inflight, func(_, feature string) bool { return has && feature == "chats" }, contract.DefaultLimits())
		Expect(RegisterEgress(f.b, host)).To(Succeed())
		f.h = f.b.Handler("sandbox", "secret", []string{"workspace.", "egress."})
	})
	live := func(scope, resource string) {
		e := env("sandbox", "op1", scope, time.Now().Add(time.Minute))
		e.Request.ResourceID = resource
		DeferCleanup(f.inflight.Begin(e))
	}

	It("takes the chat from the envelope's resource and the user from its actor", func() {
		live("act", "c-abc")
		code, out := f.call("secret", "op1", "egress.check", `{"host":"pypi.org","port":443,"kind":"connect","tls":true}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		Expect(string(out.Result)).To(MatchJSON(`{"action":"block","reason":"off"}`))
		Expect(host.calls).To(ConsistOf(EgressCall{Actor: contract.Actor{UserID: "u1", CredentialID: "c1", Mode: "named-user"}, ResourceID: "c-abc", OperationID: "op1"}))
		Expect(host.checks).To(ConsistOf(EgressCheck{Host: "pypi.org", Port: 443, Kind: "connect", TLS: true}))
	})

	It("refuses arguments that name a chat, a user or the policy, and unknown ones", func() {
		live("act", "c-abc")
		for _, body := range []string{
			`{"host":"a.example","port":443,"kind":"connect","chat":"other"}`,
			`{"host":"a.example","port":443,"kind":"connect","user":"bob"}`,
			`{"host":"a.example","port":443,"kind":"connect","preset":"all"}`,
			`{"host":"a.example","port":443,"kind":"connect","Allow":true}`,
			`{"host":"a.example","port":443,"kind":"connect","extra":1}`,
			`{"host":"","port":443,"kind":"connect"}`,
			`{"host":"a.example","port":0,"kind":"connect"}`,
			`{"host":"a.example","port":443,"kind":"udp"}`,
		} {
			code, _ := f.call("secret", "op1", "egress.check", body)
			Expect(code).To(Equal(http.StatusBadRequest), body)
		}
		Expect(host.checks).To(BeEmpty())
	})

	It("refuses a call with no live operation, a lower scope, no chat or no chats feature", func() {
		code, out := f.call("secret", "op1", "egress.check", `{"host":"a.example","port":443,"kind":"connect"}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error.Code).To(Equal(contract.HostNoLiveOperation))

		e := env("sandbox", "op2", "read", time.Now().Add(time.Minute))
		e.Request.ResourceID = "c-abc"
		DeferCleanup(f.inflight.Begin(e))
		code, out = f.call("secret", "op2", "egress.check", `{"host":"a.example","port":443,"kind":"connect"}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error.Code).To(Equal(contract.HostScope))

		e = env("sandbox", "op3", "act", time.Now().Add(time.Minute))
		DeferCleanup(f.inflight.Begin(e))
		code, _ = f.call("secret", "op3", "egress.check", `{"host":"a.example","port":443,"kind":"connect"}`)
		Expect(code).To(Equal(http.StatusBadRequest), "no resource: no chat")

		live("act", "c-abc")
		has = false
		code, out = f.call("secret", "op1", "egress.check", `{"host":"a.example","port":443,"kind":"connect"}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(out.Error.Code).To(Equal(contract.HostFeature))
		Expect(host.checks).To(BeEmpty())
	})

	It("takes at most 500 audit lines and only done or block verdicts", func() {
		live("act", "c-abc")
		code, _ := f.call("secret", "op1", "egress.audit", `{"events":[{"host":"pypi.org","port":443,"kind":"connect","verdict":"done","reason":"preset","bytesDown":10}]}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(host.events).To(HaveLen(1))
		code, _ = f.call("secret", "op1", "egress.audit", `{"events":[{"host":"pypi.org","port":443,"kind":"connect","verdict":"allow","reason":"x"}]}`)
		Expect(code).To(Equal(http.StatusBadRequest))
	})
})
