// SPDX-License-Identifier: MIT
package openai

import (
	"github.com/mudler/LocalAI/core/http/endpoints/multimodal"
	"github.com/mudler/LocalAI/pkg/embeddingmedia"
)

// MediaAcquirer fetches and validates the compressed bytes of one media source.
type MediaAcquirer = multimodal.MediaAcquirer

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
