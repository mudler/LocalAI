// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"os"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Host controls", func() {
	const bashSchema = `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`
	const fetchSchema = `{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`

	// runner is an add-on that runs commands, with the controls it asks for.
	// Any ID works: the host never looks at it.
	runner := func(id string, controls ...string) Manifest {
		m := Manifest{SchemaVersion: 1, ID: id, Version: "1.0.0", Executable: "bin/" + id, OS: "linux", Arch: "amd64",
			Host: VersionRange{Min: 1, Max: 1}, API: VersionRange{Min: 2, Max: 2}, Protocol: VersionRange{Min: 1, Max: 1}, DataFormat: VersionRange{Min: 1, Max: 1},
			Capabilities: Capabilities{Actions: true, Tools: []string{"bash"},
				ToolDefinitions: []ToolDefinition{{Name: "bash", Description: "Run a command.", Kind: ToolKindAction, InputSchema: json.RawMessage(bashSchema), Shadows: "bash"}},
				UI:              &UI{Entry: "ui/main.js", SDK: VersionRange{Min: 2, Max: 2}, Controls: controls}}}
		if slices.Contains(controls, ControlNetworkPolicy) {
			m.Capabilities.Network = &NetworkCapability{Kind: NetworkKindEgressCheck}
			m.Capabilities.HostOperations = &HostOperations{Optional: []string{"egress.check"}}
		}
		return m
	}
	host := HostCompatibility{Host: 1, API: 2, Protocol: 1, SDK: HostSDKVersion, DataFormat: 1, OS: "linux", Arch: "amd64"}

	It("accepts known controls and refuses an unknown or repeated one", func() {
		Expect(ValidateManifest(runner("sandbox", ControlNetworkPolicy, ControlToolProviders), host)).To(Succeed())
		Expect(ValidateManifest(runner("sandbox"), host)).To(Succeed())
		Expect(ValidateManifest(runner("sandbox", "settings-page"), host)).To(MatchError(ContainSubstring("host control")))
		Expect(ValidateManifest(runner("sandbox", ControlNetworkPolicy, ControlNetworkPolicy), host)).To(MatchError(ContainSubstring("host control")))
		many := make([]string, MaxControls+1)
		for i := range many {
			many[i] = ControlToolProviders
		}
		Expect(ValidateManifest(runner("sandbox", many...), host)).To(MatchError("too many host controls"))
	})

	It("reads ui.controls from a manifest and refuses an unknown field in it", func() {
		b, err := os.ReadFile("../../../tests/addons/fixtures/contracts/page.json")
		Expect(err).NotTo(HaveOccurred())
		var raw map[string]any
		Expect(json.Unmarshal(b, &raw)).To(Succeed())
		raw["capabilities"].(map[string]any)["ui"].(map[string]any)["controls"] = []string{ControlToolProviders}
		b, _ = json.Marshal(raw)
		m, err := ParseManifest(b)
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Capabilities.UI.Controls).To(Equal([]string{ControlToolProviders}))
	})

	It("draws the network policy only for an add-on granted egress that declares a network", func() {
		Expect(EntitledControls(runner("sandbox", ControlNetworkPolicy), []string{"workspace.", "egress."})).To(Equal([]string{ControlNetworkPolicy}))
		// A narrower grant that still covers the check is enough.
		Expect(EntitledControls(runner("sandbox", ControlNetworkPolicy), []string{"egress.check"})).To(Equal([]string{ControlNetworkPolicy}))
		Expect(EntitledControls(runner("sandbox", ControlNetworkPolicy), []string{"workspace."})).To(BeEmpty())
		Expect(EntitledControls(runner("sandbox", ControlNetworkPolicy), nil)).To(BeEmpty())
		m := runner("sandbox", ControlNetworkPolicy)
		m.Capabilities.Network = nil
		Expect(EntitledControls(m, []string{"egress."})).To(BeEmpty(), "a bash tool alone does not attach the control")
	})

	It("serves a second add-on with the same controls, by its grant and not by its name", func() {
		devbox := runner("devbox", ControlNetworkPolicy, ControlToolProviders)
		Expect(ValidateManifest(devbox, host)).To(Succeed())
		Expect(EntitledControls(devbox, []string{"egress."})).To(Equal([]string{ControlNetworkPolicy, ControlToolProviders}))
		Expect(EntitledControls(devbox, nil)).To(Equal([]string{ControlToolProviders}))
	})

	It("draws the tool providers for an add-on that replaces any built-in tool", func() {
		web := runner("web", ControlToolProviders, ControlNetworkPolicy)
		web.Capabilities.Actions = false
		web.Capabilities.Network, web.Capabilities.HostOperations = nil, nil
		web.Capabilities.Tools = []string{"fetch"}
		web.Capabilities.ToolDefinitions = []ToolDefinition{{Name: "fetch", Description: "Read a page.", Kind: ToolKindRead, InputSchema: json.RawMessage(fetchSchema), Shadows: "web_fetch"}}
		Expect(ValidateManifest(web, host)).To(Succeed())
		// The network policy needs a declared network, even with the grant.
		Expect(EntitledControls(web, []string{"egress."})).To(Equal([]string{ControlToolProviders}))
		web.Capabilities.ToolDefinitions[0].Shadows = ""
		Expect(EntitledControls(web, []string{"egress."})).To(BeEmpty())
	})

	It("draws the sandbox profiles only for an add-on whose commands tool declares profiles", func() {
		m := runner("sandbox", ControlSandboxProfiles)
		Expect(EntitledControls(m, nil)).To(BeEmpty(), "a bash tool without profiles does not attach the control")
		m.Capabilities.ToolDefinitions[0].Execution = &ExecutionDescriptor{Class: ExecutionClassCommands, Profiles: []ExecutionProfile{
			{ID: "base", Title: "Base", Description: "The usual tools.", Tools: []ProfileTool{{Name: "jq"}}}}}
		Expect(EntitledControls(m, nil)).To(Equal([]string{ControlSandboxProfiles}), "it needs no host grant: the policy is the host's own")
	})
})
