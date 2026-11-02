// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/music"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	g "google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// MusicSettlementBackend is deliberately not implemented by the unary Client.
// MusicSettled must survive RPC disconnect and return nil only after actual
// worker quiescence and lane release. A nonnil error is not permission to clean.
type MusicSettlementBackend interface {
	MusicSettled(MusicStageCorrelation) <-chan error
}

type MusicStageCorrelation struct {
	RequestID string
	Stage     music.Stage
	EffectID  string
}

// MusicInputReleaser is mandatory: unary release errors do not acknowledge
// deletion, and returning from a failed attempt cannot discard input ownership.
type MusicInputReleaser interface {
	ReleaseMusicInputs(context.Context, string, string, []string) music.CleanupTask
}

// OwnedDirectoryStager allocates a request-owned directory and a NONEXISTENT
// new leaf for native O_EXCL|O_NOFOLLOW. AllocRemoteTemp is incompatible.
// The cleanup closure owns the exact directory key; callers never infer keys.
// A partial allocation returning an error must return every created cleanup
// capability, including DiscardCandidate if a private frontend leaf was created.
type OwnedDirectoryStager interface {
	AllocateMusicOutput(context.Context, string, string) (MusicOutputDestination, error)
}
type MusicOutputDestination struct {
	RequestID                              string
	RemoteLeaf, FetchKey, PrivateCandidate string
	Cleanup                                func(context.Context) music.CleanupTask
	// DiscardCandidate removes only the request-owned private frontend leaf.
	// Unlike Cleanup (remote directory), it must not run on successful fetch.
	DiscardCandidate func(context.Context) music.CleanupTask
}

func (f *FileStagingClient) musicInner() (grpc.MusicBackend, MusicSettlementBackend, error) {
	inner, ok := f.Backend.(grpc.MusicBackend)
	if !ok {
		return nil, nil, fmt.Errorf("typed music backend unavailable")
	}
	ack, ok := f.Backend.(MusicSettlementBackend)
	if !ok {
		return nil, nil, music.ErrSettlementUnsupported
	}
	return inner, ack, nil
}

func (f *FileStagingClient) MusicTranscription(ctx context.Context, input *pb.MusicTranscriptionRequest, opts ...g.CallOption) (*pb.MusicTranscriptionResponse, error) {
	inner, ack, err := f.musicInner()
	if err != nil {
		return nil, err
	}
	if input == nil || validateEphemeralRequestID(input.RequestId) != nil || input.ModelIdentity == "" || !filepath.IsAbs(input.TrustedSource) {
		return nil, fmt.Errorf("invalid music transcription")
	}
	settled := ack.MusicSettled(MusicStageCorrelation{input.RequestId, music.TranscriptionStage, "run"})
	if settled == nil {
		return nil, music.ErrSettlementUnsupported
	}
	info, err := os.Lstat(input.TrustedSource)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("music source must be a trusted regular file")
	}
	if _, ok := f.stager.(MusicInputReleaser); !ok {
		return nil, music.ErrSettlementUnsupported
	}
	in := proto.Clone(input).(*pb.MusicTranscriptionRequest)
	// Use the actual request ID, not a derived path or anonymous lifecycle ID.
	lifecycle := &stagedInputLifecycle{client: f, requestID: in.RequestId, seen: map[string]struct{}{}}
	in.TrustedSource, err = f.stageInputFile(ctx, lifecycle, in.TrustedSource, "inputs")
	if err != nil {
		return nil, errors.Join(err, f.releaseMusicInputs(context.WithoutCancel(ctx), lifecycle))
	}
	out, rpcErr := inner.MusicTranscription(ctx, in, opts...)
	awaitMusicSettlement(settled)
	err = f.releaseMusicInputs(context.WithoutCancel(ctx), lifecycle)
	if rpcErr != nil || err != nil || ctx.Err() != nil {
		return nil, errors.Join(rpcErr, err, ctx.Err())
	}
	if out == nil || out.Abc == "" {
		return nil, fmt.Errorf("missing inline music ABC")
	}
	return proto.Clone(out).(*pb.MusicTranscriptionResponse), nil
}
func (f *FileStagingClient) releaseMusicInputs(ctx context.Context, l *stagedInputLifecycle) error {
	if len(l.keys) == 0 {
		return nil
	}
	if err := validateEphemeralRequestRelease(l.requestID, l.keys); err != nil {
		return err
	}
	releaser := f.stager.(MusicInputReleaser)
	return releaser.ReleaseMusicInputs(ctx, f.nodeID, l.requestID, append([]string(nil), l.keys...)).Wait()
}

