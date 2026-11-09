package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

const (
	listSandboxProfilesName = "list_sandbox_profiles"
	useSandboxProfileName   = "use_sandbox_profile"
)

// The descriptions are short: the tools sit in the group of the commands, which
// every chat with a sandbox sends at every turn.
const (
	listSandboxProfilesDescription = "List the sandbox profiles you can run commands in: id, the programs each has and whether the user may use it. " +
		"Call it when a task needs a program the current sandbox lacks."
	useSandboxProfileDescription = "Switch the sandbox profile of this chat, for example to media for ffmpeg. The sandbox restarts at the next command: the user's files stay, " +
		"running processes and installed packages are lost. Switch only when a task needs it; if the user did not ask, say why first."
)

// registerSandboxTools adds list_sandbox_profiles and use_sandbox_profile to the
// registry of built-in tools, in the group of the commands: a chat that
// switched the commands off has neither. They have the permission of the
// profile picker of the chat (the user's own chat and the administrator's
// policy), and no runtime setting of their own.
func (s *Service) registerSandboxTools() error {
	build := func(desc string, schema string, run func(context.Context, string, string, json.RawMessage) (string, error)) func(context.Context, builtins.Chat) (session.Tool, bool, string) {
		return func(ctx context.Context, c builtins.Chat) (session.Tool, bool, string) {
			if c.SubAgent {
				return session.Tool{}, false, "a sub-agent runs in the sandbox profile of the chat that started it"
			}
			p := c.Actor.Principal
			if _, ok := s.profileCatalogOf(ctx, p); !ok {
				return session.Tool{}, false, "no sandbox with profiles runs commands for this user"
			}
			chat := c.Actor.ChatID
			return session.Tool{Description: desc, Schema: json.RawMessage(schema),
				// The user and the chat come from the turn, never from the arguments.
				Run: func(ctx context.Context, args json.RawMessage) (string, error) { return run(ctx, p, chat, args) }}, true, ""
		}
	}
	if err := s.builtins.Register(builtins.Tool{
		ID: listSandboxProfilesName, Group: builtins.GroupShell, Unlisted: true, ReadOnly: true,
		Label: "Command profiles", Description: "Let a chat list the sandbox profiles it can run commands in.",
		Build: build(listSandboxProfilesDescription, `{"type":"object","properties":{},"additionalProperties":false}`, s.listSandboxProfilesTool),
	}); err != nil {
		return err
	}
	return s.builtins.Register(builtins.Tool{
		ID: useSandboxProfileName, Group: builtins.GroupShell, Unlisted: true,
		Label: "Switch command profile", Description: "Let a chat switch the sandbox profile of its commands.",
		Build: build(useSandboxProfileDescription,
			`{"type":"object","properties":{"profile":{"type":"string","description":"The profile ID, as list_sandbox_profiles gives it."}},"required":["profile"],"additionalProperties":false}`,
			s.useSandboxProfileTool),
	})
}

func (s *Service) listSandboxProfilesTool(ctx context.Context, user, chat string, _ json.RawMessage) (string, error) {
	v, err := s.SandboxProfiles(ctx, user, chat)
	if err != nil {
		return "", err
	}
	if !v.Applies {
		return "The commands of this chat do not run in a sandbox with profiles.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "This chat runs the %s profile.\n", v.Profile)
	for _, p := range v.Profiles {
		line := fmt.Sprintf("- %s: %s Programs: %s.", p.ID, p.Description, strings.Join(p.Tools, ", "))
		switch {
		case !p.Allowed:
			line += " Not allowed for this user."
		case p.State.Phase == "pulling":
			line += fmt.Sprintf(" Its image is being prepared (%d%%); the first command waits for it.", p.State.Percent)
		case p.State.Phase == "failed":
			line += " Its image could not be prepared, so it would run base."
		case p.State.Phase == "idle":
			line += " Its image is pulled on first use, which can take a while."
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (s *Service) useSandboxProfileTool(ctx context.Context, user, chat string, args json.RawMessage) (string, error) {
	var in struct {
		Profile string `json:"profile"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Profile) == "" {
		return "", errors.New("use_sandbox_profile needs a profile")
	}
	switch err := s.SetSandboxProfile(ctx, user, chat, strings.TrimSpace(in.Profile)); {
	case errors.Is(err, ErrSandboxProfileUnknown):
		return "There is no such profile. Call list_sandbox_profiles for the ones you can use.", nil
	case errors.Is(err, ErrSandboxProfileNotAllowed):
		return "Not permitted: an administrator did not give this user that profile. Tell the user, and do not try it again.", nil
	case err != nil:
		return "", err
	}
	v, err := s.SandboxProfiles(ctx, user, chat)
	if err != nil {
		return "", err
	}
	out := fmt.Sprintf("The sandbox profile is %s now. The sandbox restarts at your next command: the user's files stay, running processes and installed packages are lost.", v.Profile)
	for _, p := range v.Profiles {
		if p.ID == v.Profile {
			out += " It has: " + strings.Join(p.Tools, ", ") + "."
			switch p.State.Phase {
			case "idle":
				out += " Its image is pulled on first use, so the next command can take a while."
			case "pulling":
				out += fmt.Sprintf(" Its image is being prepared (%d%%).", p.State.Percent)
			}
		}
	}
	if v.Fallback {
		out += " Its image is not available (" + v.FallbackReason + "), so commands run in base. Tell the user."
	}
	return out, nil
}
