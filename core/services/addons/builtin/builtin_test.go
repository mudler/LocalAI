// SPDX-License-Identifier: MIT
package builtin

import (
	"slices"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func ids(es []Entry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

var _ = ginkgo.Describe("the built-in catalog", func() {
	ginkgo.It("holds no add-on", func() {
		// Studio and Talk are packaged add-ons, installed and removed like the
		// others, so nothing is listed for a viewer, whatever the viewer holds.
		Expect(ids(Visible(Viewer{Admin: true}))).To(BeEmpty(), "no add-on is built in")
		Expect(ids(Visible(Viewer{Has: func(string) bool { return true }}))).To(BeEmpty(), "no add-on is built in")
		Expect(IDs()).To(BeEmpty(), "the catalog must be empty")
		Expect(Catalog()).To(BeEmpty(), "the catalog must be empty")
	})

	ginkgo.DescribeTable("does not list a packaged add-on",
		func(id string) {
			Expect(slices.Contains(IDs(), id)).To(BeFalse(), "%s is a packaged add-on and must not be in the built-in catalog", id)
		},
		ginkgo.Entry("studio", "studio"),
		ginkgo.Entry("talk", "talk"),
	)

	ginkgo.It("hides a gated entry from a viewer without the feature", func() {
		gated := Entry{ID: "x", Feature: "fine_tuning"}
		adminOnly := Entry{ID: "y", AdminOnly: true}
		none := Viewer{Has: func(string) bool { return false }}
		Expect(none.allows(gated)).To(BeFalse(), "a viewer without the feature must not see a gated entry")
		Expect(none.allows(adminOnly)).To(BeFalse(), "a viewer without the role must not see a gated entry")
		has := Viewer{Has: func(f string) bool { return f == "fine_tuning" }}
		Expect(has.allows(gated)).To(BeTrue(), "the feature opens a feature gate")
		Expect(has.allows(adminOnly)).To(BeFalse(), "the feature opens a feature gate and nothing else")
		admin := Viewer{Admin: true}
		Expect(admin.allows(gated)).To(BeTrue(), "an administrator passes every gate")
		Expect(admin.allows(adminOnly)).To(BeTrue(), "an administrator passes every gate")
	})

	ginkgo.It("has unique and well-formed IDs", func() {
		seen := map[string]bool{}
		for _, e := range Catalog() {
			Expect(seen[e.ID] || e.ID == "" || e.TitleKey == "" || e.MainPath == "" || e.Kind != KindPage).To(BeFalse(),
				"invalid or duplicate entry %+v", e)
			seen[e.ID] = true
		}
	})
})
