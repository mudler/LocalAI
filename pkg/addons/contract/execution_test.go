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
})
