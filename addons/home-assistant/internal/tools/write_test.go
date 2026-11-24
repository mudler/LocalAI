// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/home-assistant/hatest"
	"github.com/mudler/LocalAI/addons/home-assistant/internal/policy"
	"github.com/mudler/LocalAI/addons/home-assistant/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the write tools", func() {
	var (
		srv *hatest.Server
		g   *rig
	)
	BeforeEach(func() {
		srv = home()
		DeferCleanup(srv.Close)
		g = newRig(srv, "")
	})

	light := func(extra map[string]any) map[string]any {
		a := map[string]any{"domain": "light", "service": "turn_off", "entityIds": []string{"light.kitchen"}}
		for k, v := range extra {
			a[k] = v
		}
		return a
	}

	Describe("a quiet service", func() {
		It("previews with plain facts and changes nothing", func() {
			p, err := g.rt.Preview(context.Background(), g.env("call-service", light(nil)))
			Expect(err).NotTo(HaveOccurred())
			Expect(srv.Posts()).To(BeZero())
			Expect(srv.StateOf("light.kitchen")).To(Equal("on"))
			var s struct {
				Count  int `json:"count"`
				Sample struct {
					Action   string           `json:"action"`
					Physical bool             `json:"physical"`
					Targets  []map[string]any `json:"targets"`
				} `json:"sample"`
			}
			Expect(json.Unmarshal(p.Summary, &s)).To(Succeed())
			Expect(s.Count).To(Equal(1))
			Expect(s.Sample.Action).To(Equal("light.turn_off"))
			Expect(s.Sample.Physical).To(BeFalse())
			Expect(s.Sample.Targets[0]).To(HaveKeyWithValue("name", "Kitchen Light"))
			Expect(s.Sample.Targets[0]).To(HaveKeyWithValue("state", "on"))
			Expect(len(p.Summary)).To(BeNumerically("<=", 3<<10))
			Expect(p.ExpiresAt.Sub(g.now)).To(Equal(2 * time.Hour))
		})

		It("applies once and keeps only the entity and its new state", func() {
			rc, err := g.do("call-service", light(map[string]any{"service": "turn_off", "entityIds": []string{"light.kitchen", "light.guest"}}))
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			Expect(srv.StateOf("light.kitchen")).To(Equal("off"))
			Expect(srv.StateOf("light.guest")).To(Equal("off"))
			calls := srv.ServiceCalls()
			Expect(calls).To(HaveLen(1))
			Expect(calls[0].Path).To(Equal("/api/services/light/turn_off"))
			Expect(calls[0].Token).To(Equal(token))
			var body map[string]any
			Expect(json.Unmarshal([]byte(calls[0].Body), &body)).To(Succeed())
			Expect(body["entity_id"]).To(ConsistOf("light.kitchen", "light.guest"))
			Expect(string(rc.Result.Payload)).NotTo(ContainSubstring("brightness"))
			Expect(string(rc.Result.Payload)).NotTo(ContainSubstring(token))
			Expect(string(rc.Result.Payload)).To(ContainSubstring("light.kitchen"))
		})

		It("passes the service data along", func() {
			_, err := g.do("call-service", light(map[string]any{"service": "turn_on", "data": map[string]any{"brightness": 120, "color_name": "red"}}))
			Expect(err).NotTo(HaveOccurred())
			var body map[string]any
			Expect(json.Unmarshal([]byte(srv.ServiceCalls()[0].Body), &body)).To(Succeed())
			Expect(body).To(HaveKeyWithValue("brightness", BeEquivalentTo(120)))
			Expect(body).To(HaveKeyWithValue("color_name", "red"))
		})

		It("is replayed from the receipt and never sent twice", func() {
			env := g.env("call-service", light(map[string]any{"service": "toggle"}))
			p, err := g.rt.Preview(context.Background(), env)
			Expect(err).NotTo(HaveOccurred())
			a, err := g.rt.Apply(context.Background(), env, p.Revision)
			Expect(err).NotTo(HaveOccurred())
			b, err := g.rt.Apply(context.Background(), env, p.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(b).To(Equal(a))
			Expect(srv.ServiceCalls()).To(HaveLen(1))
			rc, _ := g.rt.Receipt(context.Background(), env.Request.OperationID)
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		})

		It("answers unknown for an operation it never saw", func() {
			rc, err := g.rt.Receipt(context.Background(), "nope")
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptUnknown))
		})
	})

	Describe("what is refused before anything is sent", func() {
		refused := func(tool string, args map[string]any, why string) {
			GinkgoHelper()
			_, err := g.rt.Preview(context.Background(), g.env(tool, args))
			Expect(err).To(MatchError(ContainSubstring(why)))
			Expect(srv.ServiceCalls()).To(BeEmpty())
			_, err = g.do(tool, args)
			Expect(err).To(HaveOccurred())
			Expect(srv.ServiceCalls()).To(BeEmpty())
		}

		DescribeTable("the hard exclusions, whatever the allowlist says",
			func(domain, service, entity string) {
				g.setPolicy(func(p *policy.Policy) {
					// The file is edited by hand: Save would refuse these entries.
					p.Allow = []string{"light.turn_on"}
				})
				for _, tool := range []string{"call-service", "call-physical-service"} {
					refused(tool, map[string]any{"domain": domain, "service": service, "entityIds": []string{entity}}, "can never be called")
				}
			},
			Entry("unlock", "lock", "unlock", "lock.front"),
			Entry("open a lock", "lock", "open", "lock.front"),
			Entry("alarm", "alarm_control_panel", "alarm_disarm", "alarm_control_panel.home"),
			Entry("siren", "siren", "turn_on", "siren.hall"),
			Entry("valve", "valve", "open_valve", "valve.water"),
			Entry("water heater", "water_heater", "set_temperature", "water_heater.boiler"),
			Entry("restart", "homeassistant", "restart", "homeassistant.x"),
			Entry("stop", "homeassistant", "stop", "homeassistant.x"),
			Entry("supervisor", "hassio", "host_reboot", "hassio.x"),
			Entry("shell command", "shell_command", "run", "shell_command.x"),
			Entry("python script", "python_script", "run", "python_script.x"),
			Entry("reload", "light", "reload", "light.kitchen"),
			Entry("reload of automations", "automation", "reload", "automation.x"),
		)

		It("refuses a hard service even when the allowlist file names it", func() {
			dir := GinkgoT().TempDir()
			g2 := newRig(srv, dir)
			Expect(writeFile(dir, `{"allow":["lock.unlock","light.turn_on"],"maxTargets":5}`)).To(Succeed())
			_, err := g2.rt.Preview(context.Background(), g2.env("call-service", map[string]any{"domain": "lock", "service": "unlock", "entityIds": []string{"lock.front"}}))
			Expect(err).To(MatchError(ContainSubstring("policy of the add-on")), "a file that names a hard service is not a policy at all")
			Expect(srv.ServiceCalls()).To(BeEmpty())
		})

		It("refuses a service that is not on the allowlist, and names where to change it", func() {
			refused("call-service", map[string]any{"domain": "notify", "service": "mobile_app_phone", "entityIds": []string{"notify.x"}}, "not on the allowlist")
		})

		It("sends a physical service to the confirming tool and a quiet one to the other", func() {
			refused("call-service", map[string]any{"domain": "climate", "service": "set_temperature", "entityIds": []string{"climate.living"}, "data": map[string]any{"temperature": 21}}, "use call-physical-service")
			refused("call-service", map[string]any{"domain": "script", "service": "turn_on", "entityIds": []string{"script.goodnight"}}, "use call-physical-service")
			refused("call-service", map[string]any{"domain": "scene", "service": "turn_on", "entityIds": []string{"scene.evening"}}, "use call-physical-service")
			refused("call-physical-service", light(nil), "use call-service")
		})

		It("keeps notify, play_media and an unlisted domain out until they are allowed", func() {
			refused("call-physical-service", map[string]any{"domain": "media_player", "service": "play_media", "entityIds": []string{"media_player.tv"}}, "not on the allowlist")
			refused("call-physical-service", map[string]any{"domain": "cover", "service": "open_cover", "entityIds": []string{"cover.blind"}}, "not on the allowlist")
		})

		It("caps the targets, and accepts no area, device, all or other-domain target", func() {
			many := []string{"light.a1", "light.a2", "light.a3", "light.a4", "light.a5", "light.a6"}
			refused("call-service", light(map[string]any{"entityIds": many}), "1 to 5 entities")
			refused("call-service", light(map[string]any{"entityIds": []string{}}), "1 to 5 entities")
			refused("call-service", light(map[string]any{"entityIds": []string{"all"}}), "light.kitchen")
			refused("call-service", light(map[string]any{"entityIds": []string{"light.kitchen", "light.kitchen"}}), "twice")
			refused("call-service", light(map[string]any{"entityIds": []string{"switch.heater"}}), "not an entity of the domain light")
			refused("call-service", light(map[string]any{"areaId": "kitchen"}), "do not match")
			refused("call-service", light(map[string]any{"data": map[string]any{"area_id": "kitchen"}}), "not allowed")
			refused("call-service", light(map[string]any{"data": map[string]any{"entity_id": "all"}}), "not allowed")
			refused("call-service", light(map[string]any{"data": map[string]any{"device_id": "abc"}}), "not allowed")
			refused("call-service", light(map[string]any{"data": map[string]any{"label_id": "abc"}}), "not allowed")
		})

		It("raises the target cap only through the policy, up to its ceiling", func() {
			g.setPolicy(func(p *policy.Policy) { p.MaxTargets = 2 })
			refused("call-service", light(map[string]any{"entityIds": []string{"light.kitchen", "light.guest", "light.bedroom"}}), "1 to 2 entities")
			_, err := g.do("call-service", light(map[string]any{"entityIds": []string{"light.kitchen", "light.guest"}}))
			Expect(err).NotTo(HaveOccurred())
		})

		It("refuses excluded entities and entities that do not exist", func() {
			g.setPolicy(func(p *policy.Policy) { p.ExcludePatterns = []string{`light\.guest`} })
			refused("call-service", light(map[string]any{"entityIds": []string{"light.guest"}}), "excluded")
			g.setPolicy(func(p *policy.Policy) { p.ExcludeAreas = []string{"Bedroom"} })
			refused("call-service", light(map[string]any{"entityIds": []string{"light.bedroom"}}), "excluded")
			refused("call-service", light(map[string]any{"entityIds": []string{"light.nowhere"}}), "does not exist")
		})

		It("refuses data that is too big, too deep, or holds template markup", func() {
			refused("call-service", light(map[string]any{"service": "turn_on", "data": map[string]any{"x": strings.Repeat("a", 3000)}}), "2048 bytes")
			refused("call-service", light(map[string]any{"service": "turn_on", "data": map[string]any{"effect": "{{ states('lock.front') }}"}}), "template")
			refused("call-service", light(map[string]any{"service": "turn_on", "data": map[string]any{"a": map[string]any{"b": map[string]any{"c": map[string]any{"d": 1}}}}}), "deep")
			refused("call-service", light(map[string]any{"service": "turn_on", "data": map[string]any{"Bad-Key": 1}}), "not allowed")
			big := map[string]any{}
			for i := range 30 {
				big[fmt.Sprintf("k%d", i)] = i
			}
			refused("call-service", light(map[string]any{"service": "turn_on", "data": big}), "at most")
		})

		It("refuses an instance that is not the one the host allowed", func() {
			refused("call-service", light(map[string]any{"instance": "https://elsewhere.example"}), "not the one the host allowed")
		})
	})

	Describe("a physical service", func() {
		BeforeEach(func() {
			g.setPolicy(func(p *policy.Policy) { p.Allow = append(p.Allow, "cover.open_cover", "cover.close_cover", "notify.*") })
		})

		It("goes through the confirming tool, and the preview expires soon", func() {
			args := map[string]any{"domain": "climate", "service": "set_temperature", "entityIds": []string{"climate.living"}, "data": map[string]any{"temperature": 22}}
			p, err := g.rt.Preview(context.Background(), g.env("call-physical-service", args))
			Expect(err).NotTo(HaveOccurred())
			Expect(p.ExpiresAt.Sub(g.now)).To(Equal(15 * time.Minute))
			var s struct {
				Sample struct {
					Physical bool   `json:"physical"`
					Data     string `json:"data"`
				} `json:"sample"`
			}
			Expect(json.Unmarshal(p.Summary, &s)).To(Succeed())
			Expect(s.Sample.Physical).To(BeTrue())
			Expect(s.Sample.Data).To(ContainSubstring("22"))
			rc, err := g.do("call-physical-service", args)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		})

		It("moves a blind but never a garage door, a gate, a door or a cover of unknown type", func() {
			ok := map[string]any{"domain": "cover", "service": "open_cover", "entityIds": []string{"cover.blind"}}
			_, err := g.do("call-physical-service", ok)
			Expect(err).NotTo(HaveOccurred())
			for _, id := range []string{"cover.garage", "cover.mystery"} {
				_, err = g.do("call-physical-service", map[string]any{"domain": "cover", "service": "open_cover", "entityIds": []string{id}})
				Expect(err).To(MatchError(ContainSubstring("never moves it")), id)
			}
			srv.Add("cover.gate", "closed", map[string]any{"device_class": "gate"})
			srv.Add("cover.front_door", "closed", map[string]any{"device_class": "door"})
			for _, id := range []string{"cover.gate", "cover.front_door"} {
				_, err = g.do("call-physical-service", map[string]any{"domain": "cover", "service": "close_cover", "entityIds": []string{id}})
				Expect(err).To(HaveOccurred(), id)
			}
			Expect(srv.ServiceCalls()).To(HaveLen(1))
			Expect(srv.StateOf("cover.garage")).To(Equal("closed"))
		})

		It("still refuses a lock after notify and cover were allowed", func() {
			_, err := g.do("call-physical-service", map[string]any{"domain": "lock", "service": "unlock", "entityIds": []string{"lock.front"}})
			Expect(err).To(MatchError(ContainSubstring("can never")))
		})
	})

	Describe("a change that goes stale or is lost", func() {
		It("rejects the apply when the policy changed after the preview", func() {
			env := g.env("call-service", light(nil))
			p, err := g.rt.Preview(context.Background(), env)
			Expect(err).NotTo(HaveOccurred())
			g.setPolicy(func(q *policy.Policy) { q.MaxTargets = 6 })
			rc, err := g.rt.Apply(context.Background(), env, p.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptRejected))
			Expect(srv.ServiceCalls()).To(BeEmpty())
		})

		It("refuses at apply a service that the policy no longer allows", func() {
			env := g.env("call-service", light(nil))
			p, _ := g.rt.Preview(context.Background(), env)
			g.setPolicy(func(q *policy.Policy) { q.Allow = []string{"switch.turn_on"} })
			_, err := g.rt.Apply(context.Background(), env, p.Revision)
			Expect(err).To(MatchError(ContainSubstring("not on the allowlist")))
			Expect(srv.ServiceCalls()).To(BeEmpty())
		})

		It("does not call the state of a light a change", func() {
			env := g.env("call-service", light(nil))
			p, _ := g.rt.Preview(context.Background(), env)
			srv.Add("light.new", "on", nil)
			srv.StateOf("light.kitchen")
			rc, err := g.rt.Apply(context.Background(), env, p.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		})

		It("leaves an outcome unknown when the connection drops, and never sends it again", func() {
			dir := GinkgoT().TempDir()
			g2 := newRig(srv, dir)
			srv.DropWrites()
			env := g2.env("call-service", light(map[string]any{"service": "toggle"}))
			p, err := g2.rt.Preview(context.Background(), env)
			Expect(err).NotTo(HaveOccurred())
			_, err = g2.rt.Apply(context.Background(), env, p.Revision)
			Expect(err).To(HaveOccurred())
			Expect(srv.ServiceCalls()).To(HaveLen(1))
			rc, _ := g2.rt.Receipt(context.Background(), env.Request.OperationID)
			Expect(rc.State).To(Equal(contract.ReceiptUnknown))
			// A restart of the add-on, and a second apply of the same operation.
			g3 := newRig(srv, dir)
			rc, err = g3.rt.Apply(context.Background(), env, p.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptUnknown))
			Expect(srv.ServiceCalls()).To(HaveLen(1))
		})

		It("keeps a committed receipt across a restart", func() {
			dir := GinkgoT().TempDir()
			g2 := newRig(srv, dir)
			env := g2.env("call-service", light(nil))
			p, _ := g2.rt.Preview(context.Background(), env)
			_, err := g2.rt.Apply(context.Background(), env, p.Revision)
			Expect(err).NotTo(HaveOccurred())
			g3 := newRig(srv, dir)
			rc, _ := g3.rt.Receipt(context.Background(), env.Request.OperationID)
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
			again, err := g3.rt.Apply(context.Background(), env, p.Revision)
			Expect(err).NotTo(HaveOccurred())
			Expect(again.State).To(Equal(contract.ReceiptCommitted))
			Expect(srv.ServiceCalls()).To(HaveLen(1))
		})

		It("refuses a definite failure as a refusal and lets the operation be tried again", func() {
			srv.Fail("/api/services/light/turn_off", 400)
			_, err := g.do("call-service", light(nil))
			Expect(err).To(MatchError(ContainSubstring("refused the request")))
			srv.Fail("/api/nothing", 400)
			_, err = g.do("call-service", light(nil))
			Expect(err).NotTo(HaveOccurred())
		})

		It("never puts the token in an error", func() {
			env := g.env("call-service", light(nil))
			env.Request.Credential = "wrong-token-xyz"
			_, err := g.rt.Preview(context.Background(), env)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring("wrong-token-xyz"))
		})
	})

	It("declares the same tools the runtime serves", func() {
		Expect(tools.Declared()).To(ConsistOf("get-config", "list-areas", "search-entities", "get-state", "get-history", "get-logbook", "list-services",
			"call-service", "call-physical-service", "get-policy", "set-policy"))
	})
})
