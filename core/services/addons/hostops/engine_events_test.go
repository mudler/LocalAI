// SPDX-License-Identifier: MIT
package hostops

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeEvents struct {
	calls []EmitCall
	err   error
}

func (f *fakeEvents) Emit(_ context.Context, c EmitCall) error {
	f.calls = append(f.calls, c)
	return f.err
}

var _ = Describe("events.emit", func() {
	var (
		f    *fixture
		host *fakeEvents
	)
	BeforeEach(func() {
		host = &fakeEvents{}
		f = &fixture{inflight: NewInFlight(), seen: &Call{}}
		f.b = New(f.inflight, func(_, feature string) bool { return feature == "chats" }, contract.DefaultLimits())
		Expect(RegisterEvents(f.b, host)).To(Succeed())
		f.h = f.b.Handler("notes", "secret", []string{"events."})
	})
	live := func(op string, actor contract.Actor) {
		e := env("notes", op, "read", time.Now().Add(time.Minute))
		e.Authority.Actor = actor
		DeferCleanup(f.inflight.Begin(e))
	}
	owner := contract.Actor{UserID: "owner-1", CredentialID: "c1", Mode: "named-user"}

	It("hands the event to the host with the owner and credential of the live call", func() {
		live("op1", owner)
		code, out := f.call("secret", "op1", "events.emit", `{"type":"notes.item.created","payload":{"id":"7","name":"Report"},"dedupe_key":"k1"}`)
		Expect(code).To(Equal(http.StatusOK), "%+v", out.Error)
		Expect(host.calls).To(HaveLen(1))
		c := host.calls[0]
		Expect(c.AddonID).To(Equal("notes"))
		Expect(c.OwnerID).To(Equal("owner-1"))
		Expect(c.CredentialID).To(Equal("c1"))
		Expect(c.Type).To(Equal("notes.item.created"))
		Expect(c.DedupeKey).To(Equal("k1"))
		Expect(string(c.Payload)).To(Equal(`{"id":"7","name":"Report"}`))
	})

	It("lets a paired client's call report a change", func() {
		live("op1", contract.Actor{UserID: "owner-1", CredentialID: contract.ClientCredentialPrefix + "k9", Mode: "named-user"})
		code, _ := f.call("secret", "op1", "events.emit", `{"type":"notes.item.created","payload":{"id":"7"}}`)
		Expect(code).To(Equal(http.StatusOK))
		Expect(host.calls[0].CredentialID).To(Equal(contract.ClientCredentialPrefix + "k9"))
	})

	It("refuses an argument that tries to name who made the change, and a bad payload", func() {
		live("op1", owner)
		for _, body := range []string{
			`{"type":"notes.item.created","payload":{"id":"7"},"actor":"owner"}`,
			`{"type":"notes.item.created","payload":{"id":"7"},"user":"bob"}`,
			`{"type":"","payload":{"id":"7"}}`,
			`{"type":"notes.item.created","payload":[1,2]}`,
			`{"type":"notes.item.created","payload":"text"}`,
			`{"type":"notes.item.created"}`,
			`{"type":"notes.item.created","payload":{"id":"7"},"dedupe_key":"a` + "\\u0001" + `"}`,
		} {
			code, _ := f.call("secret", "op1", "events.emit", body)
			Expect(code).To(Equal(http.StatusBadRequest), body)
		}
		Expect(host.calls).To(BeEmpty())
	})

	It("refuses a payload larger than 256 KiB", func() {
		live("op1", owner)
		big := strings.Repeat("x", contract.MaxEventPayload)
		code, _ := f.call("secret", "op1", "events.emit", fmt.Sprintf(`{"type":"notes.item.created","payload":{"t":%q}}`, big))
		Expect(code).NotTo(Equal(http.StatusOK))
		Expect(host.calls).To(BeEmpty())
	})

	It("passes a refusal of the host back as a bad request", func() {
		live("op1", owner)
		host.err = fmt.Errorf("%w: the add-on does not declare this event", ErrBadRequest)
		code, _ := f.call("secret", "op1", "events.emit", `{"type":"notes.item.gone","payload":{"id":"7"}}`)
		Expect(code).To(Equal(http.StatusBadRequest))
	})

	It("needs a live call", func() {
		code, _ := f.call("secret", "nope", "events.emit", `{"type":"notes.item.created","payload":{"id":"7"}}`)
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(host.calls).To(BeEmpty())
	})
})
