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
	rerank       bool
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
	return openMultimodalSession(ctx, loader, cfg, app, false)
}

// OpenMultimodalRerankSession opens the same instance-bound session for the
// typed rerank RPC. It requires the rerank capability fields of the loaded
// instance instead of the embedding ones.
func OpenMultimodalRerankSession(ctx context.Context, loader *model.ModelLoader, cfg config.ModelConfig, app *config.ApplicationConfig) (*MultimodalEmbeddingSession, error) {
	return openMultimodalSession(ctx, loader, cfg, app, true)
}

func openMultimodalSession(ctx context.Context, loader *model.ModelLoader, cfg config.ModelConfig, app *config.ApplicationConfig, rerank bool) (*MultimodalEmbeddingSession, error) {
	client, err := loader.Load(ModelOptions(cfg, app, model.WithContext(ctx))...)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &MultimodalEmbeddingSession{rerank: rerank, client: client, modelOptions: grpcModelOpts(cfg, loader.ModelPath), options: gRPCPredictOpts(cfg, loader.ModelPath), ctx: ctx, cancel: cancel}
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
	version := m.GetEmbeddingProtocolVersion()
	if s.rerank {
		version = m.GetRerankProtocolVersion()
	}
	if err != nil || version != 1 || m.GetEmbeddingGeneration() == "" {
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

// admit runs the checks both typed RPCs share before an item is dispatched: the
// session is open, the exact instance is still the one that was bound, and its
// capability snapshot has not changed since the session was opened.
func (s *MultimodalEmbeddingSession) admit(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, nil, context.Canceled
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	done := func() { stop(); cancel() }
	if s.ctx.Err() != nil {
		done()
		return nil, nil, s.ctx.Err()
	}
	return ctx, done, nil
}

// recheck verifies the bound instance and its capabilities. It is called before
// dispatch and again before a result is published, so a result that raced a
// reload or a capability change is never returned.
func (s *MultimodalEmbeddingSession) recheck(ctx context.Context) (*pb.ModelMetadataResponse, error) {
	m, err := s.readMetadata(ctx)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(m, s.metadata) {
		return nil, multimodalError(501, "embedding capabilities changed; reopen session", nil)
	}
	return m, nil
}

func classifyDispatchError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if status.Code(err) == codes.Unimplemented {
		return multimodalError(501, "multimodal embedding protocol unavailable", err)
	}
	return err
}

func (s *MultimodalEmbeddingSession) Embed(ctx context.Context, request *pb.MultimodalEmbeddingRequest) (*pb.MultimodalEmbeddingResponse, error) {
	ctx, done, err := s.admit(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	if request == nil {
		return nil, multimodalError(400, "missing embedding request", nil)
	}
	// Recheck this exact connection before every item, including after idle time.
	m, err := s.recheck(ctx)
	if err != nil {
		return nil, err
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
	if ctx.Err() != nil || err != nil {
		return nil, classifyDispatchError(ctx, err)
	}
	// Do not publish a result that raced a capabilities change or backend restart.
	if _, err := s.recheck(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// Rerank sends one typed rerank request through the bound instance. It applies
// the embedding admission rules with the rerank capability fields, and never
// falls back to the text Rerank RPC.
func (s *MultimodalEmbeddingSession) Rerank(ctx context.Context, request *pb.MultimodalRerankRequest) (*pb.MultimodalRerankResponse, error) {
	if !s.rerank {
		return nil, multimodalError(501, "multimodal rerank protocol unavailable", nil)
	}
	ctx, done, err := s.admit(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	if request == nil {
		return nil, multimodalError(400, "missing rerank request", nil)
	}
	m, err := s.recheck(ctx)
	if err != nil {
		return nil, err
	}
	parts := append([]*pb.EmbeddingPart(nil), request.Query...)
	for _, d := range request.Documents {
		parts = append(parts, d.GetParts()...)
	}
	if err := CheckRerankModalities(m, parts); err != nil {
		return nil, err
	}
	in := proto.Clone(request).(*pb.MultimodalRerankRequest)
	in.Model = s.modelOptions.Model
	in.EmbeddingGeneration = s.metadata.EmbeddingGeneration
	if proto.Size(in) > MultimodalMaxRPCBytes {
		return nil, multimodalError(413, "multimodal rerank request exceeds the transport limit", nil)
	}
	release, err := AcquireGlobalBackendSlot()
	if err != nil {
		return nil, err
	}
	defer release()
	result, err := s.client.MultimodalRerank(ctx, in)
	if ctx.Err() != nil || err != nil {
		return nil, classifyDispatchError(ctx, err)
	}
	if _, err := s.recheck(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
