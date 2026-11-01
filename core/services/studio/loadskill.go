package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// loadSkillName is the name of the tool that loads a skill by its name.
const loadSkillName = skills.LoadTool

// Limits of load_skill.
const (
	// maxSkillFilesListed is the most files of a skill's folder that the
	// result names; list_files shows the rest.
	maxSkillFilesListed = 20
	// maxSkillFolders is the most folders of a skill's folder that are
	// listed to find its files.
	maxSkillFolders = 50
	// maxSkillFileDepth is how deep below the skill's folder files are
	// looked for.
	maxSkillFileDepth = 3
	// maxSkillNamesBytes bounds the list of skill names in the tool's
	// description and in its errors.
	maxSkillNamesBytes = 2048
)

// loadOpts says what the chat that calls load_skill can do with a skill.
type loadOpts struct {
	// files is true when the chat has read_file: the result then names the
	// skill's folder and the files in it, which only a file tool can read.
	files bool
	// exec is true when the chat can run commands. A skill that requires
	// them is refused with the reason when it cannot.
	exec bool
}

// registerLoadSkill adds load_skill to the registry of built-in tools. It has
// no switch: a chat that has skills always gets it, because the index of the
// skills tells the model to call it. The skills it loads are the ones the
// index lists, from the same resolution.
func (s *Service) registerLoadSkill() error {
	return s.builtins.Register(builtins.Tool{
		ID:          loadSkillName,
		Label:       "Load skills",
		Description: "Gives the model of a chat the full instructions of one of the chat's skills, by its name. Every chat that has skills gets it.",
		Unlisted:    true,
		ReadOnly:    true,
		Build:       s.buildLoadSkill,
	})
}

