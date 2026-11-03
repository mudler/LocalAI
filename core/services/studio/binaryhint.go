package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// officeKinds are the binary document types a chat meets most: the words a
// skill of the type has in its name, and a library that reads the type in a
// script.
var officeKinds = map[string]struct {
	skills  []string
	library string
}{
	".docx": {[]string{"docx", "word"}, "python-docx"},
	".xlsx": {[]string{"xlsx", "excel"}, "openpyxl"},
	".pptx": {[]string{"pptx", "powerpoint"}, "python-pptx"},
	".pdf":  {[]string{"pdf"}, "pypdf"},
}

// binaryHintTools wraps the tools that read a file as text: when one meets a
// binary file, the error says what the chat can do with it. It names the
// skill that matches the file type when the chat has one, and a script when
// the chat can run commands. The skills are resolved only when a read fails.
func (s *Service) binaryHintTools(a workspace.Actor, canRun bool, tools []session.Tool) []session.Tool {
	for i, tool := range tools {
		switch tool.Name {
		case "read_file", "edit_file":
		default:
			continue
		}
		run := tool.Run
		tools[i].Run = func(ctx context.Context, args json.RawMessage) (string, error) {
			out, err := run(ctx, args)
			var nt *files.NotTextError
			if !errors.As(err, &nt) {
				return out, err
			}
			return out, errors.New(s.binaryAdvice(ctx, a, canRun, nt))
		}
	}
	return tools
}

// binaryAdvice is the error for a binary file, short and true for the tools
// the chat has.
func (s *Service) binaryAdvice(ctx context.Context, a workspace.Actor, canRun bool, nt *files.NotTextError) string {
	ext := strings.ToLower(path.Ext(nt.Name))
	msg := fmt.Sprintf("%s is a binary file (%d bytes), not text.", nt.Name, nt.Size)
	kind, office := officeKinds[ext]
	if office {
		if list, _, err := s.Skills(ctx, a.Principal, a.ChatID); err == nil {
			for _, sk := range list {
				name := strings.ToLower(sk.Name)
				for _, w := range kind.skills {
					if strings.Contains(name, w) {
						return msg + fmt.Sprintf(" Use the %s skill for %s files (load_skill).", sk.Name, ext)
					}
				}
			}
		}
	}
	switch {
	case canRun && office:
		return msg + fmt.Sprintf(" Run a script with the commands tool instead, for example with %s.", kind.library)
	case canRun:
		return msg + " Run a script with the commands tool instead."
	}
	return msg + " This chat has no tool that opens it."
}
