package agents_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connsets"
	"github.com/mudler/LocalAI/core/services/studio/resourcesets"
)

var _ = Describe("agent validation", func() {
	It("accepts a good agent and rejects every bad shape", func() {
		ok := agents.Agent{Name: "Reviewer", Addons: []string{"office"}}
		Expect(agents.Validate(ok)).To(Succeed())
		for _, n := range []string{"", "off", "registries", "all"} {
			Expect(agents.Validate(agents.Agent{Name: "a", Network: n})).To(Succeed(), n)
		}
		for _, m := range []string{"", "own", "mine_and_own", "mine", "none"} {
			Expect(agents.Validate(agents.Agent{Name: "a", Memory: m})).To(Succeed(), m)
		}
		for _, m := range []string{"", "always", "auto", "never"} {
			Expect(agents.Validate(agents.Agent{Name: "a", MemorySave: m})).To(Succeed(), m)
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
			"empty name":         {Name: "  "},
			"long name":          {Name: strings.Repeat("n", agents.MaxNameRunes+1)},
			"long desc":          {Name: "a", Description: strings.Repeat("d", agents.MaxDescription+1)},
			"instructions":       {Name: "a", Instructions: strings.Repeat("i", agents.MaxInstructions+1)},
			"model":              {Name: "a", Model: strings.Repeat("m", agents.MaxModelRunes+1)},
			"too many":           {Name: "a", Skills: many},
			"bad addon":          {Name: "a", Addons: []string{"Office Tools"}},
			"empty collection":   {Name: "a", Collections: []string{" "}},
			"long entry":         {Name: "a", MCPServers: []string{strings.Repeat("s", agents.MaxEntryRunes+1)}},
			"bad network":        {Name: "a", Network: "everything"},
			"bad memory":         {Name: "a", Memory: "everything"},
			"a made-up rule":     {Name: "a", MemorySave: "sometimes"},
			"ask is not offered": {Name: "a", MemorySave: "ask"},
			"folder, no mode":    {Name: "a", FolderID: 7},
			"bad folder mode":    {Name: "a", FolderID: 7, FolderMode: "admin"},
		} {
			Expect(agents.Validate(a)).To(MatchError(agents.ErrInvalid), name)
		}
	})

	It("limits the folder list", func() {
		list := func(n int) []agents.AgentFolder {
			var l []agents.AgentFolder
			for i := 1; i <= n; i++ {
				l = append(l, agents.AgentFolder{Node: int64(i), Mode: agents.FolderRead})
			}
			return l
		}
		Expect(agents.Validate(agents.Agent{Name: "a", Folders: list(agents.MaxFolders)})).To(Succeed())
		Expect(agents.Validate(agents.Agent{Name: "a", Folders: list(agents.MaxFolders + 1)})).To(MatchError(agents.ErrInvalid), "nine folders")
		dup := []agents.AgentFolder{{Node: 3, Mode: agents.FolderRead}, {Node: 3, Mode: agents.FolderWrite}}
		Expect(agents.Validate(agents.Agent{Name: "a", Folders: dup})).To(MatchError(agents.ErrInvalid), "same node twice")
		Expect(agents.Validate(agents.Agent{Name: "a", Folders: []agents.AgentFolder{{Node: 3}}})).To(MatchError(agents.ErrInvalid), "no mode")
		Expect(agents.Validate(agents.Agent{Name: "a", Folders: []agents.AgentFolder{{Node: 3, Mode: "all"}}})).To(MatchError(agents.ErrInvalid), "bad mode")
		unset := []agents.AgentFolder{{Mode: agents.FolderRead}, {Mode: agents.FolderRead}}
		Expect(agents.Validate(agents.Agent{Name: "a", Folders: unset})).To(Succeed(), "several unchosen rows")
	})
})

var _ = Describe("the connections of an agent", func() {
	base := func() agents.Agent { return agents.Agent{Name: "A", Model: "m"} }
	It("accepts an add-on ID with one or several binding IDs, and none", func() {
		a := base()
		Expect(agents.Validate(a)).To(Succeed())
		a.Connections = connsets.Sets{"tracker": {"b1"}}
		Expect(agents.Validate(a)).To(Succeed())
		a.Connections = connsets.Sets{"tracker": {"b1", "b2"}}
		Expect(agents.Validate(a)).To(Succeed())
	})
	DescribeTable("refuses",
		func(m connsets.Sets) {
			a := base()
			a.Connections = m
			Expect(agents.Validate(a)).To(MatchError(agents.ErrInvalid))
		},
		Entry("an add-on ID that is not one", connsets.Sets{"Not An Id": {"b1"}}),
		Entry("an empty binding ID", connsets.Sets{"tracker": {" "}}),
		Entry("an add-on with no binding", connsets.Sets{"tracker": {}}),
	)
})

var _ = Describe("the resources of an agent", func() {
	base := func() agents.Agent { return agents.Agent{Name: "A", Model: "m"} }
	It("accepts a list with the two modes, and none", func() {
		a := base()
		Expect(agents.Validate(a)).To(Succeed())
		a.Resources = resourcesets.Sets{"tables": {{ID: "db-a", Mode: "read"}, {ID: "db-b", Mode: "write"}}}
		Expect(agents.Validate(a)).To(Succeed())
	})
	DescribeTable("refuses",
		func(m resourcesets.Sets) {
			a := base()
			a.Resources = m
			Expect(agents.Validate(a)).To(MatchError(agents.ErrInvalid))
		},
		Entry("an add-on ID that is not one", resourcesets.Sets{"Not An Id": {{ID: "db-a", Mode: "read"}}}),
		Entry("a mode that is not read or write", resourcesets.Sets{"tables": {{ID: "db-a", Mode: "own"}}}),
		Entry("no mode", resourcesets.Sets{"tables": {{ID: "db-a"}}}),
		Entry("an ID that is not a resource ID", resourcesets.Sets{"tables": {{ID: "../x", Mode: "read"}}}),
		Entry("the same resource twice", resourcesets.Sets{"tables": {{ID: "db-a", Mode: "read"}, {ID: "db-a", Mode: "write"}}}),
	)
})

var _ = Describe("the sandbox profile of an agent", func() {
	It("is empty or a profile ID", func() {
		a := agents.Agent{Name: "A", Model: "m", SandboxProfile: "media"}
		Expect(agents.Validate(a)).To(Succeed())
		a.SandboxProfile = ""
		Expect(agents.Validate(a)).To(Succeed())
		a.SandboxProfile = "Media Pro"
		Expect(agents.Validate(a)).To(MatchError(agents.ErrInvalid))
	})
})
