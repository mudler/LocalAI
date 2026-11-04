package studio_test

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// networkAddons is a broker that also says which network each add-on declares.
type networkAddons struct {
	controlledAddons
	networks map[string]studio.AddonNetwork
}

func (n networkAddons) Network(_ context.Context, _, addonID string) (studio.AddonNetwork, bool) {
	v, ok := n.networks[addonID]
	return v, ok
}

// A second add-on that runs commands, with another ID and title than the
// shipped Sandbox, and that declares its network: the host draws the same
// control and builds the same text from the declaration.
var _ = Describe("the declared network of the add-on that runs the commands", func() {
	// runner is an add-on with a commands tool whose descriptor has title.
	runner := func(id, title string) []studio.AddonTool {
		bash := addonTool(id, "bash")
		bash.Action, bash.Shadows = true, "bash"
		bash.Execution = &contract.ExecutionDescriptor{Class: contract.ExecutionClassCommands, Title: title}
		return []studio.AddonTool{bash}
	}
	runbox := studio.AddonNetwork{Title: "Runbox", Presets: []contract.NetworkPreset{{ID: "off", Title: "Closed"}}}

	service := func(id, title string, networks map[string]studio.AddonNetwork, settings egress.Settings) (*studio.Service, *studiotest.ToolHarness) {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: runner(id, title)}}
		f := newFiles()
		return newToolService(func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }, func(d *studio.Deps) {
			d.Files = f
			d.AddonTools = networkAddons{controlledAddons{isolatedAddons{fa, "isolated"}, map[string][]string{id: {"network-policy"}}}, networks}
			d.EgressSettings = func(context.Context) (egress.Settings, error) { return settings, nil }
		})
	}
	turn := func(svc *studio.Service, h *studiotest.ToolHarness) string {
		id := create(svc, alice)
		sent(svc, alice, id, "hello")
		idle(svc, alice, id)
		return h.TurnPrompt(id) + "\n" + strings.Join(h.Sent(id), "\n")
	}

	It("gives the chat the declared title and preset titles for its row", func() {
		svc, _ := service("devbox", "Runbox", map[string]studio.AddonNetwork{"devbox": runbox}, egress.Settings{Ceiling: egress.PresetAll})
		v, err := svc.Egress(context.Background(), alice, create(svc, alice))
		Expect(err).NotTo(HaveOccurred())
		j := v.JSON()
		Expect(j.Addon).To(Equal("devbox"))
		Expect(j.Network).NotTo(BeNil())
		Expect(j.Network.Title).To(Equal("Runbox"))
		Expect(j.Network.Presets).To(ConsistOf(HaveField("Title", "Closed")))
	})

	It("falls back to the title of the commands tool for an add-on that declares no network", func() {
		svc, _ := service("sandbox", "Sandbox", nil, egress.Settings{Ceiling: egress.PresetAll})
		v, _ := svc.Egress(context.Background(), alice, create(svc, alice))
		Expect(v.JSON().Addon).To(Equal("sandbox"))
		Expect(v.JSON().Network.Title).To(Equal("Sandbox"))
	})

	It("writes the declared title in the line the model reads and names no other add-on", func() {
		svc, h := service("devbox", "Runbox", map[string]studio.AddonNetwork{"devbox": runbox}, egress.Settings{Ceiling: egress.PresetOff})
		got := turn(svc, h)
		Expect(got).To(ContainSubstring("The server's setting for the Runbox network"))
		Expect(strings.ToLower(got)).NotTo(ContainSubstring("sandbox"))

		svc, h = service("devbox", "Runbox", map[string]studio.AddonNetwork{"devbox": runbox}, egress.Settings{Ceiling: egress.PresetAll, Default: egress.PresetAll})
		got = turn(svc, h)
		Expect(got).To(ContainSubstring("through the proxy that Runbox sets"))
		Expect(got).To(ContainSubstring("curl, pip and git work in Runbox through the proxy"))
		Expect(strings.ToLower(got)).NotTo(ContainSubstring("sandbox"))
	})

	It("writes the same line for the shipped Sandbox from its own declaration", func() {
		svc, h := service("sandbox", "Sandbox", map[string]studio.AddonNetwork{"sandbox": {Title: "Sandbox"}}, egress.Settings{Ceiling: egress.PresetOff})
		Expect(turn(svc, h)).To(ContainSubstring("The server's setting for the Sandbox network"))
	})

	It("tells the administrator view which network the settings apply to", func() {
		svc, _ := service("devbox", "Runbox", map[string]studio.AddonNetwork{"devbox": runbox}, egress.Settings{Ceiling: egress.PresetAll})
		a, err := svc.AdminEgress(context.Background(), alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(a.JSON().Network.Title).To(Equal("Runbox"))
		Expect(a.JSON().Sandbox).To(Equal("devbox"), "the field keeps its name for existing clients")
	})
})
