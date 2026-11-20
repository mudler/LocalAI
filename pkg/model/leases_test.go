// SPDX-License-Identifier: MIT
package model

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type leaseBackend struct {
	grpc.Backend
	busy  bool
	freed bool
}

func (b *leaseBackend) IsBusy() bool               { return b.busy }
func (b *leaseBackend) Free(context.Context) error { b.freed = true; return nil }

var _ = Describe("Advisory model leases", func() {
	var ml *ModelLoader
	var ctx context.Context
	var lease Lease
	var backend *leaseBackend
	BeforeEach(func() {
		ml = NewModelLoader(&system.SystemState{Model: system.Model{ModelsPath: GinkgoT().TempDir()}})
		ctx, lease = ml.BeginLease(context.Background(), "alice", time.Hour)
		ml.touchLease(ctx, "mesh")
		backend = &leaseBackend{}
		m := NewModelWithClient("mesh", "local", backend)
		m.leaseCreator = lease.ID
		ml.store.Set("mesh", m)
	})
	It("only releases the owning caller's handle", func() {
		_, err := ml.ReleaseLease(ctx, "bob", lease.ID, true)
		Expect(err).To(MatchError(ErrLeaseNotFound))
		got, err := ml.ReleaseLease(ctx, "alice", lease.ID, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].State).To(Equal("unloaded"))
		Expect(backend.freed).To(BeTrue())
		_, err = ml.ReleaseLease(ctx, "alice", lease.ID, true)
		Expect(err).To(MatchError(ErrLeaseNotFound))
	})
	It("retains busy models without waiting for inference", func() {
		backend.busy = true
		got, err := ml.ReleaseLease(ctx, "alice", lease.ID, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].Reason).To(Equal("model is busy"))
		Expect(backend.freed).To(BeFalse())
	})
	It("does not force cleanup without authorization", func() {
		got, err := ml.ReleaseLease(ctx, "alice", lease.ID, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].Reason).To(Equal("unload not authorized"))
		Expect(backend.freed).To(BeFalse())
	})
	It("does not unload a replacement instance or a previously resident model", func() {
		m, _ := ml.store.Get("mesh")
		m.leaseCreator = ""
		got, err := ml.ReleaseLease(ctx, "alice", lease.ID, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].Reason).To(ContainSubstring("not loaded by this lease"))
		Expect(backend.freed).To(BeFalse())
	})
	It("retains models used by another active lease", func() {
		other, _ := ml.BeginLease(ctx, "bob", time.Hour)
		ml.touchLease(other, "mesh")
		got, err := ml.ReleaseLease(ctx, "alice", lease.ID, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].Reason).To(Equal("another lease is active"))
		Expect(backend.freed).To(BeFalse())
	})
	It("does not claim ownership of distributed workers", func() {
		ml.remoteUnloader = leaseRemote{}
		got, err := ml.ReleaseLease(ctx, "alice", lease.ID, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].Reason).To(ContainSubstring("distributed"))
		Expect(backend.freed).To(BeFalse())
	})
	It("retains expired leases and forgets expired claims on admission", func() {
		ml.leases[lease.ID].expires = time.Now().Add(-time.Second)
		got, err := ml.ReleaseLease(ctx, "alice", lease.ID, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[0].Reason).To(Equal("lease expired"))
		Expect(backend.freed).To(BeFalse())
	})
})

type leaseRemote struct{}

func (leaseRemote) UnloadRemoteModel(string) error { panic("lease must not unload remote models") }
