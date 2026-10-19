// SPDX-License-Identifier: MIT
package auth_test

import (
	. "github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Motion feature registration", func() {
	It("gates every session, ingestion and subscription route", func() {
		routes := []string{}
		for _, r := range RouteFeatureRegistry {
			if r.Feature == FeatureMotion {
				routes = append(routes, r.Method+" "+r.Pattern)
			}
		}
		Expect(routes).To(ConsistOf("POST /api/motion/sessions", "GET /api/motion/sessions/:id", "DELETE /api/motion/sessions/:id", "GET /api/motion/sessions/:id/poses", "POST /api/motion/sessions/:id/tickets"))
		Expect(APIFeatures).To(ContainElement(FeatureMotion))
	})
})
