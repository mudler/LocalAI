// SPDX-License-Identifier: MIT
package policy_test

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/home-assistant/internal/policy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the policy", func() {
	It("ships a small default allowlist", func() {
		d := policy.Defaults()
		Expect(d.Validate()).To(Succeed())
		Expect(d.Allow).To(ConsistOf(
			"light.turn_on", "light.turn_off", "light.toggle", "switch.turn_on", "switch.turn_off", "switch.toggle",
			"climate.set_temperature", "climate.set_hvac_mode",
			"media_player.media_play", "media_player.media_pause", "media_player.media_play_pause", "media_player.media_stop",
			"media_player.media_next_track", "media_player.media_previous_track",
			"media_player.volume_up", "media_player.volume_down", "media_player.volume_set", "media_player.volume_mute",
			"scene.turn_on", "script.turn_on", "input_boolean.*"))
		Expect(d.ExcludeDomains).To(ConsistOf("lock", "alarm_control_panel", "camera"))
		Expect(d.MaxTargets).To(Equal(5))
		Expect(d.ExcludeLabel).To(Equal("sensitive"))
		for _, e := range []string{"notify.notify", "cover.open_cover", "lock.unlock"} {
			d2, s2, _ := policy.ParseAllow(e)
			Expect(d.ServiceAllowed(d2, s2)).To(BeFalse(), e)
		}
	})

	DescribeTable("hard exclusions that no allowlist lifts",
		func(domain, service string) {
			Expect(policy.HardService(domain, service)).To(BeTrue())
			p := policy.Defaults()
			p.Allow = append(p.Allow, domain+"."+service)
			Expect(p.Validate()).To(MatchError(ContainSubstring("can never be allowed")))
			// A file edited by hand does not widen it either.
			p.Allow = []string{domain + "." + service}
			Expect(p.ServiceAllowed(domain, service)).To(BeFalse())
		},
		Entry("unlock", "lock", "unlock"),
		Entry("open a lock", "lock", "open"),
		Entry("disarm", "alarm_control_panel", "alarm_disarm"),
		Entry("siren", "siren", "turn_on"),
		Entry("valve", "valve", "open_valve"),
		Entry("water heater", "water_heater", "set_temperature"),
		Entry("camera", "camera", "snapshot"),
		Entry("restart", "homeassistant", "restart"),
		Entry("stop", "homeassistant", "stop"),
		Entry("any homeassistant service", "homeassistant", "turn_on"),
		Entry("supervisor", "hassio", "addon_start"),
		Entry("shell command", "shell_command", "anything"),
		Entry("python script", "python_script", "anything"),
		Entry("mqtt publish", "mqtt", "publish"),
		Entry("reload of a domain", "automation", "reload"),
		Entry("reload of a config entry", "light", "reload_config_entry"),
		Entry("restart in any domain", "light", "restart"),
		Entry("firmware update", "update", "install"),
	)

	It("refuses domain.* for a hard domain and lets a wildcard skip the reload services", func() {
		p := policy.Defaults()
		p.Allow = []string{"lock.*"}
		Expect(p.Validate()).To(HaveOccurred())
		p.Allow = []string{"input_boolean.*"}
		Expect(p.Validate()).To(Succeed())
		Expect(p.ServiceAllowed("input_boolean", "turn_on")).To(BeTrue())
		Expect(p.ServiceAllowed("input_boolean", "reload")).To(BeFalse())
		Expect(p.ServiceAllowed("input_boolean", "set_datetime")).To(BeTrue())
	})

	It("treats only lights, switches, input booleans and media transport as quiet", func() {
		for _, e := range [][2]string{{"light", "turn_on"}, {"switch", "toggle"}, {"input_boolean", "turn_off"}, {"media_player", "media_pause"}} {
			Expect(policy.Physical(e[0], e[1])).To(BeFalse(), e[0]+"."+e[1])
		}
		for _, e := range [][2]string{{"climate", "set_temperature"}, {"cover", "open_cover"}, {"script", "turn_on"}, {"scene", "turn_on"}, {"notify", "mobile_app_phone"}, {"media_player", "play_media"}, {"fan", "turn_on"}} {
			Expect(policy.Physical(e[0], e[1])).To(BeTrue(), e[0]+"."+e[1])
		}
	})

	It("moves only covers of a safe device class", func() {
		Expect(policy.HardCover("garage")).To(BeTrue())
		Expect(policy.HardCover("gate")).To(BeTrue())
		Expect(policy.HardCover("door")).To(BeTrue())
		Expect(policy.HardCover("")).To(BeTrue())
		Expect(policy.HardCover("blind")).To(BeFalse())
		Expect(policy.HardCover("Curtain")).To(BeFalse())
	})

	It("validates shapes and sizes", func() {
		bad := []func(*policy.Policy){
			func(p *policy.Policy) { p.Allow = []string{"light"} },
			func(p *policy.Policy) { p.Allow = []string{"*.*"} },
			func(p *policy.Policy) { p.ExcludePatterns = []string{"("} },
			func(p *policy.Policy) { p.ExcludeAreas = []string{`Kitchen" %}`} },
			func(p *policy.Policy) { p.ExcludeLabel = "a{{b" },
			func(p *policy.Policy) { p.MaxTargets = 0 },
			func(p *policy.Policy) { p.MaxTargets = 21 },
			func(p *policy.Policy) { p.Pins = map[string]string{"ha.example:8123": "abc"} },
			func(p *policy.Policy) { p.Pins = map[string]string{"nohostport": make64()} },
		}
		for i, f := range bad {
			p := policy.Defaults()
			f(&p)
			Expect(p.Validate()).To(HaveOccurred(), "case %d", i)
		}
		p := policy.Defaults()
		p.Pins = map[string]string{"ha.example:8123": make64()}
		Expect(p.Validate()).To(Succeed())
	})

	It("matches the exclusion domains and patterns on the whole entity ID", func() {
		p := policy.Defaults()
		p.ExcludePatterns = []string{`sensor\..*_bedroom`, `light\.guest`}
		c, err := p.Compile()
		Expect(err).NotTo(HaveOccurred())
		Expect(c.EntityExcluded("lock.front")).To(BeTrue())
		Expect(c.EntityExcluded("camera.porch")).To(BeTrue())
		Expect(c.EntityExcluded("sensor.temp_bedroom")).To(BeTrue())
		Expect(c.EntityExcluded("light.guest")).To(BeTrue())
		Expect(c.EntityExcluded("light.guest_lamp")).To(BeFalse(), "a pattern matches the whole ID")
		Expect(c.EntityExcluded("light.kitchen")).To(BeFalse())
	})

	It("saves to the data folder and reads it back, with owner-only mode", func() {
		dir := GinkgoT().TempDir()
		s := policy.Open(dir)
		got, err := s.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Allow).To(Equal(policy.Defaults().Normalize().Allow))
		p := policy.Defaults()
		p.Allow = []string{"light.turn_on"}
		p.ExcludeAreas = []string{"Bathroom"}
		_, err = s.Save(p)
		Expect(err).NotTo(HaveOccurred())
		got, err = policy.Open(dir).Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Allow).To(Equal([]string{"light.turn_on"}))
		Expect(got.ExcludeAreas).To(Equal([]string{"Bathroom"}))
		st, err := os.Stat(filepath.Join(dir, "policy.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("never saves an invalid policy, and does not fall back when the file is damaged", func() {
		dir := GinkgoT().TempDir()
		s := policy.Open(dir)
		p := policy.Defaults()
		p.Allow = []string{"lock.unlock"}
		_, err := s.Save(p)
		Expect(err).To(HaveOccurred())
		Expect(filepath.Join(dir, "policy.json")).NotTo(BeAnExistingFile())
		Expect(os.WriteFile(filepath.Join(dir, "policy.json"), []byte(`{"allow":["lock.unlock"],"maxTargets":5}`), 0o600)).To(Succeed())
		_, err = s.Load()
		Expect(err).To(HaveOccurred(), "a file with a hard-excluded entry is not a policy")
		Expect(os.WriteFile(filepath.Join(dir, "policy.json"), []byte(`{`), 0o600)).To(Succeed())
		_, err = s.Load()
		Expect(err).To(HaveOccurred())
	})

	It("changes its hash when the policy changes", func() {
		a := policy.Defaults()
		b := policy.Defaults()
		Expect(a.Hash()).To(Equal(b.Hash()))
		b.MaxTargets = 6
		Expect(a.Hash()).NotTo(Equal(b.Hash()))
	})
})

func make64() string {
	s := ""
	for range 64 {
		s += "a"
	}
	return s
}
