// SPDX-License-Identifier: MIT
package hostslots_test

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func host() contract.HostCompatibility {
	return contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}
}

// manifest builds a manifest with the given ui object. Tools are ping and list.
func manifest(ui string) []byte {
	return []byte(`{"schemaVersion":1,"id":"notes","version":"1.0.0","executable":"bin/x","os":"linux","arch":"amd64",` +
		`"host":{"min":1,"max":2},"api":{"min":1,"max":1},"protocol":{"min":1,"max":1},"dataFormat":{"min":1,"max":2},` +
		`"capabilities":{"tools":["ping","list"],"ui":` + ui + `}}`)
}

const base = `"entry":"ui.js","sdk":{"min":1,"max":1},"icon":"ui/icon.png","pages":["home"]`

func validate(ui string) error {
	m, e := contract.ParseManifest(manifest(ui))
	if e != nil {
		return e
	}
	return contract.ValidateManifest(m, host())
}

var _ = Describe("Host UI contributions in the manifest", func() {
	It("accepts tool and component chat actions, slots and own-tool renderers", func() {
		ui := `{` + base + `,"chatActions":[
			{"id":"summarize","label":"Summarize","icon":"sparkles","placement":"composer","target":{"tool":"ping"}},
			{"id":"inspect","label":{"en":"Inspect","it":"Ispeziona"},"icon":"ui/icon.png","placement":"message","target":{"component":"InspectPanel"}}],
			"slots":[{"id":"nav","slot":"app.nav","component":"NavBit"},{"id":"set","slot":"settings.sections","component":"SettingsBit"},
			{"id":"cx","slot":"chat.composer.actions","component":"ComposerBit"},{"id":"mx","slot":"chat.message.actions","component":"MessageBit"}],
			"renderers":[{"kind":"approval-card","tool":"ping","component":"PingCard"},{"kind":"tool-result","tool":"list","component":"ListView"}]}`
		Expect(validate(ui)).To(Succeed())
		m, _ := contract.ParseManifest(manifest(ui))
		Expect(m.Capabilities.UI.ChatActions).To(HaveLen(2))
		Expect(m.Capabilities.UI.ChatActions[1].Label.ByLanguage).To(HaveKeyWithValue("it", "Ispeziona"))
		// The canonical form is stable.
		out, e := json.Marshal(m)
		Expect(e).NotTo(HaveOccurred())
		again, e := contract.ParseManifest(out)
		Expect(e).NotTo(HaveOccurred())
		Expect(again).To(Equal(m))
	})

	It("keeps bare chat action identifiers and every earlier fixture valid", func() {
		Expect(validate(`{` + base + `,"chatActions":["open","summarize"]}`)).To(Succeed())
		for _, name := range []string{"service", "headless", "page", "chat", "app"} {
			b, e := os.ReadFile("../fixtures/contracts/" + name + ".json")
			Expect(e).NotTo(HaveOccurred())
			m, e := contract.ParseManifest(b)
			Expect(e).NotTo(HaveOccurred())
			Expect(contract.ValidateManifest(m, host())).To(Succeed(), name)
		}
		for _, dir := range []string{"bare", "cssb", "uifixture", "uifixture-2"} {
			b, e := os.ReadFile("../fixture/ui/" + dir + "/manifest.json")
			Expect(e).NotTo(HaveOccurred())
			m, e := contract.ParseManifest(b)
			Expect(e).NotTo(HaveOccurred())
			Expect(contract.ValidateManifest(m, host())).To(Succeed(), dir)
		}
	})

	It("rejects every undeclared, unsafe or inconsistent contribution at install", func() {
		action := func(body string) string {
			return `{` + base + `,"chatActions":[` + body + `]}`
		}
		bad := map[string]string{
			"unknown slot":         `{` + base + `,"slots":[{"id":"a","slot":"chat.footer","component":"Bit"}]}`,
			"unknown placement":    action(`{"id":"a","label":"A","icon":"star","placement":"sidebar","target":{"tool":"ping"}}`),
			"missing placement":    action(`{"id":"a","label":"A","icon":"star","target":{"tool":"ping"}}`),
			"remote icon":          action(`{"id":"a","label":"A","icon":"https://evil.example/i.png","placement":"composer","target":{"tool":"ping"}}`),
			"absolute icon":        action(`{"id":"a","label":"A","icon":"/etc/icon.png","placement":"composer","target":{"tool":"ping"}}`),
			"unknown icon name":    action(`{"id":"a","label":"A","icon":"skull","placement":"composer","target":{"tool":"ping"}}`),
			"other packaged icon":  action(`{"id":"a","label":"A","icon":"ui/other.png","placement":"composer","target":{"tool":"ping"}}`),
			"undeclared tool":      action(`{"id":"a","label":"A","icon":"star","placement":"composer","target":{"tool":"delete-everything"}}`),
			"both targets":         action(`{"id":"a","label":"A","icon":"star","placement":"composer","target":{"tool":"ping","component":"Panel"}}`),
			"no target":            action(`{"id":"a","label":"A","icon":"star","placement":"composer","target":{}}`),
			"duplicate ids":        action(`{"id":"a","label":"A","icon":"star","placement":"composer","target":{"tool":"ping"}},{"id":"a","label":"B","icon":"star","placement":"message","target":{"tool":"ping"}}`),
			"markup label":         action(`{"id":"a","label":"<b>A</b>","icon":"star","placement":"composer","target":{"tool":"ping"}}`),
			"long label":           action(`{"id":"a","label":"` + strings.Repeat("x", 61) + `","icon":"star","placement":"composer","target":{"tool":"ping"}}`),
			"unsafe component":     action(`{"id":"a","label":"A","icon":"star","placement":"message","target":{"component":"__proto__"}}`),
			"dotted component":     action(`{"id":"a","label":"A","icon":"star","placement":"message","target":{"component":"A.b"}}`),
			"unknown field":        action(`{"id":"a","label":"A","icon":"star","placement":"composer","target":{"tool":"ping"},"onClick":"x"}`),
			"slot id clashes":      `{` + base + `,"chatActions":["a"],"slots":[{"id":"a","slot":"app.nav","component":"Bit"}]}`,
			"duplicate slot id":    `{` + base + `,"slots":[{"id":"a","slot":"app.nav","component":"Bit"},{"id":"a","slot":"settings.sections","component":"Bit"}]}`,
			"lowercase component":  `{` + base + `,"slots":[{"id":"a","slot":"app.nav","component":"constructor"}]}`,
			"renderer foreign":     `{` + base + `,"renderers":[{"kind":"approval-card","tool":"other-addon-tool","component":"Card"}]}`,
			"renderer kind":        `{` + base + `,"renderers":[{"kind":"banner","tool":"ping","component":"Card"}]}`,
			"renderer duplicate":   `{` + base + `,"renderers":[{"kind":"tool-result","tool":"ping","component":"A"},{"kind":"tool-result","tool":"ping","component":"B"}]}`,
			"too many actions":     `{` + base + `,"chatActions":[` + manyActions(17) + `]}`,
			"too many slots":       `{` + base + `,"slots":[` + manySlots(17) + `]}`,
			"slot without id":      `{` + base + `,"slots":[{"slot":"app.nav","component":"Bit"}]}`,
			"slot unknown field":   `{` + base + `,"slots":[{"id":"a","slot":"app.nav","component":"Bit","props":{}}]}`,
			"renderer unknown key": `{` + base + `,"renderers":[{"kind":"tool-result","tool":"ping","component":"A","style":"x"}]}`,
		}
		for name, ui := range bad {
			Expect(validate(ui)).NotTo(Succeed(), name)
		}
		// A valid control for each family, so the table above cannot pass by
		// failing for an unrelated reason.
		Expect(validate(action(`{"id":"a","label":"A","icon":"star","placement":"composer","target":{"tool":"ping"}}`))).To(Succeed())
		Expect(validate(`{` + base + `,"slots":[{"id":"a","slot":"app.nav","component":"Bit"}]}`)).To(Succeed())
		Expect(validate(`{` + base + `,"renderers":[{"kind":"tool-result","tool":"ping","component":"A"}]}`)).To(Succeed())
	})

	It("keeps the closed slot list and the icon allowlist equal to the host", func() {
		Expect(contract.HostSlots).To(Equal([]string{"chat.composer.actions", "chat.message.actions", "app.nav", "settings.sections"}))
		src, e := os.ReadFile("../../../core/http/react-ui/src/addons/sdk.js")
		Expect(e).NotTo(HaveOccurred())
		block := regexp.MustCompile(`(?s)ICON_NAMES = Object\.freeze\(\[(.*?)\]\)`).FindSubmatch(src)
		Expect(block).NotTo(BeNil())
		var names []string
		for _, m := range regexp.MustCompile(`'([a-z-]+)'`).FindAllSubmatch(block[1], -1) {
			names = append(names, string(m[1]))
		}
		Expect(slices.Equal(names, contract.IconNames)).To(BeTrue(), "sdk.js ICON_NAMES and contract.IconNames differ")
		slots, e := os.ReadFile("../../../core/http/react-ui/src/addons/slots.js")
		Expect(e).NotTo(HaveOccurred())
		for _, id := range contract.HostSlots {
			Expect(string(slots)).To(ContainSubstring("'" + id + "'"))
		}
	})
})

