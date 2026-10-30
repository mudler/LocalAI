package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var (
	// ErrBadSkill means a skill has no valid name or description.
	ErrBadSkill = errors.New("files: a skill needs a valid name and a description")
	// ErrNotAdmin means only an administrator may change the shared skills.
	ErrNotAdmin = errors.New("files: only an administrator may change the shared skills")
)

// skillDir returns the workspace folder of a skill, and the actor that changes
// it. A personal skill is changed by its user, a shared one by the server on an
// administrator's behalf; the built-in layer is not changed through here.
func (s *Service) skillDir(principal string, layer skills.Layer, name string, write bool) (string, workspace.Actor, error) {
	if !skills.ValidName(name) {
		return "", workspace.Actor{}, fmt.Errorf("%w: %q is not a skill name", ErrBadSkill, name)
	}
	switch layer {
	case skills.LayerPersonal:
		if !validPrincipal(principal) {
			return "", workspace.Actor{}, workspace.ErrBadName
		}
		return session.HomePath(principal) + "/" + skills.SkillsDir + "/" + name, s.Actor(principal), nil
	case skills.LayerShared:
		if write {
			if principal != AdminPrincipal {
				return "", workspace.Actor{}, ErrNotAdmin
			}
			return SharedSkillsPath + "/" + name, s.admin, nil
		}
		return SharedSkillsPath + "/" + name, s.Actor(principal), nil
	case skills.LayerBuiltin:
		if write {
			return "", workspace.Actor{}, fmt.Errorf("%w: the built-in skills ship with the server", ErrBadSkill)
		}
		return BuiltinSkillsPath + "/" + name, s.Actor(principal), nil
	}
	return "", workspace.Actor{}, fmt.Errorf("%w: unknown layer %q", ErrBadSkill, layer)
}

// WriteSkill creates or replaces a skill: a folder with a SKILL.md made of the
// name, the description and the body. For the shared layer principal must be
// AdminPrincipal. The description is one line; line breaks and control
// characters are folded away, so it cannot add frontmatter keys.
func (s *Service) WriteSkill(ctx context.Context, principal string, layer skills.Layer, name, description, body string) error {
	if !s.Available() {
		return ErrUnavailable
	}
	dir, actor, err := s.skillDir(principal, layer, name, true)
	if err != nil {
		return err
	}
	description = skills.Clean(description)
	if description == "" {
		return fmt.Errorf("%w: the description is empty", ErrBadSkill)
	}
	text := "---\nname: " + name + "\ndescription: " + yamlQuote(description) + "\n---\n" + strings.TrimLeft(body, "\n")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	// What is stored must parse back to what was asked for.
	if n, d, perr := skills.Parse([]byte(text)); perr != nil || n != name || d != description {
		return fmt.Errorf("%w: the text does not make a valid SKILL.md", ErrBadSkill)
	}
	_, err = s.ws.Put(ctx, actor, dir+"/"+skills.SkillFile, strings.NewReader(text), workspace.PutOptions{Overwrite: true})
	return err
}

// ReadSkill returns a skill's description and its body, without the frontmatter.
func (s *Service) ReadSkill(ctx context.Context, principal string, layer skills.Layer, name string) (description, body string, err error) {
	if !s.Available() {
		return "", "", ErrUnavailable
	}
	dir, actor, err := s.skillDir(principal, layer, name, false)
	if err != nil {
		return "", "", err
	}
	r, _, err := s.ws.Open(ctx, actor, dir+"/"+skills.SkillFile)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return "", "", err
	}
	_, description, err = skills.Parse(data)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrBadSkill, err)
	}
	return description, skillBody(string(data)), nil
}

// DeleteSkill moves a skill's folder to the Trash.
func (s *Service) DeleteSkill(ctx context.Context, principal string, layer skills.Layer, name string) error {
	if !s.Available() {
		return ErrUnavailable
	}
	dir, actor, err := s.skillDir(principal, layer, name, true)
	if err != nil {
		return err
	}
	return s.ws.Delete(ctx, actor, dir)
}

// skillBody returns the text after the frontmatter of a SKILL.md.
func skillBody(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return text
	}
	if _, after, found := strings.Cut(rest, "\n---\n"); found {
		return after
	}
	if _, after, found := strings.Cut(rest, "\n---"); found && !strings.Contains(after, "\n") {
		return ""
	}
	return rest
}

// yamlQuote quotes a one-line value for the frontmatter when it has a
// character that YAML reads as syntax.
func yamlQuote(s string) string {
	if s != strings.TrimSpace(s) || strings.ContainsAny(s, ":#'\"{}[]&*!|>%@`,") || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "?") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	return s
}
