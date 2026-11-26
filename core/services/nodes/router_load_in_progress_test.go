package nodes

import (
	"context"
	"runtime"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// A request that finds every node it may use already loading the model, but
// no load it can wait on, is told to retry: 503 with Retry-After, not 500.
var _ = Describe("Route when every candidate node already loads the model", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		registry *NodeRegistry
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
	})

	It("answers with a retryable error", func() {
		register := func(name, zone string) *BackendNode {
			node := &BackendNode{Name: name, NodeType: NodeTypeBackend, Address: name + ":50051", MaxReplicasPerModel: 1}
			Expect(registry.Register(ctx, node, true)).To(Succeed())
			fetched, err := registry.GetByName(ctx, name)
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.SetNodeLabel(ctx, fetched.ID, "zone", zone)).To(Succeed())
			return fetched
		}
		register("zone-a", "a")
		nodeB := register("zone-b", "b")
		// Another frontend loads the model on node B.
		_, won, err := registry.ClaimLoadJobOn(ctx, "model-x", nodeB.ID, "another-frontend")
		Expect(err).ToNot(HaveOccurred())
		Expect(won).To(BeTrue())

		// The rule moves the model from zone a to zone b while the request
		// routes: it waits on no load in zone a, and the only node in zone b
		// already loads the model.
		rules := &switchingRuleRegistry{NodeRegistry: registry, rules: []*ModelSchedulingConfig{
			{ModelName: "model-x", NodeSelector: `{"zone":"a"}`},
			{ModelName: "model-x", NodeSelector: `{"zone":"b"}`},
		}}
		unloader := &fakeUnloader{installReply: &workerctl.BackendInstallReply{Success: true, Address: "127.0.0.1:9001"}}
		backend := &stubBackend{healthResult: true, loadResult: &pb.Result{Success: true}}
		router := NewSmartRouter(rules, SmartRouterOptions{Unloader: unloader, ClientFactory: &stubClientFactory{client: backend}, DB: db})

		_, err = router.Route(ctx, "model-x", "models/x.gguf", "llama-cpp", "", &pb.ModelOptions{Model: "models/x.gguf"}, false)

		Expect(err).To(MatchError(ErrLoadInProgress))
		Expect(err).To(MatchError(ErrNoAvailableNodes), "the HTTP layer answers 503 with Retry-After for it")
	})
})

// switchingRuleRegistry answers each read of the governing rule with the next
// rule of the list, and the last one after that.
type switchingRuleRegistry struct {
	*NodeRegistry
	rules []*ModelSchedulingConfig
	reads atomic.Int32
}

func (s *switchingRuleRegistry) GetGoverningScheduling(context.Context, string) (*ModelSchedulingConfig, error) {
	i := int(s.reads.Add(1)) - 1
	return s.rules[min(i, len(s.rules)-1)], nil
}
