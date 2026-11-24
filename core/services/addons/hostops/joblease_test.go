// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Job leases", func() {
	act := func(op string) contract.Envelope {
		e := env("sandbox", op, "act", time.Now().Add(time.Minute))
		e.Request.Action = "act"
		e.Request.ResourceID = "chat-c1"
		return e
	}
	chat := Binding{Principal: "u1", ChatID: "c1"}

	It("keeps a started job's request live after the call, bound to the same chat, until the job ends", func() {
		in := NewInFlight()
		end := in.BeginRuntime(act("op1"), chat, true, "inc1")
		Expect(in.Lease("sandbox", "op1", "inc1", "job-abcdef", time.Now().Add(time.Hour))).To(Succeed())
		_, _, leased, live := in.LookupLive("sandbox", "op1")
		Expect(live && !leased).To(BeTrue(), "while the call runs it is an ordinary request")
		end()
		got, b, leased, live := in.LookupLive("sandbox", "op1")
		Expect(live).To(BeTrue())
		Expect(leased).To(BeTrue())
		Expect(b).To(Equal(chat))
		Expect(got.Authority.Actor.UserID).To(Equal("u1"))
		Expect(in.Leases("sandbox")).To(Equal(1))
		in.EndLease("sandbox", "inc1", "job-abcdef")
		_, _, _, live = in.LookupLive("sandbox", "op1")
		Expect(live).To(BeFalse())
		in.EndLease("sandbox", "inc1", "job-abcdef") // twice is fine
	})

	It("ends a lease at its end time even when nobody reports the job", func() {
		in := NewInFlight()
		end := in.BeginRuntime(act("op1"), chat, true, "inc1")
		Expect(in.Lease("sandbox", "op1", "inc1", "job-abcdef", time.Now().Add(50*time.Millisecond))).To(Succeed())
		end()
		Eventually(func() bool { _, _, _, live := in.LookupLive("sandbox", "op1"); return live }).Should(BeFalse())
	})

	It("cannot lease a request that is not running, of another incarnation, twice or an observation", func() {
		in := NewInFlight()
		Expect(in.Lease("sandbox", "nope", "inc1", "job-abcdef", time.Now().Add(time.Hour))).NotTo(Succeed())
		end := in.BeginRuntime(act("op1"), chat, true, "inc1")
		defer end()
		Expect(in.Lease("sandbox", "op1", "inc2", "job-abcdef", time.Now().Add(time.Hour))).NotTo(Succeed())
		Expect(in.Lease("sandbox", "op1", "inc1", "", time.Now().Add(time.Hour))).NotTo(Succeed())
		Expect(in.Lease("sandbox", "op1", "inc1", "job-abcdef", time.Now().Add(-time.Second))).NotTo(Succeed())
		Expect(in.Lease("sandbox", "op1", "inc1", "job-abcdef", time.Now().Add(time.Hour))).To(Succeed())
		Expect(in.Lease("sandbox", "op1", "inc1", "job-bcdefg", time.Now().Add(time.Hour))).NotTo(Succeed())
		endObs, _, err := in.BeginObservation(act("op2"), chat, "inc1", "r1")
		Expect(err).NotTo(HaveOccurred())
		defer endObs()
		Expect(in.Lease("sandbox", "op2", "inc1", "job-cdefgh", time.Now().Add(time.Hour))).NotTo(Succeed())
	})

	It("bounds the leases of one add-on", func() {
		in := NewInFlight()
		for i := range MaxLeasesPerAddon {
			op := fmt.Sprintf("op%d", i)
			end := in.BeginRuntime(act(op), chat, true, "inc1")
			Expect(in.Lease("sandbox", op, "inc1", fmt.Sprintf("j%d", i), time.Now().Add(time.Hour))).To(Succeed())
			end()
		}
		end := in.BeginRuntime(act("over"), chat, true, "inc1")
		defer end()
		Expect(in.Lease("sandbox", "over", "inc1", "jx", time.Now().Add(time.Hour))).To(MatchError(ContainSubstring("too many")))
	})

	It("reserves the place of a lease before a start is sent, so a full table refuses the start", func() {
		in := NewInFlight()
		var slots []*LeaseSlot
		for range MaxLeasesPerAddon - 1 {
			sl, err := in.ReserveLease("sandbox")
			Expect(err).NotTo(HaveOccurred())
			slots = append(slots, sl)
		}
		last, err := in.ReserveLease("sandbox")
		Expect(err).NotTo(HaveOccurred())
		_, err = in.ReserveLease("sandbox")
		Expect(err).To(MatchError(ErrLeasesFull), "every place is taken or reserved")
		_, err = in.ReserveLease("other")
		Expect(err).NotTo(HaveOccurred(), "the places are per add-on")

		// A reserved start leases within its own place, whatever the count.
		end := in.BeginRuntime(act("op1"), chat, true, "inc1")
		Expect(last.Lease("op1", "inc1", "job-abcdef", time.Now().Add(time.Hour))).To(Succeed())
		end()
		Expect(in.Leases("sandbox")).To(Equal(1))
		last.Release() // after a lease it frees nothing
		_, err = in.ReserveLease("sandbox")
		Expect(err).To(MatchError(ErrLeasesFull))

		// A refused start gives its place back, once.
		slots[0].Release()
		slots[0].Release()
		_, err = in.ReserveLease("sandbox")
		Expect(err).NotTo(HaveOccurred())
		_, err = in.ReserveLease("sandbox")
		Expect(err).To(MatchError(ErrLeasesFull))
		// The end of a job frees its place.
		in.EndLease("sandbox", "inc1", "job-abcdef")
		_, err = in.ReserveLease("sandbox")
		Expect(err).NotTo(HaveOccurred())
	})

	It("drops the leases of an incarnation that goes away", func() {
		in := NewInFlight()
		end := in.BeginRuntime(act("op1"), chat, true, "inc1")
		Expect(in.Lease("sandbox", "op1", "inc1", "job-abcdef", time.Now().Add(time.Hour))).To(Succeed())
		end()
		in.ForgetRuntime("sandbox", "inc1")
		_, _, _, live := in.LookupLive("sandbox", "op1")
		Expect(live).To(BeFalse())
	})

	Describe("on the channel", func() {
		var in *InFlight
		var h http.Handler
		var seen []Call
		BeforeEach(func() {
			in = NewInFlight()
			seen = nil
			b := New(in, always, contract.DefaultLimits())
			for _, op := range []string{"egress.check", "workspace.commit", "workspace.materialize"} {
				Expect(b.Register(Spec{Op: op, Scope: "act", Feature: "chats", Handler: func(_ context.Context, c Call) (json.RawMessage, error) {
					seen = append(seen, c)
					return json.RawMessage(`{}`), nil
				}})).To(Succeed())
			}
			h = b.HandlerForRuntime("sandbox", "secret", NewStaticAuthority(map[string][]string{"sandbox": {"egress.", "workspace."}}), 1, "inc1")
		})
		call := func(op string) int {
			body, _ := json.Marshal(contract.HostRequest{OperationID: "op1", Op: op, Arguments: json.RawMessage(`{}`)})
			req := httptest.NewRequest(http.MethodPost, "/host", bytes.NewReader(body))
			req.Header.Set(contract.HostCredentialHeader, "secret")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			return rec.Code
		}

		It("allows the checkpoint and the network checks of a job, marks them leased, and nothing else", func() {
			end := in.BeginRuntime(act("op1"), chat, true, "inc1")
			Expect(call("workspace.materialize")).To(Equal(http.StatusOK), "the start call itself may do anything")
			Expect(seen[0].Lease).To(BeFalse())
			Expect(in.Lease("sandbox", "op1", "inc1", "job-abcdef", time.Now().Add(time.Hour))).To(Succeed())
			end()
			Expect(call("egress.check")).To(Equal(http.StatusOK))
			Expect(call("workspace.commit")).To(Equal(http.StatusOK))
			Expect(call("workspace.materialize")).To(Equal(http.StatusForbidden))
			Expect(seen).To(HaveLen(3))
			Expect(seen[1].Lease).To(BeTrue())
			Expect(seen[1].ChatID).To(Equal("c1"))
			Expect(seen[1].Actor.UserID).To(Equal("u1"))
			in.EndLease("sandbox", "inc1", "job-abcdef")
			Expect(call("egress.check")).To(Equal(http.StatusForbidden))
		})
	})

	It("tells the egress host that a leased check is unattended", func() {
		ec, err := egressCall(Call{ResourceID: "chat-c1", OperationID: "op1", Lease: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(ec.Unattended).To(BeTrue())
		ec, _ = egressCall(Call{ResourceID: "chat-c1", OperationID: "op1"})
		Expect(ec.Unattended).To(BeFalse())
	})
})
