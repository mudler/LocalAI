package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
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
	return s.readSkillAt(ctx, actor, dir)
}

// agentSkillDir returns the workspace folder of a skill of the agent layer.
func (s *Service) agentSkillDir(principal, agent, name string) (string, error) {
	switch {
	case !skills.ValidName(name):
		return "", fmt.Errorf("%w: %q is not a skill name", ErrBadSkill, name)
	case !validPrincipal(principal), !ValidAgentID(agent):
		return "", workspace.ErrBadName
	}
	return session.HomePath(principal) + "/" + AgentSkillsDir(agent) + "/" + name, nil
}

// ReadAgentSkill reads a skill of the agent layer of one saved agent, like
// ReadSkill.
func (s *Service) ReadAgentSkill(ctx context.Context, principal, agent, name string) (description, body string, err error) {
	if !s.Available() {
		return "", "", ErrUnavailable
	}
	dir, err := s.agentSkillDir(principal, agent, name)
	if err != nil {
		return "", "", err
	}
	return s.readSkillAt(ctx, s.Actor(principal), dir)
}

// DeleteAgentSkill moves a skill of an agent to the Trash. Writing one is a
// confirmed proposal (kind skill), never a direct call, so there is no writer
// here.
func (s *Service) DeleteAgentSkill(ctx context.Context, principal, agent, name string) error {
	if !s.Available() {
		return ErrUnavailable
	}
	dir, err := s.agentSkillDir(principal, agent, name)
	if err != nil {
		return err
	}
	return s.ws.Delete(ctx, s.Actor(principal), dir)
}

// ReadAddonSkill reads a skill of the add-on layer, like ReadSkill: the skill
// name of the add-on addonID. The layer is read-only for everyone.
func (s *Service) ReadAddonSkill(ctx context.Context, principal, addonID, name string) (description, body string, err error) {
	if !s.Available() {
		return "", "", ErrUnavailable
	}
	if !skills.ValidName(name) || !skills.ValidName(addonID) {
		return "", "", fmt.Errorf("%w: %q is not a skill of an add-on", ErrBadSkill, addonID+"/"+name)
	}
	return s.readSkillAt(ctx, s.Actor(principal), AddonSkillsPath+"/"+addonID+"/"+name)
}

