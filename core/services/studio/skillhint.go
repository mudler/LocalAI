package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// skillHintTools wraps the tools that read a path (read_file, read_document
// and list_files): when one answers "not found" for a path that names a skill
// of the chat, the error says where the skill is and which tool reads it. A
// model that guesses the folder of a skill (the folder for the user's own
// skills, or a path of the Sandbox) then finds it without help. The skills are
// resolved for the chat only when a read fails, so a good read costs nothing.
func (s *Service) skillHintTools(a workspace.Actor, tools []session.Tool) []session.Tool {
	for i, tool := range tools {
		switch tool.Name {
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
			return out, fmt.Errorf("not found. The skill %s is at %s; read it with read_file.", sk.Name, sk.GuestPath)
		}
	}
	return tools
}
