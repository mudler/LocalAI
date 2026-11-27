package backend_test

import (
	"context"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/priority"
	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	ggrpc "google.golang.org/grpc"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// priorityBackend records the PredictOptions of the Predict and Embeddings
// calls it receives.
type priorityBackend struct {
	grpcPkg.Backend

	predict    *pb.PredictOptions
	embeddings *pb.PredictOptions
}

func (r *priorityBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (r *priorityBackend) IsBusy() bool                              { return false }

func (r *priorityBackend) Predict(_ context.Context, in *pb.PredictOptions, _ ...ggrpc.CallOption) (*pb.Reply, error) {
	r.predict = in
	return &pb.Reply{Message: []byte("ok")}, nil
}

func (r *priorityBackend) Embeddings(_ context.Context, in *pb.PredictOptions, _ ...ggrpc.CallOption) (*pb.EmbeddingResult, error) {
	r.embeddings = in
	return &pb.EmbeddingResult{Embeddings: []float32{1}}, nil
}

var _ = Describe("request priority reaches the backend", func() {
	var (
		rec    *priorityBackend
		loader *model.ModelLoader
		appCfg *config.ApplicationConfig
		cl     *config.ModelConfigLoader
		cfg    config.ModelConfig
	)

	BeforeEach(func() {
		rec = &priorityBackend{}
		loader = model.NewModelLoader(&system.SystemState{})
		loader.SetModelRouter(func(_ context.Context, id string, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			return model.NewModelWithClient(id, "test://priority", rec), nil
		})
		appCfg = config.NewApplicationConfig(config.WithSystemState(&system.SystemState{}))
		cl = config.NewModelConfigLoader(GinkgoT().TempDir())
		cfg = identityModelCfg()
	})

	infer := func(ctx context.Context) *pb.PredictOptions {
		fn, err := backend.ModelInference(ctx, "hi", nil, nil, nil, nil, loader, &cfg, cl, appCfg, nil, "", "", nil, nil, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		_, err = fn()
		Expect(err).ToNot(HaveOccurred())
		Expect(rec.predict).ToNot(BeNil())
		return rec.predict
	}

	embed := func(ctx context.Context) *pb.PredictOptions {
		fn, err := backend.ModelEmbedding(ctx, "hi", nil, loader, cfg, appCfg)
		Expect(err).ToNot(HaveOccurred())
		_, err = fn()
		Expect(err).ToNot(HaveOccurred())
		Expect(rec.embeddings).ToNot(BeNil())
		return rec.embeddings
	}

	It("sends Default when the context carries no class", func() {
		Expect(infer(context.Background()).GetPriority()).To(BeEquivalentTo(0))
		Expect(embed(context.Background()).GetPriority()).To(BeEquivalentTo(0))
	})

	It("sends the class of the context for a chat request", func() {
		Expect(infer(priority.WithClass(context.Background(), priority.Interactive)).GetPriority()).To(BeEquivalentTo(1))
		Expect(infer(priority.WithClass(context.Background(), priority.Batch)).GetPriority()).To(BeEquivalentTo(-1))
	})

	It("sends the class of the context for an embeddings request", func() {
		Expect(embed(priority.WithClass(context.Background(), priority.Batch)).GetPriority()).To(BeEquivalentTo(-1))
	})

	It("ignores a priority key in the request metadata", func() {
		ctx := priority.WithClass(context.Background(), priority.Batch)
		fn, err := backend.ModelInference(ctx, "hi", nil, nil, nil, nil, loader, &cfg, cl, appCfg, nil, "", "", nil, nil, nil,
			map[string]string{"priority": "1", "Priority": "1"})
		Expect(err).ToNot(HaveOccurred())
		_, err = fn()
		Expect(err).ToNot(HaveOccurred())
		Expect(rec.predict.GetPriority()).To(BeEquivalentTo(-1))
	})
})
