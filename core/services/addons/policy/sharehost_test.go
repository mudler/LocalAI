// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/gateway/carriertest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ownerShares is the owner's grant changes of the /api/shares routes, cut to
// what these tests call: the change goes to the policy as the resource
// service makes it.
type ownerShares struct{ p *Policy }

func (o ownerShares) Shares(context.Context, contract.Actor, string) (gateway.ShareIndex, error) {
	return gateway.ShareIndex{}, gateway.ErrForbidden
}

func (o ownerShares) ShareGrants(ctx context.Context, a contract.Actor, kind, id string) (gateway.ShareList, error) {
	m, err := o.p.GrantsRef(ctx, a, Ref{Kind: kind, ResourceID: id})
	out := gateway.ShareList{Kind: kind, ResourceID: id}
	for u, r := range m {
		out.Grants = append(out.Grants, gateway.Grant{UserID: u, Role: string(r)})
	}
	return out, err
}

func (o ownerShares) SetShare(ctx context.Context, a contract.Actor, kind, id, principal, role string) (gateway.ShareList, error) {
	if err := o.p.SetGrantRef(ctx, a, Ref{Kind: kind, ResourceID: id}, principal, Role(role)); err != nil {
		return gateway.ShareList{}, err
	}
	return o.ShareGrants(ctx, a, kind, id)
}

func (o ownerShares) RevokeShare(ctx context.Context, a contract.Actor, kind, id, principal string) (gateway.ShareList, error) {
	if err := o.p.RevokeRef(ctx, a, Ref{Kind: kind, ResourceID: id}, principal); err != nil {
		return gateway.ShareList{}, err
	}
	return o.ShareGrants(ctx, a, kind, id)
}

func (o ownerShares) SearchShareUsers(context.Context, contract.Actor, string, string, string, int) (gateway.UserList, error) {
	return gateway.UserList{}, gateway.ErrForbidden
}

// cluster is an owner that holds the policy and two frontends that reach it
// through the real carrier.
type cluster struct {
	f      *fixture
	host   *ShareHost
	net    *carriertest.Fixture
	fronts map[string]Forwarded
}

