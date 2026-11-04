// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"

	"github.com/mudler/LocalAI/addons/home-assistant/hatest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the policy tools of the page", func() {
	var (
		srv *hatest.Server
		g   *rig
	)
	BeforeEach(func() {
		srv = home()
		DeferCleanup(srv.Close)
		g = newRig(srv, GinkgoT().TempDir())
	})

	It("shows the policy and the hard exclusions to anyone, as editable only to an administrator", func() {
		d, err := g.read("get-policy", map[string]any{})
		Expect(err).NotTo(HaveOccurred())
		Expect(d["admin"]).To(BeFalse())
		Expect(d["policy"].(map[string]any)["allow"]).To(ContainElement("light.turn_on"))
		hard := d["hard"].(map[string]any)
		Expect(hard["domains"]).To(ContainElements("lock", "alarm_control_panel", "siren", "valve", "water_heater", "hassio", "shell_command", "python_script", "homeassistant"))
		Expect(hard["servicePrefixes"]).To(ContainElement("reload"))
		Expect(d["physical"]).To(ContainElements("climate", "scene", "script"))
		g.admin = true
		d, _ = g.read("get-policy", map[string]any{})
		Expect(d["admin"]).To(BeTrue())
	})

	It("lets only an administrator change it, and says so", func() {
		_, err := g.read("set-policy", map[string]any{"maxTargets": 3})
		Expect(err).To(MatchError(ContainSubstring("only an administrator")))
		g.admin = true
		d, err := g.read("set-policy", map[string]any{"maxTargets": 3, "excludeAreas": []string{"Bathroom"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(d["saved"]).To(BeTrue())
		p, _ := g.pol.Load()
		Expect(p.MaxTargets).To(Equal(3))
		Expect(p.ExcludeAreas).To(Equal([]string{"Bathroom"}))
		Expect(p.Allow).To(ContainElement("light.turn_on"), "a field that was not named stays")
	})

	It("refuses a hard-excluded entry and saves nothing", func() {
		g.admin = true
		for _, e := range []string{"lock.unlock", "lock.*", "alarm_control_panel.alarm_disarm", "homeassistant.restart", "shell_command.x", "light.reload"} {
			_, err := g.read("set-policy", map[string]any{"allow": []string{"light.turn_on", e}})
			Expect(err).To(MatchError(ContainSubstring("can never be allowed")), e)
		}
		p, _ := g.pol.Load()
		Expect(p.Allow).To(ContainElement("light.toggle"))
	})

	It("refuses a pattern that does not compile and an area name with markup", func() {
		g.admin = true
		_, err := g.read("set-policy", map[string]any{"excludePatterns": []string{"(("}})
		Expect(err).To(HaveOccurred())
		_, err = g.read("set-policy", map[string]any{"excludeAreas": []string{`x" %}{{ 1 }}`}})
		Expect(err).To(HaveOccurred())
	})

	It("takes pins as host:port=fingerprint and applies them to the next call", func() {
		g.admin = true
		short := "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
		_, err := g.read("set-policy", map[string]any{"pins": []string{"Ha.Example:8123=" + short}})
		Expect(err).To(HaveOccurred(), "16 bytes is not a SHA-256")
		colons := short + ":" + short
		_, err = g.read("set-policy", map[string]any{"pins": []string{"ha.example:8123=" + colons}})
		Expect(err).NotTo(HaveOccurred(), "openssl prints the digits with colons")
		fp64 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		d, err := g.read("set-policy", map[string]any{"pins": []string{"Ha.Example:8123=" + fp64}})
		Expect(err).NotTo(HaveOccurred())
		Expect(d["policy"].(map[string]any)["pins"]).To(Equal([]any{"ha.example:8123=" + fp64}))
		_, err = g.read("set-policy", map[string]any{"pins": []string{"nonsense"}})
		Expect(err).To(HaveOccurred())
	})

	It("reports a damaged file instead of hiding it", func() {
		dir := GinkgoT().TempDir()
		g2 := newRig(srv, dir)
		Expect(writeFile(dir, `{`)).To(Succeed())
		d, err := g2.read("get-policy", map[string]any{})
		Expect(err).NotTo(HaveOccurred())
		Expect(d["broken"]).To(BeTrue())
		g2.admin = true
		_, err = g2.read("set-policy", map[string]any{"maxTargets": 4})
		Expect(err).NotTo(HaveOccurred(), "an administrator can repair it from the page")
		d, _ = g2.read("get-policy", map[string]any{})
		Expect(d["broken"]).To(BeFalse())
	})

	It("does not call Home Assistant for the policy", func() {
		_, _ = g.read("get-policy", map[string]any{})
		Expect(srv.Calls()).To(BeEmpty())
		raw, _ := json.Marshal(map[string]any{"tool": "get-policy"})
		_, err := g.rt.Read(context.Background(), envWithArgs(g, raw))
		Expect(err).NotTo(HaveOccurred())
	})
})
