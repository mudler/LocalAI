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
		for _, n := range []string{"", "off", "registries", "all"} {
			Expect(agents.Validate(agents.Agent{Name: "a", Network: n})).To(Succeed(), n)
		}
		for _, f := range []agents.Agent{
			{Name: "a"},
			{Name: "a", FolderID: 7, FolderMode: agents.FolderRead},
			{Name: "a", FolderID: 7, FolderMode: agents.FolderWrite},
			{Name: "a", FolderMode: agents.FolderRead}, // the folder is not chosen yet
		} {
			Expect(agents.Validate(f)).To(Succeed(), "%+v", f)
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
			"bad network":      {Name: "a", Network: "everything"},
			"folder, no mode":  {Name: "a", FolderID: 7},
			"bad folder mode":  {Name: "a", FolderID: 7, FolderMode: "admin"},
		} {
			Expect(agents.Validate(a)).To(MatchError(agents.ErrInvalid), name)
		}
	})
})
