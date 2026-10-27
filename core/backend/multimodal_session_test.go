// SPDX-License-Identifier: MIT
package backend_test

import (
	"context"
	"fmt"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	grpcpkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sync"
	"sync/atomic"
	"time"
)

type sessionBackend struct {
	grpcpkg.Backend
	metadata func(context.Context, *pb.ModelOptions) (*pb.ModelMetadataResponse, error)
	embed    func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error)
	rerank   func(context.Context, *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error)
}

func (b *sessionBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (b *sessionBackend) IsBusy() bool                              { return false }
func (b *sessionBackend) ModelMetadata(ctx context.Context, in *pb.ModelOptions, _ ...ggrpc.CallOption) (*pb.ModelMetadataResponse, error) {
	return b.metadata(ctx, in)
}
func (b *sessionBackend) MultimodalEmbedding(ctx context.Context, in *pb.MultimodalEmbeddingRequest, _ ...ggrpc.CallOption) (*pb.MultimodalEmbeddingResponse, error) {
	return b.embed(ctx, in)
}
func (b *sessionBackend) MultimodalRerank(ctx context.Context, in *pb.MultimodalRerankRequest, _ ...ggrpc.CallOption) (*pb.MultimodalRerankResponse, error) {
	return b.rerank(ctx, in)
}
func rerankMetadata(mod ...pb.EmbeddingModality) *pb.ModelMetadataResponse {
	return &pb.ModelMetadataResponse{EmbeddingGeneration: "test-instance", RerankProtocolVersion: 1, RerankModalities: mod}
}
func rerankRequest() *pb.MultimodalRerankRequest {
	text := func(s string) []*pb.EmbeddingPart {
		return []*pb.EmbeddingPart{{Part: &pb.EmbeddingPart_Text{Text: s}}}
	}
	return &pb.MultimodalRerankRequest{Model: "untrusted", Query: text("q"), Documents: []*pb.RerankDocument{{Parts: text("a")}}}
}
func sessionMetadata(mod ...pb.EmbeddingModality) *pb.ModelMetadataResponse {
	return &pb.ModelMetadataResponse{EmbeddingGeneration: "test-instance", EmbeddingProtocolVersion: 1, EmbeddingModalities: mod}
}
func sessionRequest() *pb.MultimodalEmbeddingRequest {
	return &pb.MultimodalEmbeddingRequest{Model: "untrusted", Parts: []*pb.EmbeddingPart{{Part: &pb.EmbeddingPart_Text{Text: "hello"}}}}
}
func sessionLoader(b grpcpkg.Backend) *model.ModelLoader {
	l := model.NewModelLoader(&system.SystemState{})
	l.SetModelRouter(func(_ context.Context, _, id, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
		return model.NewModelWithClient(id, "test://session", b), nil
	})
	return l
}

var _ = Describe("Multimodal routing", func() {
	var b *sessionBackend
	var app *config.ApplicationConfig
	var cfg config.ModelConfig
	BeforeEach(func() {
		cfg = identityModelCfg()
		cfg.Model = "weights.gguf"
		app = config.NewApplicationConfig(config.WithSystemState(&system.SystemState{}))
		b = &sessionBackend{metadata: func(context.Context, *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
			return sessionMetadata(pb.EmbeddingModality_TEXT), nil
		}, embed: func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
			return &pb.MultimodalEmbeddingResponse{}, nil
		}}
	})
	open := func(l *model.ModelLoader) *backend.MultimodalEmbeddingSession {
		s, err := backend.OpenMultimodalEmbeddingSession(context.Background(), l, cfg, app)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(s.Close)
		return s
	}
	It("model-identity binds options and does not mutate caller request", func() {
		b.metadata = func(_ context.Context, in *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
			Expect(in.Model).To(Equal(cfg.Model))
			return sessionMetadata(pb.EmbeddingModality_TEXT), nil
		}
		b.embed = func(_ context.Context, in *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
			Expect(in.Model).To(Equal(cfg.Model))
			Expect(in.Options.ModelIdentity).To(Equal(cfg.Model))
			return &pb.MultimodalEmbeddingResponse{}, nil
		}
		s := open(sessionLoader(b))
		req := sessionRequest()
		_, err := s.Embed(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Model).To(Equal("untrusted"))
		Expect(req.Options).To(BeNil())
	})
	It("distributed same-name instances never share positive capabilities", func() {
		l := sessionLoader(b)
		s := open(l)
		other := &sessionBackend{metadata: func(context.Context, *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
			return sessionMetadata(pb.EmbeddingModality_IMAGE), nil
		}}
		l.SetModelRouter(func(_ context.Context, _, id, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			return model.NewModelWithClient(id, "test://other", other), nil
		})
		second := open(l)
		_, err := second.Embed(context.Background(), sessionRequest())
		var typed *backend.MultimodalEmbeddingError
		Expect(err).To(BeAssignableToTypeOf(typed))
		Expect(err.(*backend.MultimodalEmbeddingError).StatusCode()).To(Equal(422))
		_, err = s.Embed(context.Background(), sessionRequest())
		Expect(err).NotTo(HaveOccurred())
	})
	It("local unload reload invalidates an instance even with identical capabilities", func() {
		l := model.NewModelLoader(&system.SystemState{})
		store := model.NewInMemoryModelStore()
		l.SetModelStore(store)
		store.Set(cfg.ModelID(), model.NewModelWithClient(cfg.ModelID(), "test://local", b))
		s := open(l)
		store.Delete(cfg.ModelID())
		store.Set(cfg.ModelID(), model.NewModelWithClient(cfg.ModelID(), "test://local", b))
		res, err := s.Embed(context.Background(), sessionRequest())
		Expect(err).To(HaveOccurred())
		Expect(res).To(BeNil())
		fresh := open(l)
		_, err = fresh.Embed(context.Background(), sessionRequest())
		Expect(err).NotTo(HaveOccurred())
	})
	It("reload rechecks metadata and rejects stale positive capabilities", func() {
		s := open(sessionLoader(b))
		b.metadata = func(context.Context, *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
			return sessionMetadata(pb.EmbeddingModality_IMAGE), nil
		}
		_, err := s.Embed(context.Background(), sessionRequest())
		Expect(err).To(HaveOccurred())
	})
	It("reload stale response race never publishes the old result", func() {
		var changed atomic.Bool
		b.metadata = func(context.Context, *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
			if changed.Load() {
				return &pb.ModelMetadataResponse{}, nil
			}
			return sessionMetadata(pb.EmbeddingModality_TEXT), nil
		}
		b.embed = func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
			changed.Store(true)
			return &pb.MultimodalEmbeddingResponse{}, nil
		}
		s := open(sessionLoader(b))
		res, err := s.Embed(context.Background(), sessionRequest())
		Expect(err).To(HaveOccurred())
		Expect(res).To(BeNil())
	})
	It("fails closed on old missing unknown and unavailable metadata", func() {
		for _, version := range []uint32{0, 2} {
			b.metadata = func(context.Context, *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
				return &pb.ModelMetadataResponse{EmbeddingProtocolVersion: version}, nil
			}
			s, err := backend.OpenMultimodalEmbeddingSession(context.Background(), sessionLoader(b), cfg, app)
			Expect(s).To(BeNil())
			Expect(err.(*backend.MultimodalEmbeddingError).Code).To(Equal(501))
		}
		b.metadata = func(context.Context, *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
			return nil, status.Error(codes.Unimplemented, "old")
		}
		_, err := backend.OpenMultimodalEmbeddingSession(context.Background(), sessionLoader(b), cfg, app)
		Expect(err.(*backend.MultimodalEmbeddingError).Code).To(Equal(501))
	})
	It("context cancellation and Close cancel concurrent inference without legacy fallback", func() {
		started := make(chan struct{}, 2)
		b.embed = func(ctx context.Context, _ *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		}
		s := open(sessionLoader(b))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var wg sync.WaitGroup
		done := make(chan struct{})
		DeferCleanup(func() {
			cancel()
			Expect(s.Close()).To(Succeed())
			Eventually(done, 5*time.Second).Should(BeClosed())
		})
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, err := s.Embed(ctx, sessionRequest()); errs <- err }()
		}
		go func() { wg.Wait(); close(done) }()
		Eventually(started, time.Second).Should(Receive())
		Eventually(started, time.Second).Should(Receive())
		Expect(s.Close()).To(Succeed())
		Eventually(done, time.Second).Should(BeClosed())
		Expect(<-errs).To(MatchError(context.Canceled))
		Expect(<-errs).To(MatchError(context.Canceled))
		Expect(s.Metadata()).To(BeNil())
	})
	It("distributed same-capability generation change requires reopen before call and after stale response", func() {
		var generation atomic.Int64
		generation.Store(1)
		b.metadata = func(context.Context, *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
			m := sessionMetadata(pb.EmbeddingModality_TEXT)
			m.EmbeddingGeneration = fmt.Sprint(generation.Load())
			return m, nil
		}
		b.embed = func(_ context.Context, r *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
			Expect(r.EmbeddingGeneration).To(Equal(fmt.Sprint(generation.Load())))
			return &pb.MultimodalEmbeddingResponse{}, nil
		}
		l := sessionLoader(b)
		s := open(l)
		generation.Add(1)
		_, err := s.Embed(context.Background(), sessionRequest())
		Expect(err).To(HaveOccurred())
		fresh := open(l)
		_, err = fresh.Embed(context.Background(), sessionRequest())
		Expect(err).NotTo(HaveOccurred())
		b.embed = func(context.Context, *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
			generation.Add(1)
			return &pb.MultimodalEmbeddingResponse{}, nil
		}
		result, err := fresh.Embed(context.Background(), sessionRequest())
		Expect(err).To(HaveOccurred())
		Expect(result).To(BeNil())
	})
	It("metadata snapshots cannot be mutated by the caller", func() {
		s := open(sessionLoader(b))
		s.Metadata().EmbeddingProtocolVersion = 0
		Expect(s.Metadata().EmbeddingProtocolVersion).To(Equal(uint32(1)))
	})

	Describe("typed rerank", func() {
		openRerank := func(l *model.ModelLoader) *backend.MultimodalEmbeddingSession {
			s, err := backend.OpenMultimodalRerankSession(context.Background(), l, cfg, app)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(s.Close)
			return s
		}
		BeforeEach(func() {
			b.metadata = func(context.Context, *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
				return rerankMetadata(pb.EmbeddingModality_TEXT, pb.EmbeddingModality_IMAGE), nil
			}
			b.rerank = func(context.Context, *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
				return &pb.MultimodalRerankResponse{}, nil
			}
		})
		It("binds the selected identity and generation and does not mutate the caller request", func() {
			b.rerank = func(_ context.Context, in *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
				Expect(in.Model).To(Equal(cfg.Model))
				Expect(in.EmbeddingGeneration).To(Equal("test-instance"))
				return &pb.MultimodalRerankResponse{}, nil
			}
			s := openRerank(sessionLoader(b))
			req := rerankRequest()
			_, err := s.Rerank(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())
			Expect(req.Model).To(Equal("untrusted"))
			Expect(req.EmbeddingGeneration).To(BeEmpty())
		})
		It("requires the rerank capability, not the embedding one", func() {
			b.metadata = func(context.Context, *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
				return sessionMetadata(pb.EmbeddingModality_TEXT, pb.EmbeddingModality_IMAGE), nil
			}
			_, err := backend.OpenMultimodalRerankSession(context.Background(), sessionLoader(b), cfg, app)
			Expect(err).To(HaveOccurred())
			Expect(err.(*backend.MultimodalEmbeddingError).StatusCode()).To(Equal(501))
		})
		It("refuses an image the reranker does not publish before dispatch", func() {
			b.metadata = func(context.Context, *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
				return rerankMetadata(pb.EmbeddingModality_TEXT), nil
			}
			var called atomic.Bool
			b.rerank = func(context.Context, *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
				called.Store(true)
				return &pb.MultimodalRerankResponse{}, nil
			}
			s := openRerank(sessionLoader(b))
			req := rerankRequest()
			req.Documents[0].Parts = []*pb.EmbeddingPart{{Part: &pb.EmbeddingPart_Media{Media: &pb.EmbeddingMedia{Kind: pb.EmbeddingMedia_IMAGE, Data: []byte("x")}}}}
			_, err := s.Rerank(context.Background(), req)
			Expect(err.(*backend.MultimodalEmbeddingError).StatusCode()).To(Equal(422))
			Expect(called.Load()).To(BeFalse())
		})
		It("maps an old worker to 501 and never retries as text", func() {
			b.rerank = func(context.Context, *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
				return nil, status.Error(codes.Unimplemented, "old")
			}
			s := openRerank(sessionLoader(b))
			_, err := s.Rerank(context.Background(), rerankRequest())
			Expect(err.(*backend.MultimodalEmbeddingError).StatusCode()).To(Equal(501))
		})
		It("never publishes a result that raced a capability change", func() {
			var changed atomic.Bool
			b.metadata = func(context.Context, *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
				if changed.Load() {
					return &pb.ModelMetadataResponse{}, nil
				}
				return rerankMetadata(pb.EmbeddingModality_TEXT), nil
			}
			b.rerank = func(context.Context, *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
				changed.Store(true)
				return &pb.MultimodalRerankResponse{}, nil
			}
			s := openRerank(sessionLoader(b))
			res, err := s.Rerank(context.Background(), rerankRequest())
			Expect(err).To(HaveOccurred())
			Expect(res).To(BeNil())
		})
		It("is not available on an embedding session", func() {
			b.metadata = func(context.Context, *pb.ModelOptions) (*pb.ModelMetadataResponse, error) {
				return sessionMetadata(pb.EmbeddingModality_TEXT), nil
			}
			s := open(sessionLoader(b))
			_, err := s.Rerank(context.Background(), rerankRequest())
			Expect(err.(*backend.MultimodalEmbeddingError).StatusCode()).To(Equal(501))
		})
	})
})
