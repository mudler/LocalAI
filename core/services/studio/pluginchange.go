package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Plugin actions of the assistant through a confirmation card.
//
// The plugins tool of the admin group reads marketplace text that third
// parties wrote. A description can hold instructions that steer the model
// into an install, a switch or a new marketplace, so a model call of such an
// action never changes the server by itself. It becomes a change proposal of
// the kind plugin: the person sees a card with the action and its target, and
// only their confirmation runs the tool, as the owner of the chat. The read
// actions and refresh_marketplace run at once. The admin group is offered
// only in chats a person watches (no sub-agent, trigger, job or messaging
// chat), so a card can always be answered.

// pluginChangeTarget is the target of a plugin proposal; it names no file.
const pluginChangeTarget = "plugins"

// maxPluginChangeContent bounds the content: one flat PluginsRequest.
const maxPluginChangeContent = 4 << 10

func (c *Changes) pluginKind() proposals.Kind {
	s := c.s
	return proposals.Kind{
		Name:      contract.ChangePlugin,
		Sensitive: true,
		Internal:  true,
		MaxBytes:  maxPluginChangeContent,
		Path: func(a proposals.Aim) (string, error) {
			if a.Scope != contract.ScopeUser {
				return "", fmt.Errorf("%w: a plugin action is the user's: use scope user", proposals.ErrBadTarget)
			}
			return pluginChangeTarget, nil
		},
		Check: func(in proposals.CheckInput) error {
			_, err := parsePluginChange(in.Content)
			return err
		},
		Dest: &proposals.Destination{
			Prepare: func(context.Context, proposals.ApplyInput) error {
				if s.d.Assistant == nil || s.d.Assistant.Executor() == nil {
					return errors.New("the LocalAI admin tools are not available on this server")
				}
				return nil
			},
			Apply: func(ctx context.Context, in proposals.ApplyInput) error {
				return s.applyPluginChange(ctx, in)
			},
			Undo: func(context.Context, proposals.ApplyInput) error {
				return errors.New("change the plugin on the Plugins tab of the Add-ons page")
			},
		},
	}
}

func parsePluginChange(content string) (localaitools.PluginsRequest, error) {
	var in localaitools.PluginsRequest
	dec := json.NewDecoder(strings.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, errors.New("the content of a plugin action is the JSON arguments of the plugins tool")
	}
	// One object and nothing after it, as the REST endpoints decode a body.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return in, errors.New("the content of a plugin action is one JSON object")
	}
	if !localaitools.PluginsActionConfirms(in.Action) {
		return in, fmt.Errorf("the plugins action %q does not need a confirmation", in.Action)
	}
	return in, nil
}

// applyPluginChange runs the confirmed action with the admin tools, as the
// owner of the proposal. The tool keeps its own checks: the rights of the
// REST API, and the refusal of a plugin that runs code outside the browser
// session.
func (s *Service) applyPluginChange(ctx context.Context, in proposals.ApplyInput) error {
	req, err := parsePluginChange(in.Proposal.Content)
	if err != nil {
		return err
	}
	owner, err := s.ownerOf(ctx, in.Owner)
	if err != nil {
		return fmt.Errorf("nothing changed: %w", err)
	}
	if s.d.Assistant == nil {
		return errors.New("the LocalAI admin tools are not available on this server")
	}
	exec := mcpendpoint.AsUser(s.d.Assistant.Executor(), owner)
	if exec == nil {
		return errors.New("the LocalAI admin tools are not available on this server")
	}
	args, _ := json.Marshal(req)
	out, err := exec.ExecuteTool(ctx, localaitools.ToolPlugins, string(args))
	if err != nil {
		return err
	}
	// A refusal of the tool comes back as plain text; a result is JSON.
	if t := strings.TrimSpace(out); !json.Valid([]byte(t)) || (!strings.HasPrefix(t, "{") && !strings.HasPrefix(t, "[")) {
		return errors.New(t)
	}
	s.log.Info("studio: plugin action run after a confirmation", "owner", in.Owner, "proposal", in.Proposal.ID, "action", req.Action)
	return nil
}

// guardPlugins replaces the Run of the plugins tool so that an action that
// changes the server goes through a confirmation card.
func (s *Service) guardPlugins(tools []session.Tool, chat builtins.Chat) []session.Tool {
	for i, t := range tools {
		if t.Name != localaitools.ToolPlugins {
			continue
		}
		run := t.Run
		tools[i].Run = func(ctx context.Context, args json.RawMessage) (string, error) {
			var in localaitools.PluginsRequest
			if json.Unmarshal(args, &in) != nil || !localaitools.PluginsActionConfirms(in.Action) {
				return run(ctx, args)
			}
			return s.proposePluginChange(ctx, chat, in)
		}
	}
	return tools
}

// pluginChangeSummary is the line of the card.
func pluginChangeSummary(in localaitools.PluginsRequest) string {
	switch in.Action {
	case localaitools.PluginsActionInstall:
		if in.Scope == "instance" {
			return fmt.Sprintf("Install the plugin %s for everyone", in.Plugin)
		}
		return fmt.Sprintf("Install the plugin %s for you", in.Plugin)
	case localaitools.PluginsActionEnable:
		return fmt.Sprintf("Turn on the plugin install %s for you", in.ID)
	case localaitools.PluginsActionDisable:
		return fmt.Sprintf("Turn off the plugin install %s for you", in.ID)
	case localaitools.PluginsActionRemove:
		return fmt.Sprintf("Remove the plugin install %s", in.ID)
	case localaitools.PluginsActionAddMarketplace:
		return fmt.Sprintf("Add the plugin marketplace %s", in.Source)
	}
	return "Change the plugins: " + in.Action
}

// proposePluginChange turns a call into a proposal. Nothing changes.
func (s *Service) proposePluginChange(ctx context.Context, chat builtins.Chat, in localaitools.PluginsRequest) (string, error) {
	if chat.SubAgent || chat.Origin != "" {
		// The admin group is not offered there; keep the rule if that changes.
		return "Nothing changed: plugin changes are confirmed by a person in a chat they watch. Do not try again here.", nil
	}
	// Only the fields of the action go into the card, so the person confirms
	// exactly what runs.
	clean := localaitools.PluginsRequest{Action: in.Action}
	switch in.Action {
	case localaitools.PluginsActionInstall:
		clean.Plugin, clean.Scope = strings.TrimSpace(in.Plugin), in.Scope
	case localaitools.PluginsActionAddMarketplace:
		clean.Source, clean.Ref, clean.Marketplace = strings.TrimSpace(in.Source), in.Ref, in.Marketplace
	default:
		clean.ID = strings.TrimSpace(in.ID)
	}
	content, _ := json.Marshal(clean)
	p, err := s.changes.Propose(ctx, ProposeInput{User: chat.Actor.Principal, Agent: chat.AgentID,
		Source: contract.ChangeSource{Chat: chat.Actor.ChatID}, Kind: string(contract.ChangePlugin), Scope: string(contract.ScopeUser),
		Content: string(content), Summary: pluginChangeSummary(clean)})
	if err != nil {
		return "", changeErr(err)
	}
	session.SetToolCard(ctx, session.ToolCard{Kind: ChangeProposalCardKind, ID: p.ID})
	return fmt.Sprintf("Proposal %s stored. Nothing changed yet: the person must confirm the card in this chat, and it expires in 24 hours. "+
		"Do not say that it is done. After they confirm, call plugins with list_installed to see the result.", p.ID), nil
}