// ReadSkillFile returns the SKILL.md of the skill folder dir (a workspace path,
// skills.Skill.Path) as principal reads it: the same bytes the file tools give
// the model, at most skills.MaxFileSize of them.
func (s *Service) ReadSkillFile(ctx context.Context, principal, dir string) (string, error) {
	if !s.Available() {
		return "", ErrUnavailable
	}
	r, _, err := s.ws.Open(ctx, s.Actor(principal), dir+"/"+skills.SkillFile)
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(io.LimitReader(r, skills.MaxFileSize))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// readSkillAt reads the SKILL.md of the skill folder dir as actor.
func (s *Service) readSkillAt(ctx context.Context, actor workspace.Actor, dir string) (description, body string, err error) {
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

// ErrSkillExists means a skill of that name is already there. An import never
// replaces a skill.
var ErrSkillExists = errors.New("files: a skill of that name exists already")

// ImportSkills stores skills that skills.ReadPacks has checked in the personal
// layer of principal, as one change set (one Undo takes all of them back). The
// files are only stored: nothing in a pack is run. Each skill gets the
// skills.NewMarker file, which says it was imported and has not been used yet.
//
// It refuses the whole import, before it writes anything, when one name is
// taken in the layer, and when reserved says a name belongs to the product.
func (s *Service) ImportSkills(ctx context.Context, principal string, packs []skills.Pack, reserved func(string) bool) (int64, error) {
	if !s.Available() {
		return 0, ErrUnavailable
	}
	dirs := make([]string, len(packs))
	var actor workspace.Actor
	for i, p := range packs {
		dir, a, err := s.skillDir(principal, skills.LayerPersonal, p.Name, true)
		if err != nil {
			return 0, err
		}
		if reserved != nil && reserved(p.Name) {
			return 0, fmt.Errorf("%w: %q is the name of a skill that ships with LocalAI; rename the skill and import it again", ErrBadSkill, p.Name)
		}
		dirs[i], actor = dir, a
	}
	if err := s.EnsureHome(ctx, principal); err != nil {
		return 0, err
	}
	for i, p := range packs {
		if _, err := s.ws.Stat(ctx, actor, dirs[i]); err == nil {
			return 0, fmt.Errorf("%w: %q", ErrSkillExists, p.Name)
		} else if !errors.Is(err, workspace.ErrNotFound) {
			return 0, err
		}
	}
	title := "Import skill " + packs[0].Name
	if len(packs) > 1 {
		title = fmt.Sprintf("Import %d skills", len(packs))
	}
	tx, err := s.ws.Begin(ctx, actor, title)
	if err != nil {
		return 0, err
	}
	for i, p := range packs {
		for _, f := range p.Files {
			if _, err := tx.Put(ctx, dirs[i]+"/"+f.Path, bytes.NewReader(f.Data), workspace.PutOptions{MaxSize: int64(len(f.Data)) + 1, Title: title}); err != nil {
				_ = tx.Rollback()
				return 0, err
			}
		}
		if _, err := tx.Put(ctx, dirs[i]+"/"+skills.NewMarker, bytes.NewReader(nil), workspace.PutOptions{MaxSize: 1, Title: title}); err != nil {
			_ = tx.Rollback()
			return 0, err
		}
	}
	cs, err := tx.Commit()
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return 0, err
	}
	return cs.Rev, nil
}

// SkillIsNew reports whether the personal skill name was imported and no chat
// has used it yet.
func (s *Service) SkillIsNew(ctx context.Context, principal, name string) bool {
	if !s.Available() {
		return false
	}
	dir, actor, err := s.skillDir(principal, skills.LayerPersonal, name, false)
	if err != nil {
		return false
	}
	_, err = s.ws.Stat(ctx, actor, dir+"/"+skills.NewMarker)
	return err == nil
}

// ClearSkillNew removes the mark of an imported skill that a chat used for the
// first time. It does nothing for a skill without the mark.
func (s *Service) ClearSkillNew(ctx context.Context, principal, name string) {
	if !s.SkillIsNew(ctx, principal, name) {
		return
	}
	dir, actor, err := s.skillDir(principal, skills.LayerPersonal, name, true)
	if err != nil {
		return
	}
	_ = s.ws.Delete(ctx, actor, dir+"/"+skills.NewMarker)
}

// exportMaxFolders bounds the folders an export walks.
const exportMaxFolders = 200

// ReadSkillPack reads every file of a personal skill, except the mark of a new
// import, so that skills.WriteZip can make an archive. A skill over the import
// limits is refused, because it could not be imported again.
func (s *Service) ReadSkillPack(ctx context.Context, principal, name string) (skills.Pack, error) {
	if !s.Available() {
		return skills.Pack{}, ErrUnavailable
	}
	dir, actor, err := s.skillDir(principal, skills.LayerPersonal, name, false)
	if err != nil {
		return skills.Pack{}, err
	}
	maxFiles, maxFile, maxTotal := skills.DefaultPackEntries, int64(skills.DefaultPackFileBytes), int64(skills.DefaultPackBytes)
	pack := skills.Pack{Name: name}
	var total int64
	folders := []string{""}
	for n := 0; len(folders) > 0; n++ {
		if n >= exportMaxFolders {
			return skills.Pack{}, fmt.Errorf("%w: the skill has too many folders to export", ErrBadSkill)
		}
		rel := folders[0]
		folders = folders[1:]
		kids, err := s.ws.List(ctx, actor, strings.TrimSuffix(dir+"/"+rel, "/"))
		if err != nil {
			return skills.Pack{}, err
		}
		for _, k := range kids {
			krel := k.Name
			if rel != "" {
				krel = rel + "/" + k.Name
			}
			if k.Kind == workspace.KindDir {
				folders = append(folders, krel)
				continue
			}
			if krel == skills.NewMarker {
				continue
			}
			if len(pack.Files) >= maxFiles || k.Size > maxFile || total+k.Size > maxTotal {
				return skills.Pack{}, fmt.Errorf("%w: the skill is larger than an archive may be (%d files, %d MiB a file, %d MiB in all)", ErrBadSkill, maxFiles, maxFile>>20, maxTotal>>20)
			}
			r, _, err := s.ws.Open(ctx, actor, dir+"/"+krel)
			if err != nil {
				return skills.Pack{}, err
			}
			data, err := io.ReadAll(io.LimitReader(r, maxFile+1))
			_ = r.Close()
			if err != nil {
				return skills.Pack{}, err
			}
			total += int64(len(data))
			pack.Files = append(pack.Files, skills.PackFile{Path: krel, Data: data})
		}
	}
	var text []byte
	for _, f := range pack.Files {
		if f.Path == skills.SkillFile {
			text = f.Data
		}
	}
	if text == nil {
		return skills.Pack{}, workspace.ErrNotFound
	}
	_, pack.Description, _ = skills.Parse(text)
	sort.Slice(pack.Files, func(i, j int) bool { return pack.Files[i].Path < pack.Files[j].Path })
	return pack, nil
}
