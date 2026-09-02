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
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// Agent workers hold no tunnel and serve no control plane, so asking one to
// list its backends can only fail. ListBackends read that failure as a node
// that had gone away and marked it unhealthy; the node's next heartbeat marked
// it healthy again. Every poll of the backends view therefore flapped every
// agent node in the cluster, and while it was unhealthy the router would not
// schedule onto it.
var _ = Describe("Backend listing across mixed node types", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		mc       *scriptedControlWorkers
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
		mc = newScriptedControlWorkers()
		mgr = &DistributedBackendManager{
			local:    stubLocalBackendManager{},
			adapter:  NewRemoteUnloaderAdapter(nil, nil, mc.controlClient(), 3*time.Minute, 15*time.Minute),
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
		mc.scriptUnroutable(agent.ID)

		_, err := mgr.ListBackends()
		Expect(err).ToNot(HaveOccurred())

		Expect(statusOf(agent.ID)).To(Equal(StatusHealthy),
			"an agent node serves no control plane and must not be judged on it")
		Expect(mc.callSubjects()).To(BeEmpty(),
			"an agent node must not be asked a backend-worker verb at all")
	})

	// The direction changed with the carrier, and the change is the safety
	// property rather than a regression. "No responders" meant the worker was
	// not on the bus; a failed control RPC means THIS frontend could not route
	// to it, which is equally what a healthy worker re-homing its tunnel
	// between frontend replicas produces. Demoting on that is the fleet-wide
	// eviction this phase exists to prevent, so a node that does not answer is
	// skipped and left alone. cluster.Presence, read identically on every
	// replica from the database, is what may say a worker is gone.
	It("leaves a BACKEND node healthy when its control RPC could not be routed", func() {
		backendNode := register("worker-a", NodeTypeBackend)
		mc.scriptUnroutable(backendNode.ID)

		_, err := mgr.ListBackends()
		Expect(err).ToNot(HaveOccurred())

		Expect(statusOf(backendNode.ID)).To(Equal(StatusHealthy),
			"a route this frontend could not open is not evidence the worker has gone")
	})

	It("still reports the backends of a node that does answer", func() {
		backendNode := register("worker-b", NodeTypeBackend)
		mc.scriptReply(controlKey(backendNode.ID, workerctl.PathBackendList),
			messaging.BackendListReply{Backends: []messaging.NodeBackendInfo{{Name: "vllm"}}})

		backends, err := mgr.ListBackends()
		Expect(err).ToNot(HaveOccurred())
		Expect(backends).To(HaveKey("vllm"))
	})
})
