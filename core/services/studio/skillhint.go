package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// skillHintTools wraps the tools that read a path (read_file, read_document
// and list_files): when one answers "not found" for a path that names a skill
// of the chat, the error says where the skill is and that load_skill loads it
// by its name. The index names no path, but a model that guesses the folder of
// a skill (the folder for the user's own skills, or a path of the Sandbox)
// still finds it without help. The skills are
// resolved for the chat only when a read fails, so a good read costs nothing.
func (s *Service) skillHintTools(a workspace.Actor, tools []session.Tool) []session.Tool {
	for i, tool := range tools {
		switch tool.Name {
		case "write_file", "move_file":
			tools[i].Run = skillPlaceHint(tool.Name, tool.Run)
			continue
		case "read_file", "read_document", "list_files":
		default:
			continue
		}
		run := tool.Run
		tools[i].Run = func(ctx context.Context, args json.RawMessage) (string, error) {
			out, err := run(ctx, args)
			if err == nil || !strings.HasPrefix(err.Error(), "not found") {
				return out, err
			}
			var in struct{ Path string }
			if json.Unmarshal(args, &in) != nil || in.Path == "" {
				return out, err
			}
			list, _, lerr := s.Skills(ctx, a.Principal, a.ChatID)
			if lerr != nil {
				return out, err
			}
			sk, ok := skills.Where(list, in.Path)
			if !ok {
				return out, err
			}
			return out, fmt.Errorf("not found. The skill %s is at %s; load it with load_skill.", sk.Name, sk.GuestPath)
		}
	}
	return tools
}

// skillPlaceHint wraps write_file and move_file: when a call puts a SKILL.md
// in a folder that is not a skills folder, the result says that it is not
// loaded as a skill and where it belongs. Models that read writing-skills
// still drop the dot and write /workspace/home/skills/<name>/SKILL.md; the
// file then never applies and nothing tells them, so the user gets a skill
// that does nothing. The result names the place, so the model moves the file
// itself on the next step. Any other file is left alone.
func skillPlaceHint(name string, run func(context.Context, json.RawMessage) (string, error)) func(context.Context, json.RawMessage) (string, error) {
	field := "path"
	if name == "move_file" {
		field = "to"
	}
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		out, err := run(ctx, args)
		if err != nil {
			return out, err
		}
		var in map[string]any
		if json.Unmarshal(args, &in) != nil {
			return out, nil
		}
		if note := skillPlaceNote(fmt.Sprint(in[field])); note != "" {
			return out + "\n\n" + note, nil
		}
		return out, nil
	}
}

// skillPlaceNote is the line for a SKILL.md at p outside a skills folder, or
// "" when p is not a SKILL.md or already sits in one.
func skillPlaceNote(p string) string {
	segs := strings.Split(strings.Trim(path.Clean("/"+p), "/"), "/")
	if len(segs) < 2 || segs[len(segs)-1] != "SKILL.md" {
		return ""
	}
	for i, seg := range segs[:len(segs)-1] {
		if seg == skills.SkillsDir {
			return ""
		}
		// The project and agent layers keep their skills in a plain "skills"
		// folder: Projects/<project>/skills and LocalAI/agents/<id>/skills.
		if seg == skills.SkillsLayerDir && i >= 2 &&
			(segs[i-2] == "Projects" || (segs[i-2] == "agents" && i >= 3 && segs[i-3] == "LocalAI")) {
			return ""
		}
	}
	skill := segs[len(segs)-2]
	return fmt.Sprintf("Note: %s is not in a %s folder, so it is not loaded as a skill. "+
		"A skill of your own goes in /workspace/home/%s/%s/SKILL.md: move it there with move_file.",
		p, skills.SkillsDir, skills.SkillsDir, skill)
}
