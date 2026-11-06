// SPDX-License-Identifier: MIT
package backend

import (
	"context"

	"github.com/mudler/LocalAI/core/services/music"
)

// MusicResolver establishes authorization, loaded identity, capability, parser,
// quota and storage facts. It is not populated from user-supplied booleans.
type MusicResolver interface {
	ResolveMusic(context.Context, music.ValidatedRequest) (music.ValidatedRequest, error)
}

// RemoteMusicBackend deliberately has no unary RPC fallback. A disconnected
// unary call cannot prove native quiescence or release of the model lane.
// Enable a concrete adapter only when an explicit worker-release protocol exists.
type RemoteMusicBackend struct{}

func (RemoteMusicBackend) Admit(context.Context, music.StageRequest) (music.StageLease, error) {
	return nil, music.ErrSettlementUnsupported
}
func (RemoteMusicBackend) CheckGeneration(context.Context, music.ValidatedRequest) (music.Selection, error) {
	return music.Selection{}, music.ErrSettlementUnsupported
}

// RunMusic resolves trusted facts before entering the private runner. Requested
// selections/flags are hints only; the resolver must replace, not bless, them.
func RunMusic(ctx context.Context, resolver MusicResolver, runner *music.Runner, requested music.ValidatedRequest) (music.Result, error) {
	if resolver == nil || runner == nil {
		return music.Result{}, music.ErrPreflight
	}
	validated, err := resolver.ResolveMusic(ctx, requested)
	if err != nil {
		return music.Result{}, err
	}
	return runner.Run(ctx, validated)
}
