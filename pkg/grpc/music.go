package grpc

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	g "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MusicBackend is optional: legacy inference backends need no music stubs.
type MusicBackend interface {
	MusicTranscription(context.Context, *pb.MusicTranscriptionRequest, ...g.CallOption) (*pb.MusicTranscriptionResponse, error)
	MusicGeneration(context.Context, *pb.MusicGenerationRequest, ...g.CallOption) (*pb.MusicGenerationResponse, error)
}

// MusicModelContext requires the caller's context. There is no legacy fallback.
type MusicModelContext interface {
	MusicTranscription(context.Context, *pb.MusicTranscriptionRequest) (*pb.MusicTranscriptionResponse, error)
	MusicGeneration(context.Context, *pb.MusicGenerationRequest) (*pb.MusicGenerationResponse, error)
}

func musicText(s string, limit int, nonblank bool) bool {
	return len(s) <= limit && utf8.ValidString(s) && !strings.ContainsRune(s, 0) && (!nonblank || strings.TrimSpace(s) != "")
}
func musicPath(s string) bool {
	// Ownership, containment, and symlink-safe opens belong to the future I/O gate.
	return musicText(s, 4096, true) && filepath.IsAbs(s) && filepath.Clean(s) == s
}
func musicEnvelope(id, model, path string) error {
	if !musicText(id, 128, true) || !musicText(model, 128, true) || !musicPath(path) {
		return status.Error(codes.InvalidArgument, "invalid typed music request envelope")
	}
	return nil
}
func validateMusicGeneration(in *pb.MusicGenerationRequest) error {
	if in == nil {
		return status.Error(codes.InvalidArgument, "missing music generation request")
	}
	if err := musicEnvelope(in.RequestId, in.ModelIdentity, in.TrustedOutputDestination); err != nil {
		return err
	}
	p := in.Params
	if !musicText(in.Style, 4<<10, true) || !musicText(in.Lyrics, 16<<10, false) || p == nil || p.Cot < pb.MusicCOT_MUSIC_COT_OFF || p.Cot > pb.MusicCOT_MUSIC_COT_FULL || p.Seed > 2147483647 || math.IsNaN(p.GuidanceScale) || math.IsInf(p.GuidanceScale, 0) || p.GuidanceScale < 0 || p.GuidanceScale > 20 || p.NumInferenceSteps < 1 || p.NumInferenceSteps > 100 {
		return status.Error(codes.InvalidArgument, "invalid typed music generation fields or materialized parameters")
	}
	if in.Abc != nil {
		if !musicText(*in.Abc, 256<<10, true) || p.Cot == pb.MusicCOT_MUSIC_COT_OFF {
			return status.Error(codes.InvalidArgument, "invalid provided music ABC")
		}
		// Reject active directives here. A complete notation parser is an executor gate.
		for _, line := range strings.Split(*in.Abc, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "%%") || strings.HasPrefix(line, "I:") || strings.Contains(line, "[I:") {
				return status.Error(codes.InvalidArgument, "music ABC directives are unsupported")
			}
		}
	}
	return nil
}
func (s *server) musicIdentity(model string) error {
	s.identityMu.RLock()
	loaded := s.loadedIdentity
	s.identityMu.RUnlock()
	if loaded == "" {
		return status.Error(codes.FailedPrecondition, "music requires a nonempty loaded identity")
	}
	if loaded != model {
		return grpcerrors.ModelMismatch("grpc-server", loaded, model)
	}
	return nil
}
func (s *server) MusicTranscription(ctx context.Context, in *pb.MusicTranscriptionRequest) (*pb.MusicTranscriptionResponse, error) {
	if in == nil {
		return nil, status.Error(codes.InvalidArgument, "missing music transcription request")
	}
	if err := musicEnvelope(in.RequestId, in.ModelIdentity, in.TrustedSource); err != nil {
		return nil, err
	}
	if err := s.musicIdentity(in.ModelIdentity); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	model, ok := s.llm.(MusicModelContext)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "context-capable typed music model unavailable")
	}
	return model.MusicTranscription(ctx, in)
}
func (s *server) MusicGeneration(ctx context.Context, in *pb.MusicGenerationRequest) (*pb.MusicGenerationResponse, error) {
	if err := validateMusicGeneration(in); err != nil {
		return nil, err
	}
	if err := s.musicIdentity(in.ModelIdentity); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	model, ok := s.llm.(MusicModelContext)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "context-capable typed music model unavailable")
	}
	return model.MusicGeneration(ctx, in)
}
func (c *Client) MusicTranscription(ctx context.Context, in *pb.MusicTranscriptionRequest, opts ...g.CallOption) (*pb.MusicTranscriptionResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return pb.NewBackendClient(conn).MusicTranscription(ctx, in, opts...)
}
func (c *Client) MusicGeneration(ctx context.Context, in *pb.MusicGenerationRequest, opts ...g.CallOption) (*pb.MusicGenerationResponse, error) {
	if !c.parallel {
		c.opMutex.Lock()
		defer c.opMutex.Unlock()
	}
	c.setBusy(true)
	defer c.setBusy(false)
	defer c.wdMark()()
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return pb.NewBackendClient(conn).MusicGeneration(ctx, in, opts...)
}
func (e *embedBackend) MusicTranscription(ctx context.Context, in *pb.MusicTranscriptionRequest, _ ...g.CallOption) (*pb.MusicTranscriptionResponse, error) {
	return e.s.MusicTranscription(ctx, in)
}
func (e *embedBackend) MusicGeneration(ctx context.Context, in *pb.MusicGenerationRequest, _ ...g.CallOption) (*pb.MusicGenerationResponse, error) {
	return e.s.MusicGeneration(ctx, in)
}

var _ MusicBackend = (*Client)(nil)
var _ MusicBackend = (*embedBackend)(nil)
