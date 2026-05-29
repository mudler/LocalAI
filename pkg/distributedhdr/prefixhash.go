package distributedhdr

import "context"

type prefixChainKey struct{}

// WithPrefixChain attaches a prompt prefix-hash chain to ctx so the distributed
// router can make a prefix-cache-aware decision. Set at inference entry where
// the rendered prompt is known; read in SmartRouter.Route.
func WithPrefixChain(ctx context.Context, chain []uint64) context.Context {
	return context.WithValue(ctx, prefixChainKey{}, chain)
}

// PrefixChain returns the chain attached by WithPrefixChain, or nil.
func PrefixChain(ctx context.Context) []uint64 {
	if v, ok := ctx.Value(prefixChainKey{}).([]uint64); ok {
		return v
	}
	return nil
}

// PrefixChainHook, when set at startup (distributed mode only), builds a prefix
// hash chain from a model id and rendered prompt. Left nil in single-process
// mode so there is zero overhead. See core/application/distributed.go.
var PrefixChainHook func(model, prompt string) []uint64

// MaybeWithPrefixChain attaches a prefix chain to ctx iff the hook is set and
// returns a non-empty chain. Otherwise returns ctx unchanged.
func MaybeWithPrefixChain(ctx context.Context, model, prompt string) context.Context {
	if PrefixChainHook == nil {
		return ctx
	}
	if chain := PrefixChainHook(model, prompt); len(chain) > 0 {
		return WithPrefixChain(ctx, chain)
	}
	return ctx
}
