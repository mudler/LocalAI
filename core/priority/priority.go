// Package priority carries the priority class of a request to the backend.
//
// A backend that queues requests (llama.cpp) serves a higher class first when
// requests wait for a slot. The classes match the priority field of the
// llama.cpp server:
//
//	-1  Batch        work nobody waits on: sub-agents, jobs, indexing
//	 0  Default      a plain API request
//	 1  Interactive  a person waits for the answer
//
// The class lives in the request context (WithClass, FromContext) and
// reaches the backend as PredictOptions.Priority. It is set from trusted
// state only: the role of an API caller (Decide) or LocalAI's own callers.
// It is never read from request metadata, which the caller controls.
package priority

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Class is the priority class of a request. Higher is served first.
type Class int32

const (
	// Batch is work nobody waits on.
	Batch Class = -1
	// Default is a request with no class of its own.
	Default Class = 0
	// Interactive is a request a person waits on.
	Interactive Class = 1
)

// The OpenAI service_tier values the API accepts.
const (
	TierAuto     = "auto"
	TierDefault  = "default"
	TierFlex     = "flex"
	TierScale    = "scale"
	TierPriority = "priority"
)

// ServiceTier returns the OpenAI service_tier name of the class, the value an
// API response echoes.
func (c Class) ServiceTier() string {
	switch {
	case c < Default:
		return TierFlex
	case c > Default:
		return TierPriority
	default:
		return TierDefault
	}
}

// ErrInvalidServiceTier means a service_tier value names no tier.
var ErrInvalidServiceTier = errors.New("invalid service_tier")

// ParseServiceTier maps an OpenAI service_tier to a class. Empty, auto,
// default and scale are Default; flex is Batch; priority is Interactive.
func ParseServiceTier(s string) (Class, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", TierAuto, TierDefault, TierScale:
		return Default, nil
	case TierFlex:
		return Batch, nil
	case TierPriority:
		return Interactive, nil
	}
	return Default, fmt.Errorf("%w %q: use %s, %s, %s or %s", ErrInvalidServiceTier, s, TierAuto, TierDefault, TierFlex, TierPriority)
}

// Decide returns the class of an API request from its service_tier. Any
// caller may lower its class. Only a caller that may raise (an administrator)
// gets a class above Default; for any other caller it is clamped to Default,
// so a user cannot push other users' requests back.
func Decide(serviceTier string, mayRaise bool) (Class, error) {
	c, err := ParseServiceTier(serviceTier)
	if err != nil {
		return Default, err
	}
	if c > Default && !mayRaise {
		return Default, nil
	}
	return c, nil
}

type ctxKey struct{}

// WithClass returns ctx carrying c. A later call replaces the class: the
// caller that starts a piece of work knows best what it is for.
func WithClass(ctx context.Context, c Class) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// FromContext returns the class ctx carries, and whether it carries one.
func FromContext(ctx context.Context) (Class, bool) {
	if ctx == nil {
		return Default, false
	}
	c, ok := ctx.Value(ctxKey{}).(Class)
	return c, ok
}

// Of returns the class ctx carries, Default when none.
func Of(ctx context.Context) Class {
	c, _ := FromContext(ctx)
	return c
}
