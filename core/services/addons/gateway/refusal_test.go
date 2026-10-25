// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"

	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// refusingRuntime answers every read as the add-on's own invalid-request
// refusal, the way the runtime client reports an HTTP 400 of the add-on.
type refusingRuntime struct{ contract.Runtime }

func (refusingRuntime) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, &addonruntime.Refusal{Status: 400, Reason: "invalid request"}
}

// outageRuntime is the control: an add-on that is down.
type outageRuntime struct{ contract.Runtime }

func (outageRuntime) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, addonruntime.ErrRuntime
}

// refusingBroker refuses every proposal call as the add-on's refusal.
type refusingBroker struct{ ProposalBroker }

func (refusingBroker) Propose(context.Context, contract.Actor, contract.Request) (Proposal, error) {
	return Proposal{}, &addonruntime.Refusal{Status: 400, Reason: "invalid request"}
}
func (refusingBroker) Confirm(context.Context, contract.Actor, string, string) (Proposal, error) {
	return Proposal{}, &addonruntime.Refusal{Status: 400, Reason: "invalid request"}
}

var _ = Describe("an add-on's invalid-request refusal", func() {
	ok := func(context.Context, contract.Actor) error { return nil }
	actor := contract.Actor{UserID: "u", CredentialID: "key:k", Mode: "named-user"}
	read := contract.Request{AddonID: "sample", Generation: 1, OperationID: "refused-read", Action: "read", Arguments: []byte(`{}`)}
	owner := func(rt contract.Runtime) *Gateway {
		return &Gateway{Revalidate: ok, Admission: &admitted{}, Resolver: resolved{}, Runtime: currentFixture{rt}, Proposals: refusingBroker{}}
	}
	assertInvalid := func(e error) {
		ExpectWithOffset(1, errors.Is(e, ErrInvalidRequest)).To(BeTrue(), "%v", e)
		var unknown *OutcomeUnknown
		var unavailable *ProposalUnavailable
		ExpectWithOffset(1, errors.As(e, &unknown)).To(BeFalse())
		ExpectWithOffset(1, errors.As(e, &unavailable)).To(BeFalse())
		ExpectWithOffset(1, errors.Is(e, ErrUnavailable)).To(BeFalse())
		ExpectWithOffset(1, StatusFor(e)).NotTo(Equal(http.StatusServiceUnavailable))
	}

	It("is the typed invalid request on the owner for a read, never an outage", func() {
		_, e := owner(refusingRuntime{}).Dispatch(context.Background(), actor, read)
		assertInvalid(e)
		_, e = owner(outageRuntime{}).Dispatch(context.Background(), actor, read)
		Expect(StatusFor(e)).To(Equal(http.StatusServiceUnavailable))
		Expect(errors.Is(e, ErrInvalidRequest)).To(BeFalse())
	})
	It("is the typed invalid request on the owner for a proposal call", func() {
		g := owner(refusingRuntime{})
		request := contract.Request{AddonID: "sample", ResourceID: "r", Generation: 1, OperationID: "refused-propose", Action: "write", Arguments: []byte(`{}`)}
		_, e := g.Propose(context.Background(), actor, request)
		assertInvalid(e)
		_, e = g.ConfirmProposal(context.Background(), actor, "refused-propose", "digest")
		assertInvalid(e)
	})
	It("is the typed invalid request on the owner for a json relay", func() {
		w := httptest.NewRecorder()
		e := owner(refusingRuntime{}).Relay(context.Background(), actor, RelayRequest{Request: read, Kind: "json"}, w)
		assertInvalid(e)
		Expect(w.Body.Len()).To(BeZero())
	})
	It("is the same typed answer from a forwarding frontend", func() {
		g := owner(refusingRuntime{})
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(g)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		front := cs["front-a"]
		_, e := front.Dispatch(context.Background(), actor, read)
		assertInvalid(e)
		request := contract.Request{AddonID: "sample", ResourceID: "r", Generation: 1, OperationID: "refused-propose", Action: "write", Arguments: []byte(`{}`)}
		_, e = front.Proposal(context.Background(), actor, request, ProposalCall{Op: proposalPropose, ID: request.OperationID})
		assertInvalid(e)
		_, e = front.Proposal(context.Background(), actor, contract.Request{AddonID: "sample", ResourceID: "r", Generation: 1, OperationID: "refused-propose", Action: "write", Arguments: []byte(`{}`)}, ProposalCall{Op: proposalConfirm, ID: "refused-propose", Digest: "digest"})
		assertInvalid(e)
		w := httptest.NewRecorder()
		e = front.Relay(context.Background(), actor, RelayRequest{Request: read, Kind: "json"}, w)
		assertInvalid(e)
		Expect(w.Body.Len()).To(BeZero())
	})
})
