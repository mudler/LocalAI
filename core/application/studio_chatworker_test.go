package application

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/nodes"
)

var _ = Describe("chat worker node", func() {
	var reg *nodes.NodeRegistry

	BeforeEach(func() {
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "nodes.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).NotTo(HaveOccurred())
		reg, err = nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		reg.SetHeartbeatCheckpoint(0) // a write per beat, so the beats show
	})

	get := func(name string) *nodes.BackendNode {
		n, err := reg.GetByName(context.Background(), name)
		Expect(err).NotTo(HaveOccurred())
		return n
	}

	It("appears in the node list as a healthy agent node with the chat worker role", func() {
		n, err := registerChatWorkerNode(context.Background(), reg, "chat-a", "replica-a", time.Hour)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(n.stop)
		node := get("chat-a")
		Expect(node.NodeType).To(Equal(nodes.NodeTypeAgent))
		Expect(node.Status).To(Equal(nodes.StatusHealthy))
		labels, err := reg.GetNodeLabels(context.Background(), node.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(labels).To(ContainElement(SatisfyAll(
			HaveField("Key", "node.role"), HaveField("Value", ChatWorkerRole))))
	})

	It("names its replica, and is listed for a start while that replica is live", func() {
		n, err := registerChatWorkerNode(context.Background(), reg, "chat-a", "replica-a", time.Hour)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(n.stop)
		m, err := registerChatWorkerNode(context.Background(), reg, "chat-b", "replica-b", time.Hour)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(m.stop)
		// A node with the instance label and another role is not a chat worker.
		other := &nodes.BackendNode{Name: "agent-1", NodeType: nodes.NodeTypeAgent}
		Expect(reg.Register(context.Background(), other, true)).To(Succeed())
		Expect(reg.SetNodeLabel(context.Background(), other.ID, chatWorkerInstanceLabel, "replica-c")).To(Succeed())

		live := []string{"replica-a", "replica-c", "frontend-1"}
		members := chatWorkerInstances(reg, func(context.Context) ([]string, error) { return live, nil })
		got, err := members(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ConsistOf("replica-a"), "replica-b is not live, and replica-c is no chat worker")
	})

	It("lists only healthy agent nodes, whatever labels another node carries", func() {
		n, err := registerChatWorkerNode(context.Background(), reg, "chat-a", "replica-a", time.Hour)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(n.stop)
		labelled := func(name, nodeType, instance string) *nodes.BackendNode {
			GinkgoHelper()
			node := &nodes.BackendNode{Name: name, NodeType: nodeType}
			Expect(reg.Register(context.Background(), node, true)).To(Succeed())
			Expect(reg.SetNodeLabels(context.Background(), node.ID, map[string]string{
				"node.role": ChatWorkerRole, chatWorkerInstanceLabel: instance,
			})).To(Succeed())
			return node
		}
		// A backend node that an admin gave the labels of a chat worker.
		labelled("backend-1", nodes.NodeTypeBackend, "replica-b")
		// A chat worker that went offline, while its replica is still listed live.
		gone := labelled("chat-c", nodes.NodeTypeAgent, "replica-c")
		Expect(reg.MarkOffline(context.Background(), gone.ID)).To(Succeed())

		members := chatWorkerInstances(reg, func(context.Context) ([]string, error) {
			return []string{"replica-a", "replica-b", "replica-c"}, nil
		})
		got, err := members(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ConsistOf("replica-a"))
	})

	It("does not list the live replicas when no node names a chat worker", func() {
		members := chatWorkerInstances(reg, func(context.Context) ([]string, error) {
			Fail("the live replicas were listed although no chat worker exists")
			return nil, nil
		})
		got, err := members(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
	})

	It("gets no model: the scheduler skips it", func() {
		n, err := registerChatWorkerNode(context.Background(), reg, "chat-a", "replica-a", time.Hour)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(n.stop)
		_, err = reg.FindLeastLoadedNode(context.Background())
		Expect(err).To(HaveOccurred(), "a chat worker was offered for a model")
		_, err = reg.FindNodeWithVRAM(context.Background(), 0)
		Expect(err).To(HaveOccurred(), "a chat worker was offered for a model")
	})

	It("beats while it runs and goes offline when it stops", func() {
		n, err := registerChatWorkerNode(context.Background(), reg, "chat-a", "replica-a", 10*time.Millisecond)
		Expect(err).NotTo(HaveOccurred())
		first := get("chat-a").LastHeartbeat
		Eventually(func() time.Time { return get("chat-a").LastHeartbeat }).
			WithTimeout(5 * time.Second).Should(BeTemporally(">", first))
		n.stop()
		Expect(get("chat-a").Status).To(Equal(nodes.StatusOffline))
	})

	It("keeps its row across a restart", func() {
		n, err := registerChatWorkerNode(context.Background(), reg, "chat-a", "replica-a", time.Hour)
		Expect(err).NotTo(HaveOccurred())
		id := get("chat-a").ID
		n.stop()
		n, err = registerChatWorkerNode(context.Background(), reg, "chat-a", "replica-a", time.Hour)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(n.stop)
		node := get("chat-a")
		Expect(node.ID).To(Equal(id))
		Expect(node.Status).To(Equal(nodes.StatusHealthy))
	})

	It("is named after a stable instance ID, else after the host", func() {
		Expect(chatWorkerNodeName(config.DistributedConfig{InstanceID: "w1"})).To(Equal("chat-w1"))
		generated := chatWorkerNodeName(config.DistributedConfig{InstanceID: "f00", InstanceIDGenerated: true})
		Expect(generated).To(HavePrefix("chat-"))
		Expect(generated).NotTo(Equal("chat-f00"))
	})
})
