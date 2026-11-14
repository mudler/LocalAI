// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/home-assistant/hatest"
	"github.com/mudler/LocalAI/addons/home-assistant/internal/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the read tools", func() {
	var (
		srv *hatest.Server
		g   *rig
	)
	BeforeEach(func() {
		srv = home()
		DeferCleanup(srv.Close)
		g = newRig(srv, "")
	})

	Describe("get-config", func() {
		It("gives version, units and time zone, and no secret or location", func() {
			d, err := g.read("get-config", map[string]any{})
			Expect(err).NotTo(HaveOccurred())
			Expect(d).To(HaveKeyWithValue("version", "2026.11.0"))
			Expect(d).To(HaveKeyWithValue("timeZone", "Europe/Rome"))
			Expect(d["units"]).To(HaveKeyWithValue("temperature", "°C"))
			b, _ := json.Marshal(d)
			for _, leak := range []string{"secret.example", "10.0.0.2", "/config", "45.1", "9.1", "latitude", "elevation"} {
				Expect(string(b)).NotTo(ContainSubstring(leak))
			}
		})
		It("sends the token to the configured instance and nowhere else", func() {
			_, err := g.read("get-config", map[string]any{})
			Expect(err).NotTo(HaveOccurred())
			for _, c := range srv.Calls() {
				Expect(c.Token).To(Equal(token))
				Expect(c.Path + c.Query + c.Body).NotTo(ContainSubstring(token))
			}
		})
		It("says the token was refused without repeating it", func() {
			env := g.env("get-config", map[string]any{})
			env.Request.Credential = "another-token-value"
			_, err := g.rt.Read(context.Background(), env)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, protocol.ErrBadRequest)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("refused the token"))
			Expect(err.Error()).NotTo(ContainSubstring("another-token-value"))
		})
	})

	It("refuses an instance other than the one the host allowed", func() {
		_, err := g.read("get-config", map[string]any{"instance": "https://elsewhere.example"})
		Expect(err).To(MatchError(ContainSubstring("not the one the host allowed")))
		Expect(srv.Calls()).To(BeEmpty())
	})

	It("accepts the allowed instance in any letter case", func() {
		_, err := g.read("get-config", map[string]any{"instance": strings.ToUpper(srv.URL[:4]) + srv.URL[4:]})
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses a call without a token, an unknown tool and bad arguments", func() {
		env := g.env("get-config", map[string]any{})
		env.Request.Credential = ""
		_, err := g.rt.Read(context.Background(), env)
		Expect(err).To(MatchError(ContainSubstring("no token")))
		_, err = g.read("render-template", map[string]any{})
		Expect(err).To(MatchError(ContainSubstring("unknown tool")))
		_, err = g.read("get-state", map[string]any{"entityId": "light.kitchen", "extra": 1})
		Expect(err).To(MatchError(ContainSubstring("do not match")))
	})

	Describe("list-areas", func() {
		It("lists the areas with counts and the entities by domain", func() {
			d, err := g.read("list-areas", map[string]any{})
			Expect(err).NotTo(HaveOccurred())
			Expect(d["areas"]).To(HaveLen(4))
			Expect(d["unavailable"]).To(BeEquivalentTo(2), "unavailable and unknown")
			by := d["entitiesByDomain"].(map[string]any)
			Expect(by).To(HaveKeyWithValue("light", BeEquivalentTo(4)))
			Expect(by).NotTo(HaveKey("lock"))
			Expect(by).NotTo(HaveKey("camera"))
		})
		It("leaves out an excluded area", func() {
			g.setPolicy(func(p *policy.Policy) { p.ExcludeAreas = []string{"bedroom"} })
			d, err := g.read("list-areas", map[string]any{})
			Expect(err).NotTo(HaveOccurred())
			var names []string
			for _, a := range d["areas"].([]any) {
				names = append(names, a.(map[string]any)["name"].(string))
			}
			Expect(names).NotTo(ContainElement("Bedroom"))
			Expect(names).To(ContainElement("Kitchen"))
		})
	})

	Describe("search-entities", func() {
		It("finds by words in the name or the ID", func() {
			d, err := g.read("search-entities", map[string]any{"query": "kitchen light"})
			Expect(err).NotTo(HaveOccurred())
			Expect(ids(d["entities"])).To(Equal([]string{"light.kitchen"}))
			row := d["entities"].([]any)[0].(map[string]any)
			Expect(row).To(HaveKeyWithValue("name", "Kitchen Light"))
			Expect(row).To(HaveKeyWithValue("area", "Kitchen"))
		})
		It("filters by domain, state and area", func() {
			d, _ := g.read("search-entities", map[string]any{"domain": "light"})
			Expect(ids(d["entities"])).To(ConsistOf("light.kitchen", "light.bedroom", "light.guest", "light.attic"))
			d, _ = g.read("search-entities", map[string]any{"state": "unavailable"})
			Expect(ids(d["entities"])).To(Equal([]string{"sensor.diary"}))
			d, _ = g.read("search-entities", map[string]any{"area": "Bedroom"})
			Expect(ids(d["entities"])).To(ConsistOf("light.bedroom", "sensor.temp_bedroom"))
		})
		It("never returns the default excluded domains, the sensitive label or a pattern", func() {
			d, _ := g.read("search-entities", map[string]any{"limit": 50})
			got := ids(d["entities"])
			Expect(got).NotTo(ContainElements("lock.front", "camera.porch", "sensor.private_note"))
			g.setPolicy(func(p *policy.Policy) { p.ExcludePatterns = []string{`light\.guest`} })
			d, _ = g.read("search-entities", map[string]any{"domain": "light"})
			Expect(ids(d["entities"])).NotTo(ContainElement("light.guest"))
		})
		It("returns a lock again when the person removes the exclusion", func() {
			g.setPolicy(func(p *policy.Policy) { p.ExcludeDomains = []string{"camera"} })
			d, _ := g.read("search-entities", map[string]any{"domain": "lock"})
			Expect(ids(d["entities"])).To(Equal([]string{"lock.front"}))
		})
		It("does not search an excluded area", func() {
			g.setPolicy(func(p *policy.Policy) { p.ExcludeAreas = []string{"Attic"} })
			_, err := g.read("search-entities", map[string]any{"area": "Attic"})
			Expect(err).To(MatchError(ContainSubstring("excluded")))
			d, _ := g.read("search-entities", map[string]any{"domain": "light"})
			Expect(ids(d["entities"])).NotTo(ContainElement("light.attic"))
		})
		It("bounds the output and says it was cut", func() {
			for i := range 120 {
				srv.Add(fmt.Sprintf("sensor.bulk_%03d", i), "1", map[string]any{"friendly_name": strings.Repeat("N", 300)})
			}
			d, err := g.read("search-entities", map[string]any{"query": "bulk", "limit": 500})
			Expect(err).NotTo(HaveOccurred())
			Expect(d["entities"]).To(HaveLen(50))
			Expect(d["total"]).To(BeEquivalentTo(120))
			Expect(d["truncated"]).To(BeTrue())
			row := d["entities"].([]any)[0].(map[string]any)
			Expect(len(row["name"].(string))).To(BeNumerically("<=", 100))
		})
		It("refuses a bad domain", func() {
			_, err := g.read("search-entities", map[string]any{"domain": "light; drop"})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("get-state", func() {
		It("gives the state with attributes and no picture, token or location", func() {
			d, err := g.read("get-state", map[string]any{"entityId": "light.kitchen"})
			Expect(err).NotTo(HaveOccurred())
			Expect(d).To(HaveKeyWithValue("state", "on"))
			attrs := d["attributes"].(map[string]any)
			Expect(attrs).To(HaveKeyWithValue("brightness", BeEquivalentTo(200)))
			Expect(attrs).NotTo(HaveKey("entity_picture"))
			Expect(attrs).NotTo(HaveKey("access_token"))
			Expect(d["attributesOmitted"]).To(BeEquivalentTo(2))
			Expect(d["area"]).To(Equal("Kitchen"))
		})
		It("strips the location of a person and secret-looking keys", func() {
			g.setPolicy(func(p *policy.Policy) { p.ExcludeDomains = nil })
			d, _ := g.read("get-state", map[string]any{"entityId": "person.ann"})
			attrs := d["attributes"].(map[string]any)
			Expect(attrs).To(HaveKey("source"))
			for _, k := range []string{"latitude", "longitude", "gps_accuracy"} {
				Expect(attrs).NotTo(HaveKey(k))
			}
			d, _ = g.read("get-state", map[string]any{"entityId": "climate.living"})
			attrs = d["attributes"].(map[string]any)
			Expect(attrs).To(HaveKey("temperature"))
			Expect(attrs).NotTo(HaveKey("api_key"))
			Expect(attrs).NotTo(HaveKey("wifi_password"))
			d, _ = g.read("get-state", map[string]any{"entityId": "camera.porch"})
			b, _ := json.Marshal(d)
			Expect(string(b)).NotTo(ContainSubstring("cam-tok"))
		})
		It("drops a value that carries a token in an address and bounds long text", func() {
			srv.Add("sensor.odd", "x", map[string]any{"friendly_name": "Odd", "link": "http://h/p?access_token=zzz", "long": strings.Repeat("a", 900), "nested": map[string]any{"a": strings.Repeat("b", 600)}})
			d, _ := g.read("get-state", map[string]any{"entityId": "sensor.odd"})
			attrs := d["attributes"].(map[string]any)
			Expect(attrs).NotTo(HaveKey("link"))
			Expect(attrs).NotTo(HaveKey("nested"))
			Expect(len(attrs["long"].(string))).To(BeNumerically("<=", 200))
		})
		It("returns only the attributes asked for", func() {
			d, _ := g.read("get-state", map[string]any{"entityId": "light.kitchen", "attributes": []string{"brightness"}})
			Expect(d["attributes"]).To(HaveLen(1))
		})
		It("refuses an excluded entity without asking Home Assistant for its state", func() {
			for _, id := range []string{"lock.front", "camera.porch", "sensor.private_note"} {
				_, err := g.read("get-state", map[string]any{"entityId": id})
				Expect(err).To(MatchError(ContainSubstring("excluded")), id)
			}
			for _, c := range srv.Calls() {
				Expect(c.Path).NotTo(HavePrefix("/api/states/"))
			}
		})
		It("refuses an entity ID that is not one", func() {
			for _, id := range []string{"", "light", "../config", "light.kitchen/../../x", "LIGHT.kitchen", "light.kitchen?x=1"} {
				_, err := g.read("get-state", map[string]any{"entityId": id})
				Expect(err).To(HaveOccurred(), id)
			}
		})
		It("says not found for an entity that does not exist", func() {
			_, err := g.read("get-state", map[string]any{"entityId": "light.nowhere"})
			Expect(err).To(MatchError(ContainSubstring("not found")))
		})
	})

	Describe("exclusions that need Home Assistant to answer", func() {
		It("fails closed when an excluded area cannot be checked", func() {
			g.setPolicy(func(p *policy.Policy) { p.ExcludeAreas = []string{"Bedroom"} })
			srv.Fail("/api/template", 500)
			_, err := g.read("search-entities", map[string]any{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("could not be checked"))
		})
		It("applies the label when the instance knows labels, and says so when it does not", func() {
			srv.NoLabels()
			d, err := g.read("search-entities", map[string]any{"query": "private"})
			Expect(err).NotTo(HaveOccurred())
			Expect(d["_warnings"]).To(ContainElement(ContainSubstring("labels")))
		})
		It("fails closed when the label check breaks for another reason", func() {
			srv.Fail("/api/template", 500)
			_, err := g.read("search-entities", map[string]any{})
			Expect(err).To(MatchError(ContainSubstring("could not be checked")))
		})
		It("can have the label check turned off", func() {
			g.setPolicy(func(p *policy.Policy) { p.ExcludeLabel = "" })
			d, _ := g.read("search-entities", map[string]any{"query": "private"})
			Expect(ids(d["entities"])).To(Equal([]string{"sensor.private_note"}))
		})
	})

	It("refuses to serve when the policy file is damaged, rather than serve without it", func() {
		dir := GinkgoT().TempDir()
		g2 := newRig(srv, dir)
		Expect(writeFile(dir, `{"allow":["lock.unlock"],"maxTargets":5}`)).To(Succeed())
		_, err := g2.read("search-entities", map[string]any{})
		Expect(err).To(MatchError(ContainSubstring("policy of the add-on")))
		Expect(srv.Calls()).To(BeEmpty())
	})

	Describe("get-history and get-logbook", func() {
		It("downsamples a long history, with numeric statistics", func() {
			var pts []hatest.Point
			for i := range 400 {
				pts = append(pts, hatest.Point{At: g.now.Add(-time.Duration(400-i) * time.Minute), State: fmt.Sprint(i % 50)})
			}
			srv.SetHistory("sensor.temp_office", pts...)
			d, err := g.read("get-history", map[string]any{"entityId": "sensor.temp_office", "hours": 12})
			Expect(err).NotTo(HaveOccurred())
			Expect(d["points"]).To(HaveLen(100))
			Expect(d["changes"]).To(BeEquivalentTo(400))
			Expect(d["sampled"]).To(BeTrue())
			Expect(d["numeric"]).To(HaveKeyWithValue("max", BeEquivalentTo(49)))
			var query string
			for _, c := range srv.Calls() {
				if strings.HasPrefix(c.Path, "/api/history/period/2026-11-04T00:00:00Z") {
					query = c.Query
				}
			}
			Expect(query).To(ContainSubstring("filter_entity_id=sensor.temp_office"))
			Expect(query).To(ContainSubstring("end_time=2026-11-04T12%3A00%3A00Z"))
			Expect(query).To(ContainSubstring("no_attributes"))
		})
		It("handles an entity with no history and non-numeric states", func() {
			d, err := g.read("get-history", map[string]any{"entityId": "light.kitchen"})
			Expect(err).NotTo(HaveOccurred())
			Expect(d["points"]).To(BeEmpty())
			srv.SetHistory("light.kitchen", hatest.Point{At: g.now, State: "on"})
			d, _ = g.read("get-history", map[string]any{"entityId": "light.kitchen"})
			Expect(d).NotTo(HaveKey("numeric"))
		})
		It("bounds the window and refuses excluded entities", func() {
			_, err := g.read("get-history", map[string]any{"entityId": "light.kitchen", "hours": 500})
			Expect(err).To(MatchError(ContainSubstring("hours")))
			_, err = g.read("get-history", map[string]any{"entityId": "light.kitchen", "end": "yesterday"})
			Expect(err).To(HaveOccurred())
			_, err = g.read("get-history", map[string]any{"entityId": "lock.front"})
			Expect(err).To(MatchError(ContainSubstring("excluded")))
			_, err = g.read("get-logbook", map[string]any{"entityId": "camera.porch"})
			Expect(err).To(MatchError(ContainSubstring("excluded")))
		})
		It("keeps the newest logbook entries up to the limit and cuts long text", func() {
			var es []hatest.Entry
			for i := range 80 {
				es = append(es, hatest.Entry{When: g.now.Add(-time.Duration(80-i) * time.Minute), Name: "Kitchen Light", Message: fmt.Sprintf("turned on %d %s", i, strings.Repeat("z", 400))})
			}
			srv.SetLogbook("light.kitchen", es...)
			d, err := g.read("get-logbook", map[string]any{"entityId": "light.kitchen"})
			Expect(err).NotTo(HaveOccurred())
			Expect(d["entries"]).To(HaveLen(50))
			Expect(d["total"]).To(BeEquivalentTo(80))
			Expect(d["truncated"]).To(BeTrue())
			last := d["entries"].([]any)[49].(map[string]any)
			Expect(last["message"]).To(HavePrefix("turned on 79"))
			Expect(len(last["message"].(string))).To(BeNumerically("<=", 200))
		})
	})

	Describe("list-services", func() {
		It("lists only the services the policy allows, with the tool to call them", func() {
			d, err := g.read("list-services", map[string]any{})
			Expect(err).NotTo(HaveOccurred())
			var names []string
			tool := map[string]string{}
			for _, s := range d["services"].([]any) {
				m := s.(map[string]any)
				n := m["domain"].(string) + "." + m["service"].(string)
				names = append(names, n)
				tool[n] = m["tool"].(string)
			}
			Expect(names).To(ConsistOf("light.turn_on", "light.turn_off", "climate.set_temperature"))
			Expect(tool["light.turn_on"]).To(Equal("call-service"))
			Expect(tool["climate.set_temperature"]).To(Equal("call-physical-service"))
		})
		It("lists nothing from a hard domain even if the file says so", func() {
			g.setPolicy(func(p *policy.Policy) { p.Allow = []string{"light.turn_on"} })
			d, _ := g.read("list-services", map[string]any{"domain": "lock"})
			Expect(d["services"]).To(BeEmpty())
		})
	})

	It("passes a rate limit of Home Assistant on as a wait", func() {
		srv.Fail("/api/config", 429)
		_, err := g.read("get-config", map[string]any{})
		var limited *contract.ErrRateLimited
		Expect(errors.As(err, &limited)).To(BeTrue())
	})

	It("reads the policy again on each call", func() {
		d, _ := g.read("search-entities", map[string]any{"domain": "light"})
		Expect(ids(d["entities"])).To(ContainElement("light.guest"))
		g.setPolicy(func(p *policy.Policy) { p.ExcludePatterns = []string{`light\..*`} })
		d, _ = g.read("search-entities", map[string]any{"domain": "light"})
		Expect(d["entities"]).To(BeEmpty())
	})
})
