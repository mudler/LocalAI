package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

const waitAgentsName = "wait_agents"

// Bounds of the timeout of wait_agents. The tool never blocks for longer.
const (
	waitAgentsDefault = 30 * time.Second
	waitAgentsMax     = 10 * time.Minute
)

// waitAgentsMin is the shortest timeout. A test shortens it.
var waitAgentsMin = newWaitKnob(10 * time.Second)

// waitAgentsPoll is how often wait_agents reads the statuses of the
// sub-agents from the store. A test shortens it.
var waitAgentsPoll = newWaitKnob(2 * time.Second)

const waitAgentsDescription = "Wait for background sub-agents (spawn_agent with wait=false) and see how they stand. " +
	"ids are the sub-agents to wait for; omit them to wait for all that run now. " +
	"mode all returns when every one is done; any_failed_or_all returns as soon as one failed or was stopped, or when all are done. " +
	"timeout (default 30s, from 10s to 10m) bounds the wait: when it passes, the tool returns the current statuses and the sub-agents keep running. " +
	"The result of a finished sub-agent also arrives as a message."

const waitAgentsSchema = `{"type":"object","properties":{` +
	`"ids":{"type":"array","items":{"type":"string"},"description":"Ids of the sub-agents. Omit for all that run now."},` +
	`"mode":{"type":"string","enum":["all","any_failed_or_all"],"description":"all (default) or any_failed_or_all."},` +
	`"timeout":{"type":"string","description":"How long to wait at most, such as 30s or 5m. Default 30s, from 10s to 10m."}}}`

// waitAgentsTool is wait_agents for the chat of a.
func (s *Service) waitAgentsTool(a workspace.Actor) session.Tool {
	return session.Tool{Name: waitAgentsName, Description: waitAgentsDescription, Schema: json.RawMessage(waitAgentsSchema),
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) { return s.waitAgents(ctx, a, raw) }}
}

// agentState is the word for the state of a sub-agent.
func agentState(r SubAgent) string {
	switch {
	case r.Turn.Running:
		return "running"
	case r.Turn.Last == "":
		return "not started"
	case r.Turn.Last == store.TurnPaused:
		return "done (stopped at the step limit)"
	}
	return string(r.Turn.Last)
}

func agentFailed(r SubAgent) bool {
	return !r.Turn.Running && (r.Turn.Last == store.TurnFailed || r.Turn.Last == store.TurnStopped || r.Turn.Last == store.TurnInterrupted)
}

func (s *Service) waitAgents(ctx context.Context, a workspace.Actor, raw json.RawMessage) (string, error) {
	var args struct {
		IDs     []string `json:"ids"`
		Mode    string   `json:"mode"`
		Timeout string   `json:"timeout"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	anyFailed := false
	switch args.Mode {
	case "", "all":
	case "any_failed_or_all":
		anyFailed = true
	default:
		return "", fmt.Errorf("mode must be all or any_failed_or_all, not %q", args.Mode)
	}
	timeout := waitAgentsDefault
	if t := strings.TrimSpace(args.Timeout); t != "" {
		d, err := time.ParseDuration(t)
		if err != nil || d <= 0 {
			return "", fmt.Errorf("timeout must be a duration such as 30s or 5m, not %q", args.Timeout)
		}
		timeout = d
	}
	timeout = min(max(timeout, waitAgentsMin.get()), waitAgentsMax)

	all, err := s.SubAgents(ctx, a.Principal, a.ChatID)
	if err != nil {
		return "", err
	}
	byID := map[string]bool{}
	for _, r := range all {
		byID[r.Chat.ID] = true
	}
	ids := args.IDs
	if len(ids) == 0 {
		for _, r := range all {
			if r.Turn.Running {
				ids = append(ids, r.Chat.ID)
			}
		}
		if len(ids) == 0 {
			return "No sub-agent runs now.\n" + agentLines(all, nil), nil
		}
	}
	var unknown []string
	for _, id := range ids {
		if !byID[id] {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		return "", fmt.Errorf("unknown sub-agent id: %s. Your sub-agents:\n%s", strings.Join(unknown, ", "), agentLines(all, nil))
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(waitAgentsPoll.get())
	defer tick.Stop()
	for {
		cur, err := s.SubAgents(ctx, a.Principal, a.ChatID)
		if err != nil {
			return "", err
		}
		running, failed := 0, 0
		for _, r := range cur {
			if !slices.Contains(ids, r.Chat.ID) {
				continue
			}
			if r.Turn.Running {
				running++
			} else if agentFailed(r) {
				failed++
			}
		}
		switch {
		case running == 0:
			return "All the sub-agents you waited for are done.\n" + agentLines(cur, ids), nil
		case anyFailed && failed > 0:
			return "A sub-agent failed or was stopped; the others may still run.\n" + agentLines(cur, ids), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return fmt.Sprintf("Timed out after %s: %d still run, and they keep running. Their results arrive as messages.\n%s",
				timeout, running, agentLines(cur, ids)), nil
		case <-tick.C:
		}
	}
}

// agentLines lists the sub-agents (those in ids, or all when ids is nil), one
// per line, with their state.
func agentLines(list []SubAgent, ids []string) string {
	var b strings.Builder
	for _, r := range list {
		if ids != nil && !slices.Contains(ids, r.Chat.ID) {
			continue
		}
		fmt.Fprintf(&b, "- %s %q: %s\n", r.Chat.ID, truncate(r.Chat.Title, 60), agentState(r))
	}
	if b.Len() == 0 {
		return "(none)"
	}
	return strings.TrimRight(b.String(), "\n")
}