func (f *FileStagingClient) MusicGeneration(ctx context.Context, input *pb.MusicGenerationRequest, opts ...g.CallOption) (*pb.MusicGenerationResponse, error) {
	inner, ack, err := f.musicInner()
	if err != nil {
		return nil, err
	}
	allocator, ok := f.stager.(OwnedDirectoryStager)
	if !ok {
		return nil, fmt.Errorf("music requires owned directory/new-leaf staging")
	}
	if input == nil || validateEphemeralRequestID(input.RequestId) != nil || input.ModelIdentity == "" || input.Params == nil {
		return nil, fmt.Errorf("invalid music generation")
	}
	settled := ack.MusicSettled(MusicStageCorrelation{input.RequestId, music.GenerationStage, "run"})
	if settled == nil {
		return nil, music.ErrSettlementUnsupported
	}
	in := proto.Clone(input).(*pb.MusicGenerationRequest)
	dest, err := allocator.AllocateMusicOutput(ctx, f.nodeID, in.RequestId)
	// Allocation returns a capability even on a partial allocation failure.
	// Only a matching request capability can be cleaned here.
	if err != nil {
		if dest.RequestID == in.RequestId && dest.Cleanup != nil {
			err = errors.Join(err, dest.Cleanup(context.WithoutCancel(ctx)).Wait())
			if dest.DiscardCandidate != nil {
				err = errors.Join(err, dest.DiscardCandidate(context.WithoutCancel(ctx)).Wait())
			}
		}
		return nil, err
	}
	cleanup := func() error {
		if dest.Cleanup == nil {
			return fmt.Errorf("missing music cleanup authority")
		}
		return dest.Cleanup(context.WithoutCancel(ctx)).Wait()
	}
	if dest.RequestID != in.RequestId || dest.Cleanup == nil {
		return nil, fmt.Errorf("foreign music output ownership")
	}
	if dest.DiscardCandidate == nil || !filepath.IsAbs(dest.RemoteLeaf) || !filepath.IsAbs(dest.PrivateCandidate) || validateEphemeralReleaseKey(dest.FetchKey) != nil {
		return nil, errors.Join(fmt.Errorf("invalid music output destination"), cleanup())
	}
	// Require the exact request-scoped key; never trim remote filenames into keys.
	if err = validateEphemeralRequestRelease(in.RequestId, []string{dest.FetchKey}); err != nil {
		return nil, errors.Join(err, cleanup())
	}
	in.TrustedOutputDestination = dest.RemoteLeaf
	out, rpcErr := inner.MusicGeneration(ctx, in, opts...)
	awaitMusicSettlement(settled)
	if rpcErr != nil || ctx.Err() != nil {
		return nil, errors.Join(rpcErr, ctx.Err(), cleanup())
	}
	if out == nil || !out.Success {
		return nil, errors.Join(fmt.Errorf("music generation failed"), cleanup())
	}
	// Exactly one network fetch. Runner Storage.FetchValidate only validates the
	// private frontend candidate associated with this request in the stager ledger.
	err = f.stager.FetchRemoteByKey(ctx, f.nodeID, dest.FetchKey, dest.PrivateCandidate)
	err = errors.Join(err, cleanup())
	if err != nil {
		return nil, errors.Join(err, dest.DiscardCandidate(context.WithoutCancel(ctx)).Wait())
	}
	return proto.Clone(out).(*pb.MusicGenerationResponse), nil
}

var _ grpc.MusicBackend = (*FileStagingClient)(nil)

// Errors and channel closure are not quiescence evidence. A broken collaborator
// retains the call stack and its cleanup capabilities rather than orphaning work.
func awaitMusicSettlement(ack <-chan error) {
	for err := range ack {
		if err == nil {
			return
		}
	}
	select {}
}
