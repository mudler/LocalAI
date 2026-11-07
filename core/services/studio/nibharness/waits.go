package nibharness

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mudler/nib/chat"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// The two tools of nib that the host answers itself. nib offers them to the
// model under these names; the host's tools of the same names are not offered
// a second time but run when nib's tool is called.
const (
	waitForTool    = "wait_for"
	waitAgentsTool = "wait_agents"
)

// hostWaits holds the host's handlers of wait_for and wait_agents.
type hostWaits struct {
	waitFor, waitAgents *session.Tool
}

// take keeps t when it is one of the two tools and reports whether it did.
func (w *hostWaits) take(t session.Tool) bool {
	if t.Run == nil {
		return false
	}
	switch t.Name {
	case waitForTool:
		w.waitFor = &t
	case waitAgentsTool:
		w.waitAgents = &t
	default:
		return false
	}
	return true
}

// builtins are the names of nib's own tools to switch on.
func (w hostWaits) builtins() []string {
	var out []string
	if w.waitFor != nil {
		out = append(out, waitForTool)
	}
	if w.waitAgents != nil {
		out = append(out, waitAgentsTool)
	}
	return out
}

// wire sets the callbacks that carry the two tools to the host.
func (w hostWaits) wire(ctx context.Context, cb *chat.Callbacks) {
	if t := w.waitFor; t != nil {
		cb.HostWaits = true
		cb.OnWaitFor = func(req chat.WaitRequest) string { return runHost(ctx, *t, waitArgs(req)) }
	}
	if t := w.waitAgents; t != nil {
		cb.OnWaitAgents = func(ctx context.Context, req chat.WaitAgentsRequest) string {
			return runHost(ctx, *t, waitAgentsArgs(req))
		}
	}
}

func runHost(ctx context.Context, t session.Tool, args map[string]any) string {
	raw, err := json.Marshal(args)
	if err != nil {
		return err.Error()
	}
	out, err := t.Run(ctx, raw)
	if err != nil {
		return err.Error()
	}
	return out
}

// waitArgs turns a request of nib into the arguments of the host's wait_for.
func waitArgs(r chat.WaitRequest) map[string]any {
	m := map[string]any{"then": r.Then}
	set := func(k, v string) {
		if v != "" {
			m[k] = v
		}
	}
	set("tool", r.Tool)
	set("url", r.URL)
	set("until", r.Until)
	set("pattern", r.Pattern)
	set("field", r.Field)
	set("reason", r.Reason)
	if len(r.Arguments) > 0 {
		m["arguments"] = r.Arguments
	}
	if r.Equals != nil {
		m["equals"] = r.Equals
	}
	if r.IntervalSeconds > 0 {
		m["every"] = fmt.Sprintf("%ds", r.IntervalSeconds)
	}
	if r.Timeout > 0 {
		m["timeout"] = r.Timeout.Round(time.Second).String()
	}
	if r.MaxErrors > 0 {
		m["max_errors"] = r.MaxErrors
	}
	return m
}

// waitAgentsArgs turns a request of nib into the arguments of the host's
// wait_agents.
func waitAgentsArgs(r chat.WaitAgentsRequest) map[string]any {
	m := map[string]any{}
	if len(r.IDs) > 0 {
		m["ids"] = r.IDs
	}
	if r.Mode != "" {
		m["mode"] = r.Mode
	}
	if r.Timeout > 0 {
		m["timeout"] = r.Timeout.Round(time.Second).String()
	}
	return m
}
