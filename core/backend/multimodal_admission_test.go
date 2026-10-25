// SPDX-License-Identifier: MIT
package backend_test

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/nodes"
	grpcpkg "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type admissionWorker struct {
	base.SingleThread
	metadataErr error
	served      atomic.Int64
}

func (*admissionWorker) Load(*pb.ModelOptions) error { return nil }
func (w *admissionWorker) ModelMetadata(*pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
	return sessionMetadata(pb.EmbeddingModality_TEXT), w.metadataErr
}
func (w *admissionWorker) MultimodalEmbedding(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
	w.served.Add(1)
	return &pb.MultimodalEmbeddingResponse{}, nil
}

var _ = Describe("Multimodal routing", func() {
	It("distributed admission reconciles production metadata identity refusal and freshly selects an eligible replica", func() {
		ctx := context.Background()
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "admission.db")), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		sqlDB, err := db.DB()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(sqlDB.Close)
		registry, err := nodes.NewNodeRegistry(db)
		Expect(err).NotTo(HaveOccurred())
		cfg := identityModelCfg()
		cfg.Model = "selected.gguf"
		app := config.NewApplicationConfig(config.WithSystemState(&system.SystemState{}))
		workers := map[string]*admissionWorker{}
		add := func(name, loaded string, metadataErr error) *nodes.BackendNode {
			w := &admissionWorker{metadataErr: metadataErr}
			srv := grpcpkg.NewBackendServer(w)
			_, err := srv.LoadModel(ctx, &pb.ModelOptions{Model: loaded})
			Expect(err).NotTo(HaveOccurred())
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).NotTo(HaveOccurred())
			transport := ggrpc.NewServer()
			pb.RegisterBackendServer(transport, srv)
			go func() { _ = transport.Serve(listener) }()
			DeferCleanup(transport.Stop)
			node := &nodes.BackendNode{Name: name, NodeType: nodes.NodeTypeBackend, Address: listener.Addr().String()}
			Expect(registry.Register(ctx, node, true)).To(Succeed())
			Expect(registry.SetNodeModel(ctx, node.ID, cfg.ModelID(), 0, "loaded", node.Address, 0)).To(Succeed())
			workers[node.ID] = w
			return node
		}
		stale := add("recycled", "other.gguf", nil)
		loader := model.NewModelLoader(&system.SystemState{})
		var selections []string
		releases := 0
		loader.SetModelRouter(func(ctx context.Context, _, id, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			node, replica, err := registry.FindAndLockNodeWithModel(ctx, id, nil, nil)
			if err != nil {
				return nil, err
			}
			selections = append(selections, node.ID)
			raw := grpcpkg.NewClient(replica.Address, true, nil, false)
			tracked := nodes.NewInFlightTrackingClient(raw, registry, node.ID, id, replica.ReplicaIndex)
			tracked.OnFirstComplete(func() {
				releases++
				Expect(registry.DecrementInFlight(context.Background(), node.ID, id, replica.ReplicaIndex)).To(Succeed())
			})
			return model.NewModelWithClient(id, replica.Address, tracked), nil
		})
		session, err := backend.OpenMultimodalEmbeddingSession(ctx, loader, cfg, app)
		Expect(session).To(BeNil())
		var refusal *backend.MultimodalEmbeddingError
		Expect(errors.As(err, &refusal)).To(BeTrue())
		Expect(refusal.Code).To(Equal(501))
		Expect(grpcerrors.IsModelMismatch(errors.Unwrap(refusal))).To(BeTrue())
		Expect(workers[stale.ID].served.Load()).To(BeZero())
		eligible, err := registry.FindNodesWithModel(ctx, cfg.ModelID())
		Expect(err).NotTo(HaveOccurred())
		Expect(eligible).To(BeEmpty(), "metadata admission must remove the stale route before another selection")
		Expect(releases).To(Equal(1), "early admission refusal must release the route reservation")
		var remaining int64
		Expect(db.Model(&nodes.NodeModel{}).Where("node_id = ?", stale.ID).Count(&remaining).Error).To(Succeed())
		Expect(remaining).To(BeZero())

		healthy := add("fresh", cfg.Model, nil)
		session, err = backend.OpenMultimodalEmbeddingSession(ctx, loader, cfg, app)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(session.Close)
		_, err = session.Embed(ctx, sessionRequest())
		Expect(err).NotTo(HaveOccurred())
		Expect(selections).To(Equal([]string{stale.ID, healthy.ID}))
		Expect(workers[healthy.ID].served.Load()).To(Equal(int64(1)))
		Expect(workers[stale.ID].served.Load()).To(BeZero())
		replica, err := registry.GetNodeModel(ctx, healthy.ID, cfg.ModelID(), 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(replica.InFlight).To(BeZero())
		Expect(releases).To(Equal(2))
	})

	It("distributed admission preserves healthy routes on transient metadata refusal and balances early accounting", func() {
		for _, refusal := range []error{context.Canceled, status.Error(codes.Unavailable, "temporary"), status.Error(codes.Unimplemented, "old worker")} {
			tracker := &admissionTracker{}
			b := &admissionBackend{sessionBackend: sessionBackend{metadata: func(context.Context, *pb.ModelOptions) (*pb.ModelMetadataResponse, error) { return nil, refusal }}}
			tracked := nodes.NewInFlightTrackingClient(b, tracker, "node", "selected", 0)
			released := 0
			tracked.OnFirstComplete(func() { released++ })
			cfg := identityModelCfg()
			app := config.NewApplicationConfig(config.WithSystemState(&system.SystemState{}))
			s, err := backend.OpenMultimodalEmbeddingSession(context.Background(), sessionLoader(tracked), cfg, app)
			Expect(s).To(BeNil())
			Expect(errors.Is(err, refusal)).To(BeTrue())
			Expect(tracker.removed).To(BeZero())
			Expect(tracker.increments).To(Equal(1))
			Expect(tracker.decrements).To(Equal(1))
			Expect(released).To(Equal(1))
		}
	})
})

type admissionTracker struct{ increments, decrements, removed int }

func (t *admissionTracker) IncrementInFlight(context.Context, string, string, int) error {
	t.increments++
	return nil
}
func (t *admissionTracker) DecrementInFlight(context.Context, string, string, int) error {
	t.decrements++
	return nil
}
func (t *admissionTracker) RemoveNodeModel(context.Context, string, string, int) error {
	t.removed++
	return nil
}

// The loader may evict a connection on Unavailable; no worker resources are owned by this stub.
type admissionBackend struct{ sessionBackend }

func (*admissionBackend) Free(context.Context) error { return nil }
