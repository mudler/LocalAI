package distributed_test

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nats-io/nats.go"
	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	pgdriver "gorm.io/driver/postgres"
	gormDB "gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// trackingTestLLM is a minimal gRPC backend for router tracking tests.
type trackingTestLLM struct {
	base.Base
	loaded bool
}

func (t *trackingTestLLM) Load(opts *pb.ModelOptions) error {
	t.loaded = true
	return nil
}

func (t *trackingTestLLM) Predict(opts *pb.PredictOptions) (string, error) {
	return "ok", nil
}

var _ = Describe("SmartRouter trackingKey", Label("Distributed"), func() {
	var (
		ctx           context.Context
		pgContainer   *tcpostgres.PostgresContainer
		natsContainer *tcnats.NATSContainer
		nc            *messaging.Client
		db            *gormDB.DB
		registry      *nodes.NodeRegistry
		router        *nodes.SmartRouter
		grpcCleanup   func()
		grpcAddr      string
		nodeID        string
	)

	BeforeEach(func() {
		ctx = context.Background()

		// Start PostgreSQL
		var err error
		pgContainer, err = tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("localai_tracking_test"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(30*time.Second),
			),
		)
		Expect(err).ToNot(HaveOccurred())

		pgURL, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
		Expect(err).ToNot(HaveOccurred())

		db, err = gormDB.Open(pgdriver.Open(pgURL), &gormDB.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		// Start NATS for backend.install mock
		natsContainer, err = tcnats.Run(ctx, "nats:2-alpine")
		Expect(err).ToNot(HaveOccurred())

		natsURL, err := natsContainer.ConnectionString(ctx)
		Expect(err).ToNot(HaveOccurred())

		nc, err = messaging.New(natsURL)
		Expect(err).ToNot(HaveOccurred())

		// Mock backend.install handler — always replies success
		nc.Conn().Subscribe("nodes.*.backend.install", func(msg *nats.Msg) {
			reply := messaging.BackendInstallReply{Success: true}
			data, _ := json.Marshal(reply)
			msg.Respond(data)
		})

		// Start a mock gRPC backend using the same helper as full flow tests
		llm := &trackingTestLLM{}
		grpcAddr, grpcCleanup, err = startTestGRPCServer(grpcPkg.AIModel(llm))
		Expect(err).ToNot(HaveOccurred())

		// Register a node pointing to the mock backend
		node := &nodes.BackendNode{
			Name: "tracking-node", Address: grpcAddr,
		}
		Expect(registry.Register(node, true)).To(Succeed())
		nodeID = node.ID

		router = nodes.NewSmartRouter(registry)
		unloader := nodes.NewRemoteUnloaderAdapter(registry, nc)
		router.SetUnloader(unloader)
	})

	AfterEach(func() {
		if grpcCleanup != nil {
			grpcCleanup()
		}
		if nc != nil {
			nc.Close()
		}
		if pgContainer != nil {
			pgContainer.Terminate(ctx)
		}
		if natsContainer != nil {
			natsContainer.Terminate(ctx)
		}
	})

	It("records model under modelID when modelID is provided", func() {
		result, err := router.Route(ctx, "my-model-id", "path/to/model.gguf", "llama-cpp",
			&pb.ModelOptions{ModelFile: "path/to/model.gguf"}, false)
		Expect(err).ToNot(HaveOccurred())
		defer result.Release()

		// The DB should have the model tracked under "my-model-id"
		nodesWithModel, err := registry.FindNodesWithModel("my-model-id")
		Expect(err).ToNot(HaveOccurred())
		Expect(nodesWithModel).To(HaveLen(1))
		Expect(nodesWithModel[0].ID).To(Equal(nodeID))
	})

	It("records model under modelName when modelID is empty (backward compat)", func() {
		result, err := router.Route(ctx, "", "legacy/model.bin", "llama-cpp",
			&pb.ModelOptions{ModelFile: "legacy/model.bin"}, false)
		Expect(err).ToNot(HaveOccurred())
		defer result.Release()

		// The DB should have the model tracked under the modelName
		nodesWithModel, err := registry.FindNodesWithModel("legacy/model.bin")
		Expect(err).ToNot(HaveOccurred())
		Expect(nodesWithModel).To(HaveLen(1))
	})

	It("FindNodesWithModel(modelID) finds node; FindNodesWithModel(modelName) does not", func() {
		result, err := router.Route(ctx, "distinct-id", "distinct/path.gguf", "llama-cpp",
			&pb.ModelOptions{ModelFile: "distinct/path.gguf"}, false)
		Expect(err).ToNot(HaveOccurred())
		defer result.Release()

		// Should find by modelID
		found, err := registry.FindNodesWithModel("distinct-id")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(HaveLen(1))

		// Should NOT find by modelName (different from modelID)
		notFound, err := registry.FindNodesWithModel("distinct/path.gguf")
		Expect(err).ToNot(HaveOccurred())
		Expect(notFound).To(BeEmpty())
	})

	It("InFlight tracking increments and decrements via registry", func() {
		// Route to establish model record
		result, err := router.Route(ctx, "release-model", "release/path.gguf", "llama-cpp",
			&pb.ModelOptions{ModelFile: "release/path.gguf"}, false)
		Expect(err).ToNot(HaveOccurred())
		defer result.Release()

		// Manually increment in-flight (simulates what InFlightTrackingClient.track() does during inference)
		Expect(registry.IncrementInFlight(nodeID, "release-model")).To(Succeed())

		// Check in-flight is > 0
		models, err := registry.GetNodeModels(nodeID)
		Expect(err).ToNot(HaveOccurred())
		var inflight int
		for _, m := range models {
			if m.ModelName == "release-model" {
				inflight = m.InFlight
			}
		}
		Expect(inflight).To(BeNumerically(">", 0))

		// Decrement and check in-flight goes back to 0
		Expect(registry.DecrementInFlight(nodeID, "release-model")).To(Succeed())

		models, err = registry.GetNodeModels(nodeID)
		Expect(err).ToNot(HaveOccurred())
		for _, m := range models {
			if m.ModelName == "release-model" {
				Expect(m.InFlight).To(Equal(0))
			}
		}
	})

	It("clears stale model record when node is unreachable", func() {
		// First route to establish the model record
		result, err := router.Route(ctx, "stale-check", "stale/path.gguf", "llama-cpp",
			&pb.ModelOptions{ModelFile: "stale/path.gguf"}, false)
		Expect(err).ToNot(HaveOccurred())
		result.Release()

		// Model should be in DB
		found, err := registry.FindNodesWithModel("stale-check")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(HaveLen(1))

		// Stop the gRPC server to make the node unreachable
		grpcCleanup()
		grpcCleanup = nil

		// Route again — should detect unreachable node and clear stale record
		// (it will fall through to FindLeastLoadedNode + backend.install which succeeds,
		// but the LoadModel gRPC call will fail since the server is down)
		_, err = router.Route(ctx, "stale-check", "stale/path.gguf", "llama-cpp",
			&pb.ModelOptions{ModelFile: "stale/path.gguf"}, false)
		// Expect an error since the only node is down (LoadModel fails)
		Expect(err).To(HaveOccurred())

		// The stale model record should have been cleared
		found, err = registry.FindNodesWithModel("stale-check")
		Expect(err).ToNot(HaveOccurred())
		Expect(found).To(BeEmpty())
	})
})
