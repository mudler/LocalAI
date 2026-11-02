// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/executionrequest"
	"github.com/mudler/LocalAI/core/services/studio/session"
	model "github.com/mudler/LocalAI/pkg/addons/model/executionstatus"
)

// ExecutionObserver admits the owner-selected provider, not a discovery claim.
// The request and explanation reader belong exclusively to this outer scope.
type ExecutionObserver interface {
	NewExecutionObservation(context.Context, string, string) (*executionrequest.Request, string, func() string)
}

type executionObservationKey struct{}
type executionObservation struct {
	ctx                context.Context
	user, conversation string
	once               sync.Once
	request            *executionrequest.Request
	addon              string
	explanation        func() string
	provider           ExecutionObserver
}

// executionScope borrows existing outer state. Nested consumers must not close
// it: only the real turn, priming operation or standalone API request owns Close.
func (s *Service) executionScope(ctx context.Context, user, conversation string) (context.Context, func()) {
	if existing, ok := ctx.Value(executionObservationKey{}).(*executionObservation); ok && existing.user == user && existing.conversation == conversation {
		return ctx, func() {}
	}
	provider, _ := s.d.AddonTools.(ExecutionObserver)
	ownerCtx, cancel := context.WithCancel(ctx)
	scope := &executionObservation{ctx: ownerCtx, user: user, conversation: conversation, provider: provider}
	return context.WithValue(ctx, executionObservationKey{}, scope), func() {
		cancel()
		scope.once.Do(func() {})
		if scope.request != nil {
			scope.request.Close()
		}
	}
}
func (s *Service) productScope(ctx context.Context, x session.Access) (context.Context, func()) {
	a := x.Actor()
	return s.executionScope(ctx, a.Principal, a.ChatID)
}

// ExecutionObservation never falls back to an arbitrary installed provider.
// An out-of-scope, wrong-user or non-selected add-on has unknown status.
func ExecutionObservation(ctx context.Context, user, addon string) (model.Value, string) {
	scope, ok := ctx.Value(executionObservationKey{}).(*executionObservation)
	if !ok || user != scope.user {
		return model.Unknown(), ""
	}
	scope.once.Do(func() {
		if scope.provider != nil {
			scope.request, scope.addon, scope.explanation = scope.provider.NewExecutionObservation(scope.ctx, scope.user, scope.conversation)
		}
	})
	if scope.request == nil || addon != scope.addon {
		return model.Unknown(), ""
	}
	value := scope.request.Observe(ctx)
	explanation := ""
	if value.Readiness != model.UnknownReadiness && scope.explanation != nil {
		explanation = scope.explanation()
	}
	return value, explanation
}
