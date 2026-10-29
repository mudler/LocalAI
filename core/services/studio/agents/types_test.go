package agents_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/agents"
)

var _ = Describe("agent validation", func() {
	It("accepts a good agent and rejects every bad shape", func() {
		ok := agents.Agent{Name: "Reviewer", Addons: []string{"office"}}
		Expect(agents.Validate(ok)).To(Succeed())
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
			Expect(agents.Validate(a)).To(MatchError(agents.ErrInvalid), name)
		}
	})
})
