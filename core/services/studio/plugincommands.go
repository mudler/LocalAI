package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/mudler/nib/plugin/claudeplugin"

	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
)

// Plugin commands. A command of a plugin is typed as
// /<plugin>:<command> <arguments>. The message carries the arguments as its
// text and the command as its skill; the server renders the command's body
// with the arguments ($ARGUMENTS, $ARGUMENTS[N], $N, $name) and the plugin
// variables, and gives it to the model as the note of the turn. Nothing
// passes a body through a template, so "{{" stays as written. The transcript
// keeps the typed text and a command chip.
//
// A body with a shell injection runs in the chat's Sandbox in a later
// version; until then the command answers that it needs one. The "model"
// of a command is not applied yet: the turn uses the chat's model.

// needsLaterVersion is the answer of a command that runs a shell injection.
const needsLaterVersion = "This command needs a later version of LocalAI: it runs a shell command when it starts."

// maxCommandBytes bounds the file of a command, as for a SKILL.md.
const maxCommandBytes = skills.MaxFileSize

// pluginCommand is a command of a plugin that contributes to a chat.
type pluginCommand struct {
	name string // "<plugin>:<command>"
	ap   plugins.ActivePlugin
	ref  plugins.CommandRef
}

// pluginCommands lists the commands of the plugins that contribute to the
// chat now.
func (s *Service) pluginCommands(ctx context.Context, p string, rec session.ChatRecord) []pluginCommand {
	var out []pluginCommand
	for _, ap := range s.activePlugins(ctx, p, rec, s.scopeOf(ctx, rec)) {
		for _, c := range ap.Model.Commands {
			out = append(out, pluginCommand{name: claudeplugin.Qualify(ap.Name, c.Name), ap: ap, ref: c})
		}
	}
	return out
}

// pluginCommandDirective renders a plugin command for the turn.
func (s *Service) pluginCommandDirective(ctx context.Context, p string, rec session.ChatRecord, pc pluginCommand, args string, noTask bool) (string, *session.Invocation, error) {
	if pc.ref.ShellInjection {
		return "", nil, fmt.Errorf("%w: %s", ErrBadCommand, needsLaterVersion)
	}
	body, err := s.pluginCommandBody(ctx, p, pc)
	if err != nil {
		return "", nil, err
	}
	root := plugins.PluginRoot(pc.ap.Digest)
	body = claudeplugin.RenderArguments(body, args, pc.ref.Arguments)
	body = claudeplugin.ExpandContent(body, pluginVars(pc.ap, root, "", rec.ID))
	return commandDirectiveText(pc.name, body, noTask), &session.Invocation{Kind: session.InvokeCommand, Name: pc.name}, nil
}

// pluginCommandBody reads the body of a command, without its frontmatter:
// its file in the package, or its text in the manifest for a command that has
// no file.
func (s *Service) pluginCommandBody(ctx context.Context, p string, pc pluginCommand) (string, error) {
	base := files.PluginsPath + "/" + strings.TrimPrefix(pc.ap.Digest, "sha256:")
	if pc.ref.Path == "" {
		return s.inlineCommandBody(ctx, p, base, pc.ref.Name)
	}
	rel := path.Clean(pc.ref.Path)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return "", fmt.Errorf("%w: the command %q has no readable file", ErrBadCommand, pc.name)
	}
	data, err := s.readPackageFile(ctx, p, base+"/"+rel)
	if err != nil {
		return "", fmt.Errorf("studio: read the command %q: %w", pc.name, err)
	}
	_, body := claudeplugin.SplitFrontmatter(data)
	return body, nil
}

// inlineCommandBody reads the text of a command that the manifest holds
// ("commands": {"<name>": {"content": "..."}}).
func (s *Service) inlineCommandBody(ctx context.Context, p, base, name string) (string, error) {
	data, err := s.readPackageFile(ctx, p, base+"/.claude-plugin/plugin.json")
	if err != nil {
		return "", fmt.Errorf("studio: read the manifest of a plugin: %w", err)
	}
	var m struct {
		Commands map[string]struct {
			Content string `json:"content"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(data, &m); err != nil || m.Commands[name].Content == "" {
		return "", fmt.Errorf("%w: the command %q has no text", ErrBadCommand, name)
	}
	_, body := claudeplugin.SplitFrontmatter([]byte(m.Commands[name].Content))
	return body, nil
}

// readPackageFile reads a file of a package as the user, who reads every
// package.
func (s *Service) readPackageFile(ctx context.Context, p, abs string) ([]byte, error) {
	if !s.d.Files.Available() {
		return nil, files.ErrUnavailable
	}
	rc, node, err := s.d.Files.Store().Open(ctx, s.d.Files.Actor(p), abs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	if node.Size > maxCommandBytes {
		return nil, errors.New("the file is too large")
	}
	data, err := io.ReadAll(io.LimitReader(rc, maxCommandBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCommandBytes {
		return nil, errors.New("the file is too large")
	}
	return data, nil
}

// renderPluginSkill renders the body of a plugin skill that the user started
// with /: without its frontmatter, with the typed arguments and the plugin
// variables.
func (s *Service) renderPluginSkill(ctx context.Context, p string, rec session.ChatRecord, sk skills.Skill, file, args string) (string, error) {
	ap, ok := s.pluginOf(ctx, p, rec, s.scopeOf(ctx, rec), sk.PluginInstall)
	if !ok {
		return "", fmt.Errorf("%w: the plugin of the skill %q is not on for this chat", ErrBadCommand, sk.Name)
	}
	_, body := claudeplugin.SplitFrontmatter([]byte(file))
	var names []string
	for _, r := range ap.Model.Skills {
		if claudeplugin.Qualify(ap.Name, r.Name) == sk.Name {
			if r.ShellInjection {
				return "", fmt.Errorf("%w: %s", ErrBadCommand, needsLaterVersion)
			}
			names = r.Arguments
		}
	}
	if args != "" {
		body = claudeplugin.RenderArguments(body, args, names)
	}
	return claudeplugin.ExpandContent(body, pluginVars(ap, sk.PluginRoot, path.Dir(sk.GuestPath), rec.ID)), nil
}

// commandDirectiveText is the note the model gets before a message sent with
// a plugin command, in the form of skillDirectiveText.
func commandDirectiveText(name, body string, noTask bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "(System note: the user ran the command /%s for this message. Follow its instructions, which come next", skills.Clean(name))
	if noTask {
		b.WriteString(". The message has no other text: do what the command says now.")
	} else {
		b.WriteString("; the message holds the arguments the user typed.")
	}
	fmt.Fprintf(&b, "\n<command name=%q>\n%s\n</command>)", skills.Clean(name), strings.TrimSpace(body))
	return b.String()
}
