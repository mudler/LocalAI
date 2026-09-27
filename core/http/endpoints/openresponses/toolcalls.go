package openresponses

import (
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/functions"
)

// checkToolCalls drops the tool calls that do not fit funcs, whether they
// came from the C++ autoparser (fromDeltas) or from Go-side text parsing,
// unless LocalAI sent its own grammar (see functions.ValidatesToolCalls).
//
// droppedAll reports that calls were dropped and no real call is left; the
// response must then answer with answer: content when set, else the model's
// output, and for autoparser calls, whose source text is in neither, the
// calls rendered as text after the content.
func checkToolCalls(calls []functions.FuncCallResults, fromDeltas bool, content, output string, cfg *config.ModelConfig, funcs functions.Functions, noAction string) (valid []functions.FuncCallResults, answer string, droppedAll bool) {
	if len(calls) == 0 || !functions.ValidatesToolCalls(cfg.Grammar, cfg.FunctionsConfig, funcs) {
		return calls, content, false
	}
	valid, dropped := functions.SplitFuncCalls(calls, funcs, noAction)
	if len(dropped) == 0 {
		return valid, content, false
	}
	for _, c := range valid {
		if c.Name != noAction {
			return valid, content, false
		}
	}
	if fromDeltas {
		base := content
		if strings.TrimSpace(base) == "" {
			base = output
		}
		return valid, functions.AnswerText(base, dropped, true), true
	}
	if strings.TrimSpace(content) == "" {
		content = output
	}
	return valid, content, true
}