// buildLoadSkill gives load_skill to a chat that has at least one skill. The
// tool is built with the files and commands a chat usually has; fitLoadSkill
// corrects that once the chat's tool list is final.
func (s *Service) buildLoadSkill(ctx context.Context, c builtins.Chat) (session.Tool, bool, string) {
	if !s.d.Files.Available() {
		return session.Tool{}, false, "the files are off"
	}
	list, err := s.loadableSkills(ctx, c.Actor)
	if err != nil {
		return session.Tool{}, false, "the skills cannot be read now"
	}
	if len(list) == 0 {
		return session.Tool{}, false, "the chat has no skills"
	}
	return session.Tool{
		Description: "Load the full instructions of a skill by its name, then follow them. Call it before a task that matches a skill. " +
			"Skills: " + nameList(skills.Names(list)) + ".",
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"name":{"type":"string","description":"The name of the skill, as the list of skills shows it, for example writing-skills."}},` +
			`"required":["name"]}`),
		Run: s.loadSkillRun(c.Actor, loadOpts{files: true, exec: true}),
	}, true, ""
}

// fitLoadSkill sets what load_skill may offer from the chat's final tool
// list: the folder and the files of a skill only with read_file, and a skill
// that needs commands only when a tool runs them. a is the actor the tool was
// built for.
func (s *Service) fitLoadSkill(a workspace.Actor, tools []session.Tool) []session.Tool {
	i := slices.IndexFunc(tools, func(t session.Tool) bool { return t.Name == loadSkillName })
	if i < 0 {
		return tools
	}
	tools[i].Run = s.loadSkillRun(a, loadOpts{files: hasTool(tools, "read_file"), exec: s.runsCommands(tools)})
	return tools
}

// loadableSkills returns the skills the chat of a offers now: the list the
// index of its turn shows, from the same resolution.
func (s *Service) loadableSkills(ctx context.Context, a workspace.Actor) ([]skills.Skill, error) {
	rec, err := s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return nil, err
	}
	list, _, _, err := s.chatSkillSet(ctx, a.Principal, rec.SkillsOff, rec.AddonsOff, s.scopeOf(ctx, rec))
	if err != nil {
		return nil, err
	}
	return skills.Usable(list), nil
}

// loadSkillRun is the Run of load_skill. It resolves the skills again at each
// call, so a skill switched off or an add-on stopped since the turn began is
// not loaded.
func (s *Service) loadSkillRun(a workspace.Actor, o loadOpts) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		in, err := session.DecodeArgs[struct {
			Name string `json:"name"`
		}](raw)
		if err != nil {
			return "", err
		}
		list, err := s.loadableSkills(ctx, a)
		if err != nil {
			return "", fmt.Errorf("the skills cannot be read now: %w", err)
		}
		return s.loadSkill(ctx, a.Principal, list, in.Name, o)
	}
}

// loadSkill returns the instructions of the skill of a name among list, read
// as the user. A name that is not in list is an error that names the skills
// there are, and the near ones first.
func (s *Service) loadSkill(ctx context.Context, p string, list []skills.Skill, name string, o loadOpts) (string, error) {
	sk, ok := skills.Find(list, name)
	if !ok {
		return "", unknownSkill(list, name)
	}
	if why := sk.Unavailable; why != "" || (!o.exec && slices.Contains(sk.Requires, skills.RequireExec)) {
		if why == "" {
			why = skillNeedsExec
		}
		return fmt.Sprintf("The skill %s cannot be used in this chat: %s. Do not start it; tell the user plainly what is missing.", sk.Name, why), nil
	}
	actor := s.d.Files.Actor(p)
	rc, node, err := s.d.Files.Store().Open(ctx, actor, sk.Path+"/"+skills.SkillFile)
	if errors.Is(err, workspace.ErrNotFound) {
		return "", unknownSkill(list, name) // removed since it was listed
	}
	if err != nil {
		return "", fmt.Errorf("the skill %s cannot be read now: %w", sk.Name, err)
	}
	defer func() { _ = rc.Close() }()
	if node.Size > skills.MaxFileSize {
		return "", fmt.Errorf("the skill %s is too large to load (more than %d KiB)", sk.Name, skills.MaxFileSize>>10)
	}
	data, err := io.ReadAll(io.LimitReader(rc, skills.MaxFileSize+1))
	if err != nil {
		return "", fmt.Errorf("the skill %s cannot be read now: %w", sk.Name, err)
	}
	if len(data) > skills.MaxFileSize {
		return "", fmt.Errorf("the skill %s is too large to load (more than %d KiB)", sk.Name, skills.MaxFileSize>>10)
	}
	body := skills.Body(data)
	if !o.files || sk.GuestPath == "" {
		return body, nil
	}
	names, more := s.skillFiles(ctx, actor, sk.Path)
	if len(names) == 0 {
		return body, nil
	}
	var b strings.Builder
	b.WriteString("Base directory for this skill: " + path.Dir(sk.GuestPath) + "\n")
	b.WriteString("Files in it (read them with read_file; the paths are relative to the base directory): " + strings.Join(names, ", "))
	if more {
		b.WriteString(", and more (list_files shows them)")
	}
	b.WriteString("\n\n" + body)
	return b.String(), nil
}

// skillFiles names the files below a skill's folder, relative to it, other
// than SKILL.md: at most maxSkillFilesListed, from at most maxSkillFolders
// folders, maxSkillFileDepth levels deep. more is true when some were left out.
func (s *Service) skillFiles(ctx context.Context, a workspace.Actor, dir string) (names []string, more bool) {
	type item struct {
		path, rel string
		depth     int
	}
	queue := []item{{dir, "", 0}}
	listed := 0
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if listed >= maxSkillFolders {
			return names, true
		}
		listed++
		kids, err := s.d.Files.Store().List(ctx, a, it.path)
		if err != nil {
			continue
		}
		for _, k := range kids {
			rel := k.Name
			if it.rel != "" {
				rel = it.rel + "/" + k.Name
			}
			if k.Kind == workspace.KindDir {
				if it.depth+1 < maxSkillFileDepth {
					queue = append(queue, item{path.Join(it.path, k.Name), rel, it.depth + 1})
				} else {
					more = true
				}
				continue
			}
			if it.rel == "" && k.Name == skills.SkillFile {
				continue
			}
			if len(names) == maxSkillFilesListed {
				return names, true
			}
			names = append(names, skills.Clean(rel))
		}
	}
	return names, more
}

// unknownSkill is the error for a name that is not one of the chat's skills:
// the near names, then every name the chat has.
func unknownSkill(list []skills.Skill, name string) error {
	msg := fmt.Sprintf("unknown skill %q.", skills.Clean(name))
	if near := skills.Suggest(list, name); len(near) > 0 {
		msg += " Did you mean " + strings.Join(near, " or ") + "?"
	}
	if names := skills.Names(list); len(names) > 0 {
		msg += " The skills of this chat are: " + nameList(names) + "."
	} else {
		msg += " This chat has no skills now."
	}
	return errors.New(msg)
}

// nameList joins skill names with commas, at most maxSkillNamesBytes of them.
func nameList(names []string) string {
	var b strings.Builder
	for i, n := range names {
		if b.Len()+len(n)+2 > maxSkillNamesBytes {
			fmt.Fprintf(&b, " and %d more", len(names)-i)
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(n)
	}
	return b.String()
}
