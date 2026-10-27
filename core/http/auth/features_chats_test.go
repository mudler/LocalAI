// SPDX-License-Identifier: MIT
package auth_test

import (
	. "github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats feature registration", func() {
	It("gates every chat route and is on by default", func() {
		routes := []string{}
		for _, r := range RouteFeatureRegistry {
			if r.Feature == FeatureChats {
				routes = append(routes, r.Method+" "+r.Pattern)
			}
		}
		// The list of routes is not typed here: the routes package walks the
		// registered router and requires an entry for each (see "Chats route
		// contract"), so a new route cannot be missed by a stale list.
		Expect(routes).NotTo(BeEmpty())
		Expect(routes).To(ContainElements("POST /api/chats", "GET /api/chats/:id/events"))
		seen := map[string]bool{}
		for _, r := range routes {
			Expect(r).To(ContainSubstring(" /api/chats"), "a route gated by the chats feature is under /api/chats")
			Expect(seen).NotTo(HaveKey(r), "a duplicate registry entry")
			seen[r] = true
		}
		Expect(APIFeatures).To(ContainElement(FeatureChats))
		Expect(AllFeatures).To(ContainElement(FeatureChats))
	})

	It("is listed in the admin feature table as default on", func() {
		var found *FeatureMeta
		for _, m := range APIFeatureMetas() {
			if m.Key == FeatureChats {
				m := m
				found = &m
			}
		}
		Expect(found).NotTo(BeNil())
		Expect(found.DefaultValue).To(BeTrue())
	})
})
