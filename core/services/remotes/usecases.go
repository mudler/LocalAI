package remotes

import (
	"slices"

	"github.com/mudler/LocalAI/core/config"
)

// reservedUsecases are valid known_usecases that a person does not pick for a
// remote model: they reserve a model for a local direct-decode primitive.
var reservedUsecases = []string{
	config.UsecaseDecisions,
	config.UsecaseScore,
	config.UsecaseTokenClassify,
}

// validUsecases keeps the names of capabilities that known_usecases accepts,
// in the order given and without repeats. It drops modifiers such as "tools"
// and "thinking" and the reserved ones. If nothing is left it returns chat,
// because the localai-proxy backend needs a declared usecase.
func validUsecases(capabilities []string) []string {
	out := make([]string, 0, len(capabilities))
	for _, c := range capabilities {
		if slices.Contains(out, c) || slices.Contains(reservedUsecases, c) {
			continue
		}
		// GetUsecasesFromYAML returns a non-nil zero value for a name it
		// does not know, so the value is checked as well.
		if u := config.GetUsecasesFromYAML([]string{c}); u == nil || *u == config.FLAG_ANY {
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return []string{config.UsecaseChat}
	}
	return out
}
