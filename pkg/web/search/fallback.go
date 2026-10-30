package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultAttemptTimeout bounds one provider of a Fallback, so a provider that
// hangs leaves time for the next one.
const DefaultAttemptTimeout = 8 * time.Second

// Answerer is a Provider that can say which provider answered a search. A
// Searcher over an Answerer labels the results with the host that answered.
type Answerer interface {
	Provider
	SearchFrom(ctx context.Context, q Query) ([]Result, Provider, error)
}

// Fallback tries its providers in order and returns the first answer. A
// provider that fails (it is down, refuses its key, limits this server, does
// not answer in time) is skipped and the next one is tried. An answer with no
// results is an answer: the next provider is not asked.
type Fallback struct {
	providers []Provider
	attempt   time.Duration
}

// NewFallback returns a Fallback over providers, in that order. attempt bounds
// each provider; zero means DefaultAttemptTimeout.
func NewFallback(attempt time.Duration, providers ...Provider) *Fallback {
	if attempt <= 0 {
		attempt = DefaultAttemptTimeout
	}
	return &Fallback{providers: append([]Provider(nil), providers...), attempt: attempt}
}

// Providers lists the providers in order.
func (f *Fallback) Providers() []Provider { return append([]Provider(nil), f.providers...) }

// Name joins the names of the providers, so that a cache keyed by the name
// does not mix the answers of two orders.
func (f *Fallback) Name() string {
	names := make([]string, len(f.providers))
	for i, p := range f.providers {
		names[i] = p.Name()
	}
	return strings.Join(names, "+")
}

// Host is the host of the first provider. The host that answered a search is
// what SearchFrom returns.
func (f *Fallback) Host() string {
	if len(f.providers) == 0 {
		return ""
	}
	return f.providers[0].Host()
}

// Search implements Provider.
func (f *Fallback) Search(ctx context.Context, q Query) ([]Result, error) {
	r, _, err := f.SearchFrom(ctx, q)
	return r, err
}

// SearchFrom returns the first answer and the provider that gave it. When no
// provider answers, the error wraps ErrProvider and names each provider with
// its reason, in order.
func (f *Fallback) SearchFrom(ctx context.Context, q Query) ([]Result, Provider, error) {
	if len(f.providers) == 0 {
		return nil, nil, fmt.Errorf("%w: no provider is set", ErrProvider)
	}
	var reasons []string
	for _, p := range f.providers {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		actx, cancel := context.WithTimeout(ctx, f.attempt)
		res, err := p.Search(actx, q)
		cancel()
		if err == nil {
			return res, p, nil
		}
		if ctx.Err() != nil {
			// The caller stopped, or its own time ran out: no next provider.
			return nil, nil, ctx.Err()
		}
		reasons = append(reasons, p.Name()+": "+reasonOf(err))
	}
	return nil, nil, fmt.Errorf("%w: %s", ErrProvider, strings.Join(reasons, "; "))
}

// reasonOf is the sentence of a provider's error without the class prefix.
func reasonOf(err error) string {
	switch {
	case errors.Is(err, ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return "it did not answer in time"
	}
	s := err.Error()
	s = strings.TrimPrefix(s, ErrProvider.Error()+": ")
	if s == ErrProvider.Error() {
		return "it did not answer with results"
	}
	return s
}
