package studio

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// An ordinary chat runs at most maxBackgroundChildren helpers in the
// background and its helpers start none (depth 1). The tree of helpers of a
// coordinator has the limits its creator set, within the operator ceilings.

// ordinaryDepth is how deep the helpers of an ordinary chat nest.
const ordinaryDepth = 1

// maxChatDepth bounds the walk up the parents of a chat, so a loop in the
// store cannot hold a turn.
const maxChatDepth = 16

type helperLimits struct {
	// Background is how many helpers may run in the background at once.
	Background int
	// Depth is how many levels of helpers there may be under the chat.
	Depth int
	// Tree is true when the limit counts every running helper under the root
	// chat (a coordinator), and false when it counts the helpers of one
	// conversation (an ordinary chat).
	Tree bool
}

// chatDepth returns how deep rec is among helpers (0 for a chat that is not a
// helper) and the first ancestor that is not a helper. The switches, the
// rights and the limits of a tree of helpers are the root's.
func (s *Service) chatDepth(ctx context.Context, rec session.ChatRecord) (depth int, root session.ChatRecord, err error) {
	root = rec
	for root.Kind == session.KindSubAgent {
		if depth >= maxChatDepth || root.ParentID == "" {
			return 0, session.ChatRecord{}, fmt.Errorf("studio: the helpers of %s are nested too deep or have no parent", rec.ID)
		}
		parent, err := s.d.Store.GetChat(ctx, rec.Principal, root.ParentID)
		if err != nil {
			return 0, session.ChatRecord{}, err
		}
		root = parent
		depth++
	}
	return depth, root, nil
}

// helperLimitsOf returns the limits for the helpers started under rec. The
// limits are read at each call: lowering one blocks new helpers and never
// stops a helper that runs. A coordinator that cannot be read, or that was
// deleted, gives the limits of an ordinary chat.
func (s *Service) helperLimitsOf(ctx context.Context, root session.ChatRecord) helperLimits {
	ordinary := helperLimits{Background: maxBackgroundChildren, Depth: ordinaryDepth}
	h := s.coordinators
	if h == nil || !isCoordinatorChat(root) {
		return ordinary
	}
	cfg, ok, err := h.configOf(ctx, root)
	if err != nil || !ok {
		return ordinary
	}
	cfg = cfg.WithDefaults()
	lim := h.Limits()
	return helperLimits{Background: min(cfg.MaxHelpers, lim.HelpersCeiling), Depth: min(cfg.MaxDepth, lim.DepthCeiling), Tree: true}
}

// mayDelegate says whether a helper chat may start helpers of its own.
func (s *Service) mayDelegate(ctx context.Context, rec session.ChatRecord) bool {
	depth, root, err := s.chatDepth(ctx, rec)
	if err != nil {
		return false
	}
	return depth+1 <= s.helperLimitsOf(ctx, root).Depth
}

// treeChats returns every helper under the chat p/root, at any depth.
func (s *Service) treeChats(ctx context.Context, p string, root session.ChatRecord) ([]session.ChatRecord, error) {
	var out []session.ChatRecord
	queue := []string{root.ID}
	for len(queue) > 0 && len(out) < 10000 {
		id := queue[0]
		queue = queue[1:]
		kids, err := s.d.Store.ListChildren(ctx, p, id)
		if err != nil {
			return nil, err
		}
		for _, k := range kids {
			out = append(out, k)
			queue = append(queue, k.ID)
		}
	}
	return out, nil
}

// treeState returns the ids of the helpers under root whose turn runs now, on
// any frontend, or that wait for their loop, and the ids of every chat of the
// tree (root included). It reads the store, so a helper that another frontend
// started counts.
func (s *Service) treeState(ctx context.Context, p string, root session.ChatRecord) (running, chats map[string]bool, err error) {
	all, err := s.treeChats(ctx, p, root)
	if err != nil {
		return nil, nil, err
	}
	running, chats = map[string]bool{}, map[string]bool{root.ID: true}
	for _, k := range all {
		chats[k.ID] = true
		info, err := s.d.Store.Turn(ctx, p, k.ID)
		if err != nil {
			if errors.Is(err, session.ErrNotFound) {
				continue // deleted meanwhile
			}
			return nil, nil, err
		}
		if info.Running || s.loopHolds(ctx, p, k.ID) {
			running[k.ID] = true
		}
	}
	return running, chats, nil
}

// auditHelperDenied records that a coordinator's helper was refused: the model
// asked for one past a limit.
func (s *Service) auditHelperDenied(ctx context.Context, root session.ChatRecord, reason string) {
	h := s.coordinators
	if h == nil || !isCoordinatorChat(root) {
		return
	}
	cfg, ok, err := h.configOf(ctx, root)
	if err != nil || !ok {
		return
	}
	h.record(ctx, coordinator.AuditEntry{At: h.now(), CoordinatorID: cfg.AgentID, Principal: cfg.Principal,
		Kind: coordinator.KindHelper, Outcome: coordinator.OutcomeDenied, Reason: reason, Actor: "coordinator"})
}
