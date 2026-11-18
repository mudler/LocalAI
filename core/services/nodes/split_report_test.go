package nodes

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

var _ = Describe("Split worker reports of the nodes", func() {
	var (
		ctx context.Context
		reg *NodeRegistry
		db  *gorm.DB
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		db, err = sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "nodes.db")+"?_busy_timeout=5000", &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).ToNot(HaveOccurred())
		reg, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	It("keeps the last report of each node, readable by any frontend", func() {
		n := &BackendNode{Name: "gpu-1", NodeType: NodeTypeBackend}
		Expect(reg.Register(ctx, n, true)).To(Succeed())

		_, ok, err := reg.SplitReport(ctx, n.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeFalse(), "a node that never reported has no report")

		first := workerctl.SplitReport{PeerID: "peer-a", Addrs: []string{"/ip4/10.0.0.2/tcp/4001"}}
		Expect(reg.SetSplitReport(ctx, n.ID, first)).To(Succeed())
		second := workerctl.SplitReport{PeerID: "peer-a", Addrs: []string{"/ip4/10.0.0.2/tcp/4001"},
			Workers: []workerctl.SplitWorkerReport{{DeviceID: "d1", State: "online", MemoryBytes: 1 << 30}}}
		Expect(reg.SetSplitReport(ctx, n.ID, second)).To(Succeed())

		got, ok, err := reg.SplitReport(ctx, n.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(got.Report).To(Equal(second))
		Expect(got.UpdatedAt).ToNot(BeZero())

		// A second frontend on the same database reads the same report.
		other, err := NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		all, err := other.SplitReports(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(all).To(HaveKey(n.ID))
		Expect(all[n.ID].Report.Workers).To(HaveLen(1))
	})

	It("pins the split host of a node to the first peer that reports it, until the node registers again", func() {
		n := &BackendNode{Name: "gpu-pin", NodeType: NodeTypeBackend}
		Expect(reg.Register(ctx, n, true)).To(Succeed())
		Expect(reg.PinSplitPeer(ctx, n.ID, "peer-a")).To(Succeed())
		Expect(reg.PinSplitPeer(ctx, n.ID, "peer-a")).To(Succeed(), "the same peer again is fine")
		Expect(reg.SetSplitReport(ctx, n.ID, workerctl.SplitReport{PeerID: "peer-a"})).To(Succeed())

		// Another caller with the registration token cannot move the node to
		// its own peer, through this frontend or another one.
		Expect(reg.PinSplitPeer(ctx, n.ID, "peer-b")).To(MatchError(ErrSplitPeerPinned))
		other, err := NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		Expect(other.PinSplitPeer(ctx, n.ID, "peer-b")).To(MatchError(ErrSplitPeerPinned))
		got, ok, err := reg.SplitReport(ctx, n.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(got.PinnedPeer).To(Equal("peer-a"))
		Expect(got.Report.PeerID).To(Equal("peer-a"))

		// A node that registers again (it restarted, maybe with a new key)
		// may bind a new peer.
		again := &BackendNode{Name: "gpu-pin", NodeType: NodeTypeBackend}
		Expect(reg.Register(ctx, again, true)).To(Succeed())
		Expect(again.ID).To(Equal(n.ID))
		Expect(reg.PinSplitPeer(ctx, n.ID, "peer-b")).To(Succeed())
		got, _, err = reg.SplitReport(ctx, n.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.PinnedPeer).To(Equal("peer-b"))
	})

	It("stores the split server declaration of the worker, and keeps the admin's choice over the start flag", func() {
		n := &BackendNode{Name: "gpu-decl", NodeType: NodeTypeBackend, SplitServer: true}
		Expect(reg.Register(ctx, n, true)).To(Succeed())
		got, err := reg.Get(ctx, n.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.SplitServer).To(BeTrue(), "the start flag declares the worker")

		// A restart without the flag clears it while the admin has not chosen.
		Expect(reg.Register(ctx, &BackendNode{Name: "gpu-decl", NodeType: NodeTypeBackend}, true)).To(Succeed())
		got, _ = reg.Get(ctx, n.ID)
		Expect(got.SplitServer).To(BeFalse())

		// The admin declares it; a restart without the flag keeps that.
		Expect(reg.SetSplitServer(ctx, n.ID, true)).To(Succeed())
		Expect(reg.Register(ctx, &BackendNode{Name: "gpu-decl", NodeType: NodeTypeBackend}, true)).To(Succeed())
		got, _ = reg.Get(ctx, n.ID)
		Expect(got.SplitServer).To(BeTrue())
		Expect(got.SplitServerManuallySet).To(BeTrue())

		// Reset hands the choice back to the flag at the next registration.
		Expect(reg.ResetSplitServer(ctx, n.ID)).To(Succeed())
		Expect(reg.Register(ctx, &BackendNode{Name: "gpu-decl", NodeType: NodeTypeBackend}, true)).To(Succeed())
		got, _ = reg.Get(ctx, n.ID)
		Expect(got.SplitServer).To(BeFalse())
		Expect(got.SplitServerManuallySet).To(BeFalse())

		Expect(reg.SetSplitServer(ctx, "nope", true)).To(MatchError(ContainSubstring("not found")))
	})

	It("drops the report of a node that is removed", func() {
		n := &BackendNode{Name: "gpu-2", NodeType: NodeTypeBackend}
		Expect(reg.Register(ctx, n, true)).To(Succeed())
		Expect(reg.SetSplitReport(ctx, n.ID, workerctl.SplitReport{PeerID: "peer-b"})).To(Succeed())
		Expect(reg.Deregister(ctx, n.ID)).To(Succeed())
		_, ok, err := reg.SplitReport(ctx, n.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeFalse())
	})
})
