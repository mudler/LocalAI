// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/executionrequest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	model "github.com/mudler/LocalAI/pkg/addons/model/executionstatus"
	"github.com/mudler/xlog"
)

func (l lateStudioAddons) NewExecutionObservation(ctx context.Context, user, conversation string) (*executionrequest.Request, string, func() studio.ExecutionDetail) {
	if p, ok := l.a.StudioAddonTools().(studio.ExecutionObserver); ok {
		return p.NewExecutionObservation(ctx, user, conversation)
	}
	return nil, "", nil
}

func (s *studioAddons) NewExecutionObservation(ctx context.Context, user, conversation string) (*executionrequest.Request, string, func() studio.ExecutionDetail) {
	var mu sync.Mutex
	var detail studio.ExecutionDetail
	replied := false
	var addonID string
	// fail records why no status could be read. The first failure wins: later
	// ones are consequences of it.
	fail := func(stage string, err error) {
		reason := executionFailureReason(stage, err)
		mu.Lock()
		first := detail.Failure == ""
		if first {
			detail.Failure = reason
		}
		mu.Unlock()
		if first && !errors.Is(err, context.Canceled) {
			logExecutionFailure(stage, reason, err, addonID, conversation)
		}
	}
	current := func() studio.ExecutionDetail {
		mu.Lock()
		defer mu.Unlock()
		d := detail
		if d.Failure == "" && !replied {
			d.Failure = studio.UnknownNoAnswer
		}
		return d
	}
	actor, err := s.actor(user)
	if err != nil {
		fail("binding", err)
		return nil, "", current
	}
	b, err := s.adapter.BindingForActor(ctx, actor, gateway.ExecutionBindingClaim{ConversationID: conversation})
	if err != nil {
		fail("binding", err)
		return nil, "", current
	}
	var id [24]byte
	if _, err = rand.Read(id[:]); err != nil {
		fail("binding", err)
		return nil, "", current
	}
	addonID = b.AddonID
	q := model.ObservationID(hex.EncodeToString(id[:]))
	// Keep actor and conversation identity explicit. IO retains the original DTO.
	mb := model.Binding{Actor: actor.UserID, Credential: actor.CredentialID, Conversation: b.ConversationID, Mode: actor.Mode, Addon: b.AddonID, Generation: strconv.FormatUint(b.Generation, 10), Incarnation: b.Incarnation, ExecutionTool: b.ExecutionTool, StatusTool: b.StatusTool}
	io := executionrequest.IO{
		Validate: func(ctx context.Context, current model.Binding) error {
			if current != mb {
				fail("validate", gateway.ErrForbidden)
				return gateway.ErrForbidden
			}
			if err := s.adapter.ValidateExecutionBinding(ctx, actor, b); err != nil {
				fail("validate", err)
				return err
			}
			return nil
		},
		Read: func(ctx context.Context, current model.Binding, observation model.ObservationID, deadline time.Time) (model.Payload, error) {
			if current != mb || observation != q {
				fail("read", gateway.ErrForbidden)
				return model.InvalidPayload(), gateway.ErrForbidden
			}
			ctx, cancel := context.WithDeadline(ctx, deadline)
			defer cancel()
			reply, err := s.adapter.ExecutionStatusRead(ctx, actor, gateway.ExecutionStatusRequest{Version: 1, Binding: b, ObservationID: string(q)})
			if err != nil {
				fail("read", err)
				return model.InvalidPayload(), err
			}
			if reply.Version != 1 || reply.Binding != b || reply.ObservationID != string(q) {
				fail("read", gateway.ErrForbidden)
				return model.InvalidPayload(), gateway.ErrForbidden
			}
			raw, err := json.Marshal(reply.Status)
			if err != nil {
				fail("decode", err)
				return model.InvalidPayload(), err
			}
			status, err := contract.DecodeExecutionStatus(raw)
			if err != nil {
				fail("decode", err)
				return model.InvalidPayload(), err
			}
			mu.Lock()
			detail.Explanation, detail.NetworkPolicyEnforced = status.Explanation, status.NetworkPolicyEnforced
			replied = true
			mu.Unlock()
			return model.ValidPayload(model.Value{Readiness: model.Readiness(status.Readiness), Isolation: model.Isolation(status.Isolation)}), nil
		},
	}
	request := executionrequest.New(ctx, mb, q, model.Eligible, io, nil)
	return request, b.AddonID, current
}

// executionFailureReason maps a failure to the reason the person is told. The
// gateway answers forbidden for every refusal, so the stage tells them apart:
// while binding, the chat has no add-on that reports a status; later, what was
// bound no longer matches.
func executionFailureReason(stage string, err error) studio.UnknownReason {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return studio.UnknownNoAnswer
	case errors.Is(err, gateway.ErrUnavailable):
		return studio.UnknownOwnerUnreachable
	case errors.Is(err, gateway.ErrForbidden):
		if stage == "binding" {
			return studio.UnknownNotReported
		}
		return studio.UnknownStale
	case stage == "decode":
		return studio.UnknownInvalid
	}
	return studio.UnknownOwnerUnreachable
}

// failureLogEvery bounds the log line per reason: a chat page asks for the
// status often and a broken add-on would fill the log.
const failureLogEvery = time.Minute

var failureLog = struct {
	sync.Mutex
	last map[studio.UnknownReason]time.Time
}{last: map[studio.UnknownReason]time.Time{}}

// failureLogDue says whether a failure of this reason may be logged at now,
// and counts it if so.
func failureLogDue(reason studio.UnknownReason, now time.Time) bool {
	failureLog.Lock()
	defer failureLog.Unlock()
	if last, ok := failureLog.last[reason]; ok && now.Sub(last) < failureLogEvery {
		return false
	}
	failureLog.last[reason] = now
	return true
}

// logExecutionFailure logs the class of a failure, never its text, which may
// carry add-on or user data.
func logExecutionFailure(stage string, reason studio.UnknownReason, err error, addon, conversation string) {
	if !failureLogDue(reason, time.Now()) {
		return
	}
	class := "other"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		class = "deadline"
	case errors.Is(err, gateway.ErrUnavailable):
		class = "unavailable"
	case errors.Is(err, gateway.ErrForbidden):
		class = "forbidden"
	}
	xlog.Warn("Chats: the execution status could not be read", "stage", stage, "reason", string(reason), "class", class,
		"addon", addon, "chat", conversation[:min(len(conversation), 8)])
}
