package agents_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/agents"
)

func TestValidate(t *testing.T) {
	ok := agents.Agent{Name: "Reviewer", Addons: []string{"office"}}
	if err := agents.Validate(ok); err != nil {
		t.Fatal(err)
	}
	many := make([]string, agents.MaxListEntries+1)
	for i := range many {
		many[i] = "x"
	}
	for name, a := range map[string]agents.Agent{
		"empty name":       {Name: "  "},
		"long name":        {Name: strings.Repeat("n", agents.MaxNameRunes+1)},
		"long desc":        {Name: "a", Description: strings.Repeat("d", agents.MaxDescription+1)},
		"instructions":     {Name: "a", Instructions: strings.Repeat("i", agents.MaxInstructions+1)},
		"model":            {Name: "a", Model: strings.Repeat("m", agents.MaxModelRunes+1)},
		"too many":         {Name: "a", Skills: many},
		"bad addon":        {Name: "a", Addons: []string{"Office Tools"}},
		"empty collection": {Name: "a", Collections: []string{" "}},
		"long entry":       {Name: "a", MCPServers: []string{strings.Repeat("s", agents.MaxEntryRunes+1)}},
	} {
		if err := agents.Validate(a); !errors.Is(err, agents.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
