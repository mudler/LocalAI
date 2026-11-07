package studio

import (
	"context"
	"encoding/json"
	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// The action policy of an unattended run (a trigger or an agent job). The
// rules are in builtins.ActPolicy. This file finds the rights of a run, wraps
// the acting tools of the chat with the check, and writes the audit line.
//
// A tool that the policy refuses stays in the tool list. Its call returns a
// plain sentence instead of running, so the model can tell the person what it
// wanted to do. The refusal is a result, not an error: an error would make
// the model retry.

// unattended says whether a chat started by itself: no person watches it and
// nobody can approve a step.
func unattended(origin string) bool {
	return origin == session.OriginTrigger || origin == session.OriginJob
}

// actPolicy gives the rights of an unattended run, or false for a chat that a
// person watches. It fails closed: a trigger or binding that cannot be read
// gives a read only policy.
//
//   - Trigger run: the act.* names of the trigger (Acts), within the grants of
//     its agent. A trigger without an agent has no ceiling but its own list. A
//     trigger whose plan names a file it writes (Writes) holds act.files, as
//     the owner saw it on the proposal card.
//   - Read only: the trigger declared that its runs only read, or it was made
//     without asking by a binding in the auto_readonly mode.
//   - Job run: jobs have no list of their own, so the run holds the act.*
//     grants of its agent, and nothing without one.
func (s *Service) actPolicy(ctx context.Context, rec session.ChatRecord) (builtins.ActPolicy, bool) {
	if !unattended(rec.Origin) {
		return builtins.ActPolicy{}, false
	}
	ceiling := actNames(s.chatGrants(ctx, rec))
	switch rec.Origin {
	case session.OriginJob:
		return builtins.ActPolicy{Held: ceiling}, true
	}
	if s.triggers == nil {
		return builtins.ActPolicy{ReadOnly: true}, true
	}
	t, err := s.triggers.o.Store.GetTrigger(ctx, rec.OriginID)
	if err != nil || t.Owner != rec.Principal {
		return builtins.ActPolicy{ReadOnly: true}, true
	}
	return s.triggerPolicy(ctx, t, ceiling, rec.AgentID != ""), true
}

// triggerPolicy is the rights of the runs of trigger t: its acts within the
// ceiling (the grants of its agent), when it has an agent. The proposal card,
// the REST view and the run itself all read it, so they cannot disagree.
func (s *Service) triggerPolicy(ctx context.Context, t triggers.Trigger, ceiling []string, hasAgent bool) builtins.ActPolicy {
	want := slices.Clone(t.Acts)
	if t.Writes != "" && !slices.Contains(want, builtins.ClassFiles) {
		want = append(want, builtins.ClassFiles)
	}
	pol := builtins.ActPolicy{ReadOnly: t.Route.ReadOnly}
	var ap connectors.Approval
	if t.Route.Auto {
		ap = s.bindingApproval(ctx, t.Route.Binding)
		if ap.ModeOrDefault() == connectors.ApprovalAutoReadonly {
			pol.ReadOnly = true
		}
	}
	for _, n := range want {
		if hasAgent && !withinCeiling(n, ceiling) {
			continue
		}
		// A trigger made without asking stays inside what the owner allows
		// for its binding now; one the person confirmed keeps what they saw.
		if t.Route.Auto && !ap.AllowsAct(n) {
			continue
		}
		pol.Held = append(pol.Held, n)
	}
	return pol
}

// bindingApproval is the approval of a binding. A binding that cannot be read
// is treated as auto_readonly with no acts: the host does not widen a run on a
// doubt.
func (s *Service) bindingApproval(ctx context.Context, id string) connectors.Approval {
	doubt := connectors.Approval{Mode: connectors.ApprovalAutoReadonly}
	if id == "" || s.connectors == nil {
		return doubt
	}
	b, err := s.connectors.o.Store.GetBinding(ctx, id)
	if err != nil {
		return doubt
	}
	return b.Approval
}

// actNames keeps the act.* names of a list of grants.
func actNames(grants []string) []string {
	var out []string
	for _, g := range grants {
		if slices.Contains(builtins.ActGrants(), g) {
			out = append(out, g)
		}
	}
	return out
}

// withinCeiling says whether the agent's grants cover a name of a trigger: the
// name itself, or act.addons for act.addons:<id>.
func withinCeiling(name string, ceiling []string) bool {
	if slices.Contains(ceiling, name) {
		return true
	}
	return strings.HasPrefix(name, builtins.ClassAddons+":") && slices.Contains(ceiling, builtins.ClassAddons)
}

// gateAct wraps a tool with the policy. The policy is checked at each call, so
// a prompt cannot change the answer.
func (s *Service) gateAct(rec session.ChatRecord, pol builtins.ActPolicy, t session.Tool, req builtins.ActTool) session.Tool {
	if req.Name == "" {
		req.Name = t.Name
	}
	run := t.Run
	t.Run = func(ctx context.Context, args json.RawMessage) (string, error) {
		v := pol.Decide(req)
		if !req.Read {
			s.log.Info("studio: unattended action", "chat", rec.ID, "origin", rec.Origin, "origin_id", rec.OriginID,
				"tool", req.Name, "allowed", v.Allowed, "reason", v.Reason, "missing", v.Missing)
		}
		if !v.Allowed {
			if v.Reason == "not_granted" {
				s.refusedActs.LoadOrStore(rec.ID, v.Missing)
			}
			addontools.OutcomeFrom(ctx).Deny("act_" + v.Reason)
			return v.Sentence, nil
		}
		return run(ctx, args)
	}
	return t
}

// gateAddonAct applies the policy to the tool of an add-on. The kind that the
// add-on declared decides read or action, and a permission means the tool
// acts outside LocalAI with a credential.
func (s *Service) gateAddonAct(rec session.ChatRecord, pol builtins.ActPolicy, t session.Tool, d AddonTool) session.Tool {
	return s.gateAct(rec, pol, t, builtins.ActTool{Name: t.Name, Read: !d.Action, Class: builtins.ClassAddons,
		AddonID: d.AddonID, External: d.Permission != ""})
}

// gateExternalAct applies the policy to tools that come from outside, such as
// MCP servers: they are paid or external, so they need act.spend.
func (s *Service) gateExternalAct(ctx context.Context, rec session.ChatRecord, tools []session.Tool) []session.Tool {
	pol, ok := s.actPolicy(ctx, rec)
	if !ok {
		return tools
	}
	out := make([]session.Tool, len(tools))
	for i, t := range tools {
		out[i] = s.gateAct(rec, pol, t, builtins.ActTool{Name: t.Name, Class: builtins.ClassSpend})
	}
	return out
}

// gateVoiceProfile applies the policy to voice_profile by action. list only
// reads the library, so act.spend does not gate it. delete removes a voice and
// no person watches the run, so it is refused whatever the run holds. create
// keeps the act.spend gate; its own guard refuses it when nobody can confirm.
func (s *Service) gateVoiceProfile(rec session.ChatRecord, pol builtins.ActPolicy, t session.Tool) session.Tool {
	spend := s.gateAct(rec, pol, t, builtins.ActTool{Name: t.Name, Class: builtins.ClassSpend})
	run, gated := t.Run, spend.Run
	t.Run = func(ctx context.Context, args json.RawMessage) (string, error) {
		var in localaitools.VoiceProfileToolInput
		_ = json.Unmarshal(args, &in)
		switch in.Action {
		case localaitools.ActionList:
			return run(ctx, args)
		case localaitools.ActionDelete:
			s.log.Info("studio: unattended action", "chat", rec.ID, "origin", rec.Origin, "origin_id", rec.OriginID,
				"tool", t.Name, "allowed", false, "reason", "voice_delete_unattended")
			addontools.OutcomeFrom(ctx).Deny("act_voice_delete_unattended")
			return "Not deleted: this run started by itself and nobody watches it, so it may not delete a voice profile. Do not try again; say that a person must delete it in the Voice library or in a chat they watch.", nil
		}
		return gated(ctx, args)
	}
	return t
}

// gateBuiltinActs applies the policy to the built-in tools that change files
// or run commands. The other built-in tools read, or have their own grant.
func (s *Service) gateBuiltinActs(ctx context.Context, rec session.ChatRecord, tools []session.Tool) []session.Tool {
	pol, ok := s.actPolicy(ctx, rec)
	if !ok {
		return tools
	}
	out := make([]session.Tool, len(tools))
	for i, t := range tools {
		out[i] = t
		switch {
		case t.Name == localaitools.ToolVoiceProfile:
			out[i] = s.gateVoiceProfile(rec, pol, t)
		case clientToolNames[t.Name]:
			// The LocalAI client tools run models: they spend compute.
			out[i] = s.gateAct(rec, pol, t, builtins.ActTool{Name: t.Name, Class: builtins.ClassSpend})
		case fileWriteTools[t.Name]:
			out[i] = s.gateAct(rec, pol, t, builtins.ActTool{Name: t.Name, Class: builtins.ClassFiles})
		case t.Name == commandTool && t.Provider == BuiltinProvider:
			// Commands on this machine reach the files and the network.
			out[i] = s.gateAct(rec, pol, t, builtins.ActTool{Name: t.Name, Class: builtins.ClassFiles, External: true})
		}
	}
	return out
}
