package studio_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The administrator's network view says which add-on runs the commands. It
// asks the broker each time, so it is right before any chat has listed its
// tools (a fresh frontend, or one of several) and it forgets a removed add-on.
var _ = Describe("the add-on that the network settings apply to", func() {
	runner := func(id, installed string) studio.AddonTool {
		t := addonTool(id, "bash")
		t.Action, t.Shadows, t.InstalledAt = true, "bash", installed
		return t
	}
	service := func(fa *fakeAddons, list []studio.AddonShadower, pins map[string]string) *studio.Service {
		svc, _ := newToolService(func(workspace.Actor, string) []studiotest.ToolStep { return nil }, func(d *studio.Deps) {
			d.AddonTools = listedAddons{fa, list}
			d.ToolPins = func() map[string]string { return pins }
		})
		return svc
	}
	sandboxOf := func(svc *studio.Service) string {
		v, err := svc.AdminEgress(context.Background(), alice)
		Expect(err).NotTo(HaveOccurred())
		return v.Sandbox
	}

	It("is the running add-on that replaces bash, before any chat ran", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {runner("sandbox", "2026-01-01T00:00:00Z"), addonTool("notes", "read")}}}
		svc := service(fa, nil, nil)
		Expect(sandboxOf(svc)).To(Equal("sandbox"))

		// Removed: the broker no longer lists it, and neither does the view.
		fa.tools[alice] = nil
		Expect(sandboxOf(svc)).To(BeEmpty())
	})

	It("follows the pin, and is none when the pinned add-on is stopped", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {runner("sandbox", "2026-01-01T00:00:00Z"), runner("other", "2026-02-01T00:00:00Z")}}}
		svc := service(fa, nil, map[string]string{"bash": "other"})
		Expect(sandboxOf(svc)).To(Equal("other"))

		fa.tools[alice] = []studio.AddonTool{runner("sandbox", "2026-01-01T00:00:00Z")}
		stopped := []studio.AddonShadower{
			{AddonID: "sandbox", Tools: []string{"bash"}, Running: true},
			{AddonID: "other", Tools: []string{"bash"}},
		}
		svc = service(fa, stopped, map[string]string{"bash": "other"})
		Expect(sandboxOf(svc)).To(BeEmpty())
	})

	It("tells a chat that switched the add-on off from one with no add-on, with no chat having run first", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {runner("sandbox", "")}}}
		svc := service(fa, []studio.AddonShadower{{AddonID: "sandbox", Tools: []string{"bash"}, Running: true}}, nil)
		id := create(svc, alice)
		Expect(svc.SetAddonsOff(context.Background(), alice, id, []string{"sandbox"})).To(Succeed())
		c, err := svc.CommandRunner(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.State).To(Equal(studio.CommandsOff))
		Expect(c.Addon).To(Equal("sandbox"))
	})
})
