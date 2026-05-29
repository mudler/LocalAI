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
