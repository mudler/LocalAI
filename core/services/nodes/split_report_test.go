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
	)

	BeforeEach(func() {
		ctx = context.Background()
		db, err := sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "nodes.db")+"?_busy_timeout=5000", &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
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

		all, err := reg.SplitReports(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(all).To(HaveKey(n.ID))
		Expect(all[n.ID].Report.Workers).To(HaveLen(1))
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
