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

const sandboxProfileName = "sandbox_profile"

// One tool for both operations: they work on the same state, the sandbox profile
// of the chat. The description is short, because the tool sits in the group of
// the commands, which every chat with a sandbox sends at every turn.
const sandboxProfileDescription = "The sandbox profile of this chat. action list: the profiles you can run commands in, with the programs each has and whether the user may use it; " +
	"call it when a task needs a program the current sandbox lacks. action use: switch to profile, for example media for ffmpeg. The sandbox restarts at the next command: " +
	"the user's files stay, running processes and installed packages are lost. Switch only when a task needs it; if the user did not ask, say why first."

const sandboxProfileSchema = `{"type":"object","properties":{` +
	`"action":{"type":"string","enum":["list","use"]},` +
	`"profile":{"type":"string","description":"The profile ID, as action list gives it. Needed by use."}},` +
	`"required":["action"],"additionalProperties":false}`

// registerSandboxTools adds sandbox_profile to the registry of built-in tools,
// in the group of the commands: a chat that switched the commands off does not
// have it. It has the permission of the profile picker of the chat (the user's
// own chat and the administrator's policy), and no runtime setting of its own.
// It is not read only, because the action use changes the chat.
func (s *Service) registerSandboxTools() error {
	return s.builtins.Register(builtins.Tool{
		ID: sandboxProfileName, Group: builtins.GroupShell, Unlisted: true,
		Label: "Command profiles", Description: "Let a chat list the sandbox profiles it can run commands in and switch the one of its commands.",
		Build: func(ctx context.Context, c builtins.Chat) (session.Tool, bool, string) {
			if c.SubAgent {
				return session.Tool{}, false, "a sub-agent runs in the sandbox profile of the chat that started it"
			}
			p := c.Actor.Principal
			if _, ok := s.profileCatalogOf(ctx, p); !ok {
				return session.Tool{}, false, "no sandbox with profiles runs commands for this user"
			}
			chat := c.Actor.ChatID
			return session.Tool{Description: sandboxProfileDescription, Schema: json.RawMessage(sandboxProfileSchema),
				// The user and the chat come from the turn, never from the arguments.
				Run: func(ctx context.Context, args json.RawMessage) (string, error) {
					return s.sandboxProfileTool(ctx, p, chat, args)
				}}, true, ""
		},
	})
}

func (s *Service) sandboxProfileTool(ctx context.Context, user, chat string, args json.RawMessage) (string, error) {
	var in struct {
		Action  string `json:"action"`
		Profile string `json:"profile"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", errors.New("sandbox_profile needs an action: list or use")
	}
	switch in.Action {
	case "list":
		return s.listSandboxProfiles(ctx, user, chat)
	case "use":
		return s.useSandboxProfile(ctx, user, chat, in.Profile)
	}
	return "", fmt.Errorf("sandbox_profile: unknown action %q (use list or use)", in.Action)
}

func (s *Service) listSandboxProfiles(ctx context.Context, user, chat string) (string, error) {
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

func (s *Service) useSandboxProfile(ctx context.Context, user, chat, profile string) (string, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "", errors.New("sandbox_profile: action use needs a profile")
	}
	switch err := s.SetSandboxProfile(ctx, user, chat, profile); {
	case errors.Is(err, ErrSandboxProfileUnknown):
		return "There is no such profile. Call sandbox_profile with action list for the ones you can use.", nil
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
