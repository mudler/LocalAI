package studio_test

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/wellknown"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("times that a Studio chat gives to propose_trigger", func() {
	// The clock of a rig starts at 2026-10-27 07:00 UTC, a Tuesday.
	proposal := func(when string, extra string) string {
		return `{"name":"Reminder","prompt":"Say hello.","when":` + when + extra + `}`
	}
	// run proposes with args and returns the one result.
	run := func(r *trigRig, args string) studiotest.ToolResult {
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "go")
		idle(r.svc, alice, id)
		res := r.h.ResultsFor("propose_trigger")
		Expect(res).To(HaveLen(1), "results = %+v", res)
		_ = args
		return res[0]
	}
	rigFor := func(args string) *trigRig {
		return newTrigRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "propose_trigger", Args: args}, {Say: "ok"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		})
	}

	It("offers the relative forms in the Studio schema, as the connector schema does", func() {
		r := rigFor(proposal(`{"in":"2 hours"}`, ""))
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "go")
		idle(r.svc, alice, id)
		schema := r.h.ToolSchema(id, "propose_trigger")
		Expect(schema).To(ContainSubstring(`"in"`))
		Expect(schema).To(ContainSubstring(`"at"`))
		Expect(schema).To(ContainSubstring("file_arrives_in"))
		Expect(r.h.ToolSchema(id, "update_trigger")).To(ContainSubstring(`"in"`))
	})

	It("takes a delay without date arithmetic", func() {
		res := run(func() *trigRig { r := rigFor(proposal(`{"in":"2 hours"}`, "")); return r }(), "")
		Expect(res.Err).To(BeNil(), "result = %+v", res)
		Expect(res.Out).To(ContainSubstring(`"next_run":"2026-10-27T09:00:00Z"`))
		Expect(res.Out).To(ContainSubstring(`"now":"2026-10-27T07:00:00Z"`))
	})

	It("takes the zone given next to at inside when, which used to fail with 'needs time_zone'", func() {
		r := rigFor(proposal(`{"at":"09:00","time_zone":"Europe/Rome"}`, ""))
		res := run(r, "")
		Expect(res.Err).To(BeNil(), "result = %+v", res)
		Expect(res.Out).To(ContainSubstring(`"next_run":"2026-10-27T09:00:00+01:00"`))
	})

	It("takes the zone beside when", func() {
		r := rigFor(proposal(`{"at":"09:00"}`, `,"time_zone":"Europe/Rome"`))
		res := run(r, "")
		Expect(res.Err).To(BeNil(), "result = %+v", res)
		Expect(res.Out).To(ContainSubstring(`"next_run":"2026-10-27T09:00:00+01:00"`))
	})

	It("says clearly what is missing when at has no zone and the person has none", func() {
		r := rigFor(proposal(`{"at":"09:00"}`, ""))
		res := run(r, "")
		Expect(res.Err).To(HaveOccurred())
		Expect(res.Err.Error()).To(ContainSubstring("needs a time zone"))
		Expect(res.Err.Error()).To(ContainSubstring("time_zone"))
	})

	It("names the day with a date and does not guess it", func() {
		r := rigFor(proposal(`{"at":"2026-11-01 09:00"}`, `,"time_zone":"Europe/Rome"`))
		res := run(r, "")
		Expect(res.Err).To(BeNil(), "result = %+v", res)
		// 2026-11-01 is a Sunday.
		Expect(res.Out).To(ContainSubstring(`"next_run":"2026-11-01T09:00:00+01:00"`))
	})

	It("refuses a date that is past and does not move it to another day", func() {
		r := rigFor(proposal(`{"at":"2026-10-25 09:00"}`, `,"time_zone":"Europe/Rome"`))
		res := run(r, "")
		Expect(res.Err).To(HaveOccurred())
		Expect(res.Err.Error()).To(ContainSubstring("past"))
	})

	It("uses the zone of the person for a timer and tells the model the date and the zone", func() {
		r := rigFor(proposal(`{"at":"18:00"}`, ""))
		Expect(r.sh.files.EnsureHome(context.Background(), alice)).To(Succeed())
		_, err := r.svc.WellKnown().Write(context.Background(), alice, wellknown.WriteRequest{Name: wellknown.User, Values: map[string]any{"timezone": "America/New_York"}})
		Expect(err).NotTo(HaveOccurred())
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "go")
		idle(r.svc, alice, id)
		res := r.h.ResultsFor("propose_trigger")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(BeNil(), "result = %+v", res)
		// 07:00 UTC is 03:00 in New York (UTC-4 until the first Sunday of November).
		Expect(res[0].Out).To(ContainSubstring(`"next_run":"2026-10-27T18:00:00-04:00"`))
		desc := r.h.ToolDescription(id, "propose_trigger")
		Expect(desc).To(ContainSubstring("Today is Tuesday 2026-10-27 in the user's time zone America/New_York"))
		Expect(strings.Count(desc, "Today is")).To(Equal(1))
	})

	It("tells the model that the zone is unknown when it is", func() {
		r := rigFor(proposal(`{"in":"2 hours"}`, ""))
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "go")
		idle(r.svc, alice, id)
		Expect(r.h.ToolDescription(id, "propose_trigger")).To(ContainSubstring("The user's time zone is not known"))
	})
})
