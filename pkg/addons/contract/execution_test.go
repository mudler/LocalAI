// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The execution descriptor of a commands tool", func() {
	const schema = `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`
	manifestWith := func(e *ExecutionDescriptor) Manifest {
		m, err := ParseManifest(fixture())
		Expect(err).NotTo(HaveOccurred())
		m.Capabilities = Capabilities{
			Tools:   []string{"run"},
			Actions: true,
			ToolDefinitions: []ToolDefinition{{Name: "run", Description: "Run a command.", Kind: ToolKindAction,
				InputSchema: json.RawMessage(schema), Shadows: "bash", Execution: e}},
		}
		return m
	}
	host := func() HostCompatibility {
		h := compatible()
		h.SDK = 0
		return h
	}
	commands := &ExecutionDescriptor{Class: ExecutionClassCommands, Title: "Runbox", Note: "It has python3 and jq."}

	It("is valid for a tool that shadows bash", func() {
		Expect(ValidateManifest(manifestWith(commands), host())).To(Succeed())
	})
	It("is refused on a tool that does not shadow bash, with another class or with bad text", func() {
		m := manifestWith(commands)
		m.Capabilities.ToolDefinitions[0].Shadows = ""
		Expect(ValidateManifest(m, host())).NotTo(Succeed())
		for _, bad := range []*ExecutionDescriptor{
			{Class: "pages"},
			{Class: ExecutionClassCommands, Title: "<b>Runbox</b>"},
			{Class: ExecutionClassCommands, Note: string(make([]rune, MaxExecutionNoteChars+1))},
			{Class: ExecutionClassCommands, Skills: &ExecutionSkills{ProvidesRuntimeFor: []string{"games"}}},
		} {
			Expect(ValidateManifest(manifestWith(bad), host())).NotTo(Succeed())
		}
	})

	Describe("IsolatedSkills", func() {
		skills := []SkillDefinition{{Name: "docx", Shadows: "docx"}, {Name: "runbox-tips"}}
		defs := func(e *ExecutionDescriptor) []ToolDefinition {
			return manifestWith(e).Capabilities.ToolDefinitions
		}
		It("names the document skills of a provider that declares it supplies their runtime", func() {
			e := &ExecutionDescriptor{Class: ExecutionClassCommands, Skills: &ExecutionSkills{ProvidesRuntimeFor: []string{SkillKindDocuments}}}
			Expect(IsolatedSkills(defs(e), skills)).To(Equal([]string{"docx"}))
		})
		It("fails closed for a commands tool without a descriptor", func() {
			Expect(IsolatedSkills(defs(nil), skills)).To(Equal([]string{"docx"}))
		})
		It("names nothing for a descriptor that does not supply the runtime of documents", func() {
			Expect(IsolatedSkills(defs(commands), skills)).To(BeEmpty())
		})
		It("names nothing for an add-on without a commands tool", func() {
			Expect(IsolatedSkills(nil, skills)).To(BeEmpty())
		})
	})
	Describe("profiles", func() {
		base := ExecutionProfile{ID: "base", Title: "Base", Description: "The usual tools.", Tools: []ProfileTool{{Name: "jq", Check: []string{"jq --version"}}, {Name: "python3", Detail: "pandas"}}}
		media := ExecutionProfile{ID: "media", Title: "Media", Description: "Base plus ffmpeg.", MemoryMiB: 4096, Tools: []ProfileTool{{Name: "ffmpeg", Check: []string{"ffmpeg -version"}}}}
		with := func(ps ...ExecutionProfile) *ExecutionDescriptor {
			return &ExecutionDescriptor{Class: ExecutionClassCommands, Profiles: ps}
		}
		It("accepts a fallback first and other profiles after it", func() {
			Expect(ValidateManifest(manifestWith(with(base, media)), host())).To(Succeed())
		})
		It("reads a profile by ID and lists its tools as text", func() {
			e := with(base, media)
			p, ok := e.ProfileByID("base")
			Expect(ok).To(BeTrue())
			Expect(p.ToolList()).To(Equal("jq, python3 (pandas)"))
			_, ok = e.ProfileByID("nope")
			Expect(ok).To(BeFalse())
		})
		It("refuses a descriptor whose first profile is not the fallback", func() {
			Expect(ValidateManifest(manifestWith(with(media, base)), host())).NotTo(Succeed())
		})
		It("refuses a repeated profile, a bad ID, bad text, a bad limit, no tools and a bad check", func() {
			bad := func(f func(p *ExecutionProfile)) {
				p := media
				p.Tools = append([]ProfileTool(nil), media.Tools...)
				f(&p)
				ExpectWithOffset(1, ValidateManifest(manifestWith(with(base, p)), host())).NotTo(Succeed())
			}
			bad(func(p *ExecutionProfile) { p.ID = "base" })
			bad(func(p *ExecutionProfile) { p.ID = "Media Pro" })
			bad(func(p *ExecutionProfile) { p.Title = "<b>x</b>" })
			bad(func(p *ExecutionProfile) { p.Description = "" })
			bad(func(p *ExecutionProfile) { p.CPUs = -1 })
			bad(func(p *ExecutionProfile) { p.MemoryMiB = MaxProfileMemoryMiB + 1 })
			bad(func(p *ExecutionProfile) { p.Tools = nil })
			bad(func(p *ExecutionProfile) { p.Tools = append(p.Tools, ProfileTool{Name: "ffmpeg"}) })
			bad(func(p *ExecutionProfile) { p.Tools[0].Check = []string{"ffmpeg; rm -rf /"} })
			bad(func(p *ExecutionProfile) { p.Tools[0].Check = []string{"$(id)"} })
		})
		It("refuses too many profiles", func() {
			ps := []ExecutionProfile{base}
			for i := 0; i < MaxExecutionProfiles; i++ {
				p := media
				p.ID = "p" + string(rune('a'+i))
				ps = append(ps, p)
			}
			Expect(ValidateManifest(manifestWith(with(ps...)), host())).NotTo(Succeed())
		})
	})
})
