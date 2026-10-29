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

// denyingRuntime answers every read as the add-on's own denial of the person,
// the way the runtime client reports the 403 "forbidden" of an add-on.
type denyingRuntime struct{ contract.Runtime }

func (denyingRuntime) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, addonruntime.ErrDenied
}

// unreachableAdmission denies every resource as the policy does.
type unreachableAdmission struct{}

func (unreachableAdmission) Admit(context.Context, contract.Actor, ResourceKey, string, func(context.Context) error) error {
	return ErrResourceUnreachable
}

// failingResolver fails to resolve every resource with err.
type failingResolver struct{ err error }

func (f failingResolver) Resolve(context.Context, contract.Actor, contract.Request) (ResourceKey, error) {
	return ResourceKey{}, f.err
}
func (failingResolver) Output(context.Context, contract.Actor, contract.Request, string) (contract.OutputDescription, error) {
	return contract.OutputDescription{}, ErrForbidden
}

var _ = Describe("a denial that says which denial it is", func() {
	ok := func(context.Context, contract.Actor) error { return nil }
	actor := contract.Actor{UserID: "u", CredentialID: "key:k", Mode: "named-user"}
	read := contract.Request{AddonID: "sample", Generation: 1, OperationID: "denied-read", Action: "read", Arguments: []byte(`{}`)}
	frontend := func(g *Gateway) *Carrier {
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(g)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		return cs["front-a"]
	}
	It("is not permitted when the add-on denies the person, on the owner and on a forwarding frontend", func() {
		g := &Gateway{Revalidate: ok, Admission: &admitted{}, Resolver: resolved{}, Runtime: currentFixture{denyingRuntime{}}}
		_, e := g.Dispatch(context.Background(), actor, read)
		Expect(e).To(MatchError(ErrNotPermitted))
		Expect(StatusFor(e)).To(Equal(http.StatusForbidden))
		_, e = frontend(g).Dispatch(context.Background(), actor, read)
		Expect(e).To(MatchError(ErrNotPermitted))
	})
	It("is an unreachable resource when the policy denies it, on the owner and on a forwarding frontend", func() {
		g := &Gateway{Revalidate: ok, Admission: unreachableAdmission{}, Resolver: resolved{}, Runtime: currentFixture{refusingRuntime{}}}
		_, e := g.Dispatch(context.Background(), actor, read)
		Expect(e).To(MatchError(ErrResourceUnreachable))
		_, e = frontend(g).Dispatch(context.Background(), actor, read)
		Expect(e).To(MatchError(ErrResourceUnreachable))
		Expect(StatusFor(e)).To(Equal(http.StatusForbidden))
	})
	It("types the denial of a read like the denial of a write, on the owner and on a forwarding frontend", func() {
		g := &Gateway{Revalidate: ok, Admission: &admitted{}, Resolver: failingResolver{ErrResourceUnreachable}, Runtime: currentFixture{refusingRuntime{}}}
		_, e := g.Dispatch(context.Background(), actor, read)
		Expect(e).To(MatchError(ErrResourceUnreachable))
		Expect(StatusFor(e)).To(Equal(http.StatusForbidden))
		_, e = frontend(g).Dispatch(context.Background(), actor, read)
		Expect(e).To(MatchError(ErrResourceUnreachable))
	})
	It("keeps any other failure to resolve a resource a plain denial, so nothing more is told", func() {
		for _, cause := range []error{errors.New("record store: row 7 is held by another user"), ErrUnavailable, ErrQuotaReached} {
			g := &Gateway{Revalidate: ok, Admission: &admitted{}, Resolver: failingResolver{cause}, Runtime: currentFixture{refusingRuntime{}}}
			_, e := g.Dispatch(context.Background(), actor, read)
			Expect(e).To(Equal(ErrForbidden), cause.Error())
		}
	})
})
