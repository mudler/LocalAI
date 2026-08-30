package nodes

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// Agent workers do not subscribe to the backend.* subjects, so asking one to
// list its backends can only answer "no responders". ListBackends read that as
// a node that had gone away and marked it unhealthy; the node's next heartbeat
// marked it healthy again. Every poll of the backends view therefore flapped
// every agent node in the cluster, and while it was unhealthy the router would
// not schedule onto it.
var _ = Describe("Backend listing across mixed node types", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		mc       *scriptedMessagingClient
		mgr      *DistributedBackendManager
		ctx      context.Context
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		mc = newScriptedMessagingClient()
		mgr = &DistributedBackendManager{
			local:    stubLocalBackendManager{},
			adapter:  NewRemoteUnloaderAdapter(nil, mc, 3*time.Minute, 15*time.Minute),
			registry: registry,
		}
		ctx = context.Background()
	})

	register := func(name, nodeType string) *BackendNode {
		node := &BackendNode{Name: name, NodeType: nodeType, Address: name + ":50051"}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		fetched, err := registry.GetByName(ctx, name)
		Expect(err).ToNot(HaveOccurred())
		Expect(fetched.Status).To(Equal(StatusHealthy))
		return fetched
	}

	statusOf := func(id string) string {
		n, err := registry.Get(ctx, id)
		Expect(err).ToNot(HaveOccurred())
		return n.Status
	}

	It("leaves an agent node healthy instead of flapping it", func() {
		agent := register("agent-worker-1", NodeTypeAgent)
		mc.scriptNoResponders(messaging.SubjectNodeBackendList(agent.ID))

		_, err := mgr.ListBackends()
		Expect(err).ToNot(HaveOccurred())

		Expect(statusOf(agent.ID)).To(Equal(StatusHealthy),
			"an agent node cannot answer backend.list and must not be judged on it")
	})

	It("still marks a backend node unhealthy when it does not answer", func() {
		backendNode := register("worker-a", NodeTypeBackend)
		mc.scriptNoResponders(messaging.SubjectNodeBackendList(backendNode.ID))

		_, err := mgr.ListBackends()
		Expect(err).ToNot(HaveOccurred())

		Expect(statusOf(backendNode.ID)).To(Equal(StatusUnhealthy),
			"a backend worker that does not answer is genuinely gone")
	})
})
