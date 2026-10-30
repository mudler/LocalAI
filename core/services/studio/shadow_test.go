package studio_test

import (
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// shadowingSandbox is the Sandbox as the broker lists it after this change:
// bash, which shadows the built-in bash, and status under its add-on name.
func shadowingSandbox() []studio.AddonTool {
	bash := addonTool("sandbox", "bash")
	bash.Action, bash.Runs, bash.Shadows = true, true, "bash"
	return []studio.AddonTool{bash, addonTool("sandbox", "status")}
}

var _ = Describe("an add-on tool that shadows a built-in", func() {
	It("takes the built-in's name, routes the call to the add-on by its own name and labels the result", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}
		svc, h := newAddonChat(fa, func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "run" {
				return []studiotest.ToolStep{{Tool: "bash", Args: `{"command":"ls"}`}, {Say: "done"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "run")
		idle(svc, alice, id)
		names := h.ToolNames(id)
		Expect(names).To(ContainElements("bash", "addon__sandbox__status"))
		Expect(names).NotTo(ContainElement("addon__sandbox__bash"))
		Expect(fa.calls).To(HaveLen(1))
		Expect(strings.Split(fa.calls[0], "|")[2]).To(Equal("addon__sandbox__bash"))
		tools := toolEntries(svc, alice, id)
		Expect(tools).To(HaveLen(1))
		Expect(tools[0].Card).NotTo(BeNil())
		Expect(tools[0].Card.Kind).To(Equal(studio.ProviderCardKind))
		Expect(tools[0].Card.ID).To(Equal("sandbox"))
	})

	It("keeps its add-on name when the name it shadows is not replaceable", func() {
		w := addonTool("files2", "write")
		w.Shadows = "write_file"
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {w}}}
		svc, h := newAddonChat(fa, func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} })
		id := create(svc, alice)
		sent(svc, alice, id, "hi")
		idle(svc, alice, id)
		Expect(h.ToolNames(id)).To(ContainElement("addon__files2__write"))
	})

	It("offers one of several add-ons that shadow one name: the pinned one", func() {
		a, b := shadowingSandbox()[0], shadowingSandbox()[0]
		b.Name, b.AddonID = "addon__other__bash", "other"
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {a, b}}}
		svc, h := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "run" {
				return []studiotest.ToolStep{{Tool: "bash", Args: `{"command":"ls"}`}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, func(d *studio.Deps) {
			d.AddonTools = fa
			d.ToolPins = func() map[string]string { return map[string]string{"bash": "sandbox"} }
		})
		id := create(svc, alice)
		sent(svc, alice, id, "run")
		idle(svc, alice, id)
		Expect(h.ToolNames(id)).To(ContainElement("bash"))
		Expect(fa.calls).To(HaveLen(1))
		Expect(fa.calls[0]).To(ContainSubstring("addon__sandbox__bash"))
	})

	It("tells the model that bash runs commands isolated", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}
		svc, h, _ := newCapsChat(isolatedAddons{fa, "isolated"}, nil)
		id := create(svc, alice)
		Expect(turnPrompt(svc, h, id)).To(ContainSubstring("Commands: bash runs commands and code in an isolated sandbox."))
	})
})