func manyActions(n int) string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, `{"id":"a`+string(rune('a'+i))+`","label":"A","icon":"star","placement":"composer","target":{"tool":"ping"}}`)
	}
	return strings.Join(out, ",")
}

func manySlots(n int) string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, `{"id":"s`+string(rune('a'+i))+`","slot":"app.nav","component":"Bit"}`)
	}
	return strings.Join(out, ",")
}

var _ = Describe("The apps list carries the declared contributions", func() {
	snapshot := func(enabled bool, ui string) apps.Snapshot {
		m, e := contract.ParseManifest(manifest(ui))
		Expect(e).NotTo(HaveOccurred())
		return apps.Snapshot{AddonID: "notes", Installed: true, Enabled: enabled, Generation: 2, State: "running",
			Receipt: packages.PackageReceipt{Digest: "sha256:" + strings.Repeat("ab", 32), Manifest: m,
				Icon: &packages.PackageIcon{Path: "ui/icon.png"}}}
	}
	ui := `{` + base + `,"chatActions":["legacy",
		{"id":"summarize","label":{"en":"Summarize","it":"Riassumi"},"icon":"sparkles","placement":"composer","target":{"tool":"ping"}},
		{"id":"inspect","label":"Inspect","icon":"ui/icon.png","placement":"message","target":{"component":"InspectPanel"}}],
		"slots":[{"id":"set","slot":"settings.sections","component":"SettingsBit"}],
		"renderers":[{"kind":"approval-card","tool":"ping","component":"PingCard"}]}`

	It("lists actions, slots and renderers for an enabled addon and keeps the identifiers", func() {
		a := apps.Build([]apps.Snapshot{snapshot(true, ui)}, false)[0]
		Expect(a.UI).NotTo(BeNil())
		Expect(a.UI.ChatActions).To(Equal([]string{"legacy", "summarize", "inspect"}))
		Expect(a.UI.Actions).To(Equal([]apps.ChatActionView{
			{ID: "summarize", Label: "Summarize", Labels: map[string]string{"en": "Summarize", "it": "Riassumi"}, Icon: "sparkles", Placement: "composer", Tool: "ping"},
			{ID: "inspect", Label: "Inspect", PackagedIcon: true, Placement: "message", Component: "InspectPanel"},
		}))
		Expect(a.UI.Slots).To(Equal([]apps.SlotView{{ID: "set", Slot: "settings.sections", Component: "SettingsBit"}}))
		Expect(a.UI.Renderers).To(Equal([]contract.Renderer{{Kind: "approval-card", Tool: "ping", Component: "PingCard"}}))
		b, e := json.Marshal(a)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"slot":"settings.sections"`))
	})

	It("lists no contribution for a disabled addon, whoever asks", func() {
		for _, admin := range []bool{false, true} {
			for _, a := range apps.Build([]apps.Snapshot{snapshot(false, ui)}, admin) {
				Expect(a.UI).To(BeNil())
			}
		}
	})
})
