// SPDX-License-Identifier: MIT
package backend

import (
	"context"
	"errors"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	grpcpkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// MultimodalMaxRPCBytes is the existing gRPC transport ceiling (50 MiB).
const MultimodalMaxRPCBytes = 50 << 20

// MultimodalEmbeddingError exposes HTTP classification without depending on
// acquisition or HTTP packages. The cause remains available to routing callers.
type MultimodalEmbeddingError struct {
	Code    int
	cause   error
	message string
}

func (e *MultimodalEmbeddingError) Error() string   { return e.message }
func (e *MultimodalEmbeddingError) Unwrap() error   { return e.cause }
func (e *MultimodalEmbeddingError) StatusCode() int { return e.Code }
func multimodalError(code int, message string, cause error) error {
	return &MultimodalEmbeddingError{code, cause, message}
}

// MultimodalEmbeddingSession owns no connection: the loader owns its lifetime.
// Only capabilities and cancellation are request scoped. No model-name cache
// is consulted, and this session never reschedules onto another replica.
type MultimodalEmbeddingSession struct {
	valid        func() bool
	mu           sync.Mutex
	client       grpcpkg.Backend
	metadata     *pb.ModelMetadataResponse
	modelOptions *pb.ModelOptions
	options      *pb.PredictOptions
	ctx          context.Context
	cancel       context.CancelFunc
	closed       bool
}

func OpenMultimodalEmbeddingSession(ctx context.Context, loader *model.ModelLoader, cfg config.ModelConfig, app *config.ApplicationConfig) (*MultimodalEmbeddingSession, error) {
	client, err := loader.Load(ModelOptions(cfg, app, model.WithContext(ctx))...)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &MultimodalEmbeddingSession{client: client, modelOptions: grpcModelOpts(cfg, loader.ModelPath), options: gRPCPredictOpts(cfg, loader.ModelPath), ctx: ctx, cancel: cancel}
	s.modelOptions.Model = cfg.Model
	s.valid = loader.BindLoadedInstance(cfg.ModelID(), client)
	s.metadata, err = s.readMetadata(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	return s, nil
}

func (s *MultimodalEmbeddingSession) readMetadata(ctx context.Context) (*pb.ModelMetadataResponse, error) {
	if !s.valid() {
		return nil, multimodalError(501, "embedding instance unloaded; reopen session", nil)
	}
	m, err := s.client.ModelMetadata(ctx, proto.Clone(s.modelOptions).(*pb.ModelOptions))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !s.valid() {
		return nil, multimodalError(501, "embedding instance unloaded; reopen session", nil)
	}
	if err != nil || m.GetEmbeddingProtocolVersion() != 1 || m.GetEmbeddingGeneration() == "" {
		return nil, multimodalError(501, "multimodal embedding protocol unavailable", err)
	}
	return proto.Clone(m).(*pb.ModelMetadataResponse), nil
}
func (s *MultimodalEmbeddingSession) Metadata() *pb.ModelMetadataResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	return proto.Clone(s.metadata).(*pb.ModelMetadataResponse)
}
func (s *MultimodalEmbeddingSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.cancel()
	return nil
}
func (s *MultimodalEmbeddingSession) Embed(ctx context.Context, request *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, context.Canceled
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	if s.ctx.Err() != nil {
		return nil, s.ctx.Err()
	}
	if request == nil {
		return nil, multimodalError(400, "missing embedding request", nil)
	}
	// Recheck this exact connection before every item, including after idle time.
	m, err := s.readMetadata(ctx)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(m, s.metadata) {
		return nil, multimodalError(501, "embedding capabilities changed; reopen session", nil)
	}
	if err := CheckEmbeddingModalities(m, request.Parts); err != nil {
		return nil, err
	}
	in := proto.Clone(request).(*pb.MultimodalEmbeddingRequest)
	in.Model = s.modelOptions.Model
	in.EmbeddingGeneration = s.metadata.EmbeddingGeneration
	in.Options = proto.Clone(s.options).(*pb.PredictOptions)
	// The ceiling covers the request as the transport will carry it, options included.
	if proto.Size(in) > MultimodalMaxRPCBytes {
		return nil, multimodalError(413, "multimodal embedding request exceeds the transport limit", nil)
	}
	release, err := AcquireGlobalBackendSlot()
	if err != nil {
		return nil, err
	}
	defer release()
	result, err := s.client.MultimodalEmbedding(ctx, in)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if status.Code(err) == codes.Unimplemented {
			return nil, multimodalError(501, "multimodal embedding protocol unavailable", err)
		}
		return nil, err
	}
	// Do not publish a result that raced a capabilities change or backend restart.
	after, err := s.readMetadata(ctx)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(m, after) {
		return nil, multimodalError(501, "embedding capabilities changed; reopen session", nil)
	}
	return result, nil
}