func newCluster(wait, grace, drain time.Duration) *cluster {
	f := newFixture(ModeNamedUser)
	f.opts.DrainTimeout = drain
	f.policy = New(f.store, f.opts)
	Expect(f.policy.RegisterKind(boardKind, boardSpec(anyone))).To(Succeed())
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	host := NewShareHost(ctx, f.policy)
	host.Wait, host.Grace = wait, grace
	allow := func(context.Context, contract.Actor) error { return nil }
	net, err := carriertest.New(func() *gateway.Gateway {
		return &gateway.Gateway{Revalidate: allow, RevalidateAccount: allow, Sharing: ownerShares{f.policy}, ShareAuthority: host}
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(net.Close)
	c := &cluster{f: f, host: host, net: net, fronts: map[string]Forwarded{}}
	for _, id := range []string{carriertest.FrontA, carriertest.FrontB} {
		c.fronts[id] = Forwarded{Gateway: net.Frontend(id)}
	}
	return c
}

// remoteRun is a run on a frontend that ends when its context ends, and records
// when it stopped.
type remoteRun struct {
	started chan struct{}
	stopped atomic.Bool
	result  chan error
}

// hold admits actor on the frontend and keeps the run open until it is
// cancelled. stall keeps it open that much longer after the cancel.
func hold(s Sharing, actor contract.Actor, ref Ref, action Action, stall time.Duration) *remoteRun {
	h := &remoteRun{started: make(chan struct{}), result: make(chan error, 1)}
	go func() {
		h.result <- s.AdmitRef(context.Background(), actor, ref, action, func(run context.Context) error {
			close(h.started)
			<-run.Done()
			time.Sleep(stall)
			h.stopped.Store(true)
			return nil
		})
	}()
	return h
}

func (h *remoteRun) running() {
	select {
	case <-h.started:
	case err := <-h.result:
		Fail("the run was not admitted: " + errString(err))
	case <-time.After(5 * time.Second):
		Fail("the run was not admitted in time")
	}
}

func errString(err error) string {
	if err == nil {
		return "no error"
	}
	return err.Error()
}

var _ = Describe("sharing of a kind from any frontend", func() {
	bg := context.Background()
	owner, bob := named("owner"), named("bob")

	It("looks a resource shared on the owner up from a second frontend, with its owner", func() {
		c := newCluster(time.Second, time.Second, 2*time.Second)
		ref, err := c.fronts[carriertest.FrontA].CreateRef(bg, owner, boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(ref.Kind).To(Equal(boardKind))
		Expect(c.f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
		views, err := c.fronts[carriertest.FrontB].ListingKind(bg, bob, boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(views).To(Equal([]ShareView{{Ref: ref, Role: RoleUse, OwnerID: "owner"}}))
		mine, err := c.fronts[carriertest.FrontB].ListingKind(bg, owner, boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(mine).To(Equal([]ShareView{{Ref: ref, Role: RoleOwner, OwnerID: "owner", Shared: 1}}))
		grants, err := c.fronts[carriertest.FrontA].GrantsRef(bg, owner, ref)
		Expect(err).NotTo(HaveOccurred())
		Expect(grants).To(Equal(map[string]Role{"bob": RoleUse}))
		// A stranger sees nothing and an unknown kind is the plain denial.
		views, err = c.fronts[carriertest.FrontB].ListingKind(bg, named("eve"), boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(views).To(BeEmpty())
		_, err = c.fronts[carriertest.FrontB].ListingKind(bg, bob, "nobody:kind")
		Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue())
	})

	It("stops a grantee's run on one frontend before a revoke through the other frontend answers", func() {
		c := newCluster(time.Second, time.Second, 3*time.Second)
		ref, err := c.f.policy.CreateRef(bg, owner, boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
		run := hold(c.fronts[carriertest.FrontB], bob, ref, ActionOutput, 200*time.Millisecond)
		run.running()
		Expect(c.fronts[carriertest.FrontA].RevokeRef(bg, owner, ref, "bob")).To(Succeed())
		Expect(run.stopped.Load()).To(BeTrue(), "the run stopped before the revoke answered")
		Eventually(run.result).Should(Receive(BeNil()))
		// The next run is refused and runs nothing.
		err = c.fronts[carriertest.FrontB].AdmitRef(bg, bob, ref, ActionOutput, func(context.Context) error {
			Fail("ran after the revoke")
			return nil
		})
		Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue())
	})

	It("stops the work a lowering decided on the owner takes away, and keeps what the new role allows", func() {
		c := newCluster(time.Second, time.Second, 3*time.Second)
		ref, err := c.f.policy.CreateRef(bg, owner, boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.f.policy.SetGrantRef(bg, owner, ref, "bob", RoleManage)).To(Succeed())
		write := hold(c.fronts[carriertest.FrontB], bob, ref, ActionWrite, 100*time.Millisecond)
		write.running()
		read := hold(c.fronts[carriertest.FrontA], bob, ref, ActionRead, 0)
		read.running()
		Expect(c.f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
		Expect(write.stopped.Load()).To(BeTrue())
		Consistently(read.result, 300*time.Millisecond).ShouldNot(Receive())
		Expect(read.stopped.Load()).To(BeFalse())
	})

	It("answers ErrDrainPending when the frontend does not stop the run within the bound", func() {
		c := newCluster(time.Second, time.Second, 300*time.Millisecond)
		ref, err := c.f.policy.CreateRef(bg, owner, boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
		run := hold(c.fronts[carriertest.FrontB], bob, ref, ActionOutput, 1500*time.Millisecond)
		run.running()
		Expect(c.f.policy.RevokeRef(bg, owner, ref, "bob")).To(MatchError(ErrDrainPending))
		Eventually(run.result, 5*time.Second).Should(Receive(BeNil()))
		// Once the run ended, the same call settles.
		Expect(c.f.policy.RevokeRef(bg, owner, ref, "bob")).To(Succeed())
	})

	It("expires the admission of a frontend that stopped renewing it", func() {
		c := newCluster(200*time.Millisecond, 300*time.Millisecond, 5*time.Second)
		ref, err := c.f.policy.CreateRef(bg, owner, boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
		// A frontend admitted a run and died before it watched once.
		lease, err := c.host.AdmitShare(bg, bob, boardKind, ref.ResourceID, string(ActionOutput))
		Expect(err).NotTo(HaveOccurred())
		Expect(c.host.leaseCount()).To(Equal(1))
		Eventually(c.host.leaseCount, 2*time.Second).Should(BeZero())
		start := time.Now()
		Expect(c.f.policy.RevokeRef(bg, owner, ref, "bob")).To(Succeed())
		Expect(time.Since(start)).To(BeNumerically("<", time.Second), "nothing was left to wait for")
		state, err := c.host.WatchShare(bg, bob, lease.Lease, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(state.Lost).To(BeTrue())

		// A frontend that watched and then went silent expires as well.
		Expect(c.f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
		lease, err = c.host.AdmitShare(bg, bob, boardKind, ref.ResourceID, string(ActionOutput))
		Expect(err).NotTo(HaveOccurred())
		state, err = c.host.WatchShare(bg, bob, lease.Lease, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(state.Lost).To(BeFalse())
		Eventually(c.host.leaseCount, 2*time.Second).Should(BeZero())
	})

	It("keeps a lease to the person who holds it and releases it once", func() {
		c := newCluster(200*time.Millisecond, 5*time.Second, 2*time.Second)
		ref, err := c.f.policy.CreateRef(bg, owner, boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
		lease, err := c.host.AdmitShare(bg, bob, boardKind, ref.ResourceID, string(ActionOutput))
		Expect(err).NotTo(HaveOccurred())
		state, err := c.host.WatchShare(bg, named("eve"), lease.Lease, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(state.Lost).To(BeTrue(), "another person's lease is the same as no lease")
		Expect(c.host.ReleaseShare(bg, named("eve"), lease.Lease)).To(Succeed())
		Expect(c.host.leaseCount()).To(Equal(1))
		Expect(c.host.ReleaseShare(bg, bob, lease.Lease)).To(Succeed())
		Eventually(c.host.leaseCount).Should(BeZero())
		Expect(c.host.ReleaseShare(bg, bob, lease.Lease)).To(Succeed())
		_, err = c.host.AdmitShare(bg, named("eve"), boardKind, ref.ResourceID, string(ActionOutput))
		Expect(errors.Is(err, ErrForbidden)).To(BeTrue())
		Expect(c.host.leaseCount()).To(BeZero())
	})

	It("fails closed with the typed unavailable answer when the owner is gone, and stops a running turn", func() {
		c := newCluster(200*time.Millisecond, 5*time.Second, 2*time.Second)
		ref, err := c.f.policy.CreateRef(bg, owner, boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
		run := hold(c.fronts[carriertest.FrontB], bob, ref, ActionOutput, 0)
		run.running()
		c.net.Close()
		Eventually(run.result, 5*time.Second).Should(Receive(BeNil()))
		Expect(run.stopped.Load()).To(BeTrue())

		front := c.fronts[carriertest.FrontB]
		var ran atomic.Bool
		err = front.AdmitRef(bg, bob, ref, ActionOutput, func(context.Context) error { ran.Store(true); return nil })
		Expect(errors.Is(err, gateway.ErrUnavailable)).To(BeTrue(), errString(err))
		Expect(ran.Load()).To(BeFalse())
		_, err = front.ListingKind(bg, bob, boardKind)
		Expect(errors.Is(err, gateway.ErrUnavailable)).To(BeTrue(), errString(err))
		_, err = front.CreateRef(bg, owner, boardKind)
		Expect(errors.Is(err, gateway.ErrUnavailable)).To(BeTrue(), errString(err))
		Expect(front.Authorize(bg, bob, ResourceKey{AddonID: "tables", ResourceID: ref.ResourceID}, ActionRead)).To(HaveOccurred())
	})

	It("keeps one record for a name through either frontend, lists it with the name, and forgets it after the grantee's run stopped", func() {
		c := newCluster(200*time.Millisecond, 5*time.Second, 2*time.Second)
		a, b := c.fronts[carriertest.FrontA], c.fronts[carriertest.FrontB]
		ref, err := a.EnsureRef(bg, owner, boardKind, "notes")
		Expect(err).NotTo(HaveOccurred())
		again, err := b.EnsureRef(bg, owner, boardKind, "notes")
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(ref))
		found, ok := c.f.policy.FindRef(boardKind, "owner", "notes")
		Expect(ok).To(BeTrue())
		Expect(found).To(Equal(ref))
		_, err = a.EnsureRef(bg, owner, boardKind, "bad\nname")
		Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue(), errString(err))
		Expect(c.f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
		views, err := b.ListingKind(bg, bob, boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(views).To(Equal([]ShareView{{Ref: ref, Role: RoleUse, OwnerID: "owner", Local: "notes"}}))

		run := hold(b, bob, ref, ActionOutput, 100*time.Millisecond)
		run.running()
		Expect(errors.Is(b.ForgetRef(bg, bob, ref), gateway.ErrForbidden)).To(BeTrue(), "only the owner forgets a record")
		Expect(a.ForgetRef(bg, owner, ref)).To(Succeed())
		Expect(run.stopped.Load()).To(BeTrue(), "the record went only after the grantee's run stopped")
		_, ok = c.f.policy.FindRef(boardKind, "owner", "notes")
		Expect(ok).To(BeFalse())
		views, err = b.ListingKind(bg, bob, boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(views).To(BeEmpty())
	})
})
