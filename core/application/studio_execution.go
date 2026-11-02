// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/executionrequest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	model "github.com/mudler/LocalAI/pkg/addons/model/executionstatus"
)

func (l lateStudioAddons) NewExecutionObservation(ctx context.Context, user, conversation string) (*executionrequest.Request, string, func() string) {
	if p, ok := l.a.StudioAddonTools().(studio.ExecutionObserver); ok {
		return p.NewExecutionObservation(ctx, user, conversation)
	}
	return nil, "", nil
}

func (s *studioAddons) NewExecutionObservation(ctx context.Context, user, conversation string) (*executionrequest.Request, string, func() string) {
	actor, err := s.actor(user)
	if err != nil {
		return nil, "", nil
	}
	b, err := s.adapter.BindingForActor(ctx, actor, gateway.ExecutionBindingClaim{ConversationID: conversation})
	if err != nil {
		return nil, "", nil
	}
	var id [24]byte
	if _, err = rand.Read(id[:]); err != nil {
		return nil, "", nil
	}
	q := model.ObservationID(hex.EncodeToString(id[:]))
	// Keep actor and conversation identity explicit. IO retains the original DTO.
	mb := model.Binding{Actor: actor.UserID, Credential: actor.CredentialID, Conversation: b.ConversationID, Mode: actor.Mode, Addon: b.AddonID, Generation: strconv.FormatUint(b.Generation, 10), Incarnation: b.Incarnation, ExecutionTool: b.ExecutionTool, StatusTool: b.StatusTool}
	var mu sync.Mutex
	explanation := ""
	io := executionrequest.IO{
		Validate: func(ctx context.Context, current model.Binding) error {
			if current != mb {
				return gateway.ErrForbidden
			}
			return s.adapter.ValidateExecutionBinding(ctx, actor, b)
		},
		Read: func(ctx context.Context, current model.Binding, observation model.ObservationID, deadline time.Time) (model.Payload, error) {
			if current != mb || observation != q {
				return model.InvalidPayload(), gateway.ErrForbidden
			}
			ctx, cancel := context.WithDeadline(ctx, deadline)
			defer cancel()
			reply, err := s.adapter.ExecutionStatusRead(ctx, actor, gateway.ExecutionStatusRequest{Version: 1, Binding: b, ObservationID: string(q)})
			if err != nil {
				return model.InvalidPayload(), err
			}
			if reply.Version != 1 || reply.Binding != b || reply.ObservationID != string(q) {
				return model.InvalidPayload(), gateway.ErrForbidden
			}
			raw, err := json.Marshal(reply.Status)
			if err != nil {
				return model.InvalidPayload(), err
			}
			status, err := contract.DecodeExecutionStatus(raw)
			if err != nil {
				return model.InvalidPayload(), err
			}
			mu.Lock()
			explanation = status.Explanation
			mu.Unlock()
			return model.ValidPayload(model.Value{Readiness: model.Readiness(status.Readiness), Isolation: model.Isolation(status.Isolation)}), nil
		},
	}
	request := executionrequest.New(ctx, mb, q, model.Eligible, io, nil)
	return request, b.AddonID, func() string { mu.Lock(); defer mu.Unlock(); return explanation }
}
