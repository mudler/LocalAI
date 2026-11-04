package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

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
	kind, known := s.slotForExtension(ctx, a.Principal, ext)
	if known {
		if list, _, err := s.Skills(ctx, a.Principal, a.ChatID); err == nil {
			for _, sk := range list {
				name := strings.ToLower(sk.Name)
				for _, w := range kind.Words {
					if strings.Contains(name, w) {
						return msg + fmt.Sprintf(" Use the %s skill for %s files (load_skill).", sk.Name, ext)
					}
				}
			}
		}
	}
	switch {
	case canRun && known && kind.Library != "":
		return msg + fmt.Sprintf(" Run a script with the commands tool instead, for example with %s.", kind.Library)
	case canRun:
		return msg + " Run a script with the commands tool instead."
	}
	return msg + " This chat has no tool that opens it."
}

// slotForExtension finds the slot that covers files with the extension ext.
// The slots come from the skills the add-ons declare, then from the default
// slots (data in the contract); the host holds no list of formats of its own.
func (s *Service) slotForExtension(ctx context.Context, p, ext string) (contract.Slot, bool) {
	var sets []AddonSkillSet
	if lister, ok := s.d.AddonTools.(AddonSkillLister); ok && s.d.AddonTools != nil {
		sets, _ = lister.AddonSkills(ctx, p)
	}
	for _, set := range sets {
		for _, sl := range set.Slots {
			if sl.Kind == contract.SlotKindDocuments && slices.Contains(sl.Extensions, ext) {
				return sl, true
			}
		}
	}
	for _, sl := range contract.DefaultSlots() {
		if sl.Kind == contract.SlotKindDocuments && slices.Contains(sl.Extensions, ext) {
			return sl, true
		}
	}
	return contract.Slot{}, false
}
