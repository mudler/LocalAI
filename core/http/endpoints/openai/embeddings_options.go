// SPDX-License-Identifier: MIT
package openai

import (
	"context"

	"github.com/mudler/LocalAI/pkg/embeddingmedia"
)

// MediaAcquirer fetches and validates the compressed bytes of one media source.
type MediaAcquirer interface {
	Acquire(ctx context.Context, source embeddingmedia.Source, budget *embeddingmedia.Budget) ([]byte, error)
}

type embeddingsOptions struct {
	acquirer MediaAcquirer
	limiter  *embeddingmedia.Limiter
}

// EmbeddingsOption customizes EmbeddingsEndpoint; production uses none.
type EmbeddingsOption func(*embeddingsOptions)

func WithMediaAcquirer(a MediaAcquirer) EmbeddingsOption {
	return func(o *embeddingsOptions) { o.acquirer = a }
}
func WithPreparationLimiter(l *embeddingmedia.Limiter) EmbeddingsOption {
	return func(o *embeddingsOptions) { o.limiter = l }
}
func newEmbeddingsOptions(opts []EmbeddingsOption) embeddingsOptions {
	o := embeddingsOptions{}
	for _, f := range opts {
		f(&o)
	}
	if o.acquirer == nil {
		o.acquirer = embeddingmedia.NewAcquirer()
	}
	if o.limiter == nil {
		o.limiter = embeddingmedia.NewPreparationLimiter()
	}
	return o
}
