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
		Expect(routes).To(ConsistOf(
			"POST /api/chats",
			"GET /api/chats",
			"GET /api/chats/:id",
			"DELETE /api/chats/:id",
			"GET /api/chats/:id/messages",
			"POST /api/chats/:id/messages",
			"PATCH /api/chats/:id/pending/:pid",
			"DELETE /api/chats/:id/pending/:pid",
			"POST /api/chats/:id/send-now",
			"POST /api/chats/:id/stop",
			"GET /api/chats/:id/events",
			"GET /api/chats/files",
			"DELETE /api/chats/files",
			"GET /api/chats/files/content",
			"PUT /api/chats/files/content",
			"POST /api/chats/files/folders",
			"POST /api/chats/files/move",
			"GET /api/chats/files/trash",
			"POST /api/chats/files/undo",
			"GET /api/chats/files/history",
			"GET /api/chats/files/search",
			"GET /api/chats/skills",
			"POST /api/chats/skills",
			"GET /api/chats/skills/:layer/:name",
			"PUT /api/chats/skills/:layer/:name",
			"DELETE /api/chats/skills/:layer/:name",
			"PUT /api/chats/:id/skills",
			"GET /api/chats/:id/addons",
			"PUT /api/chats/:id/addons",
			"GET /api/chats/:id/tools",
			"PUT /api/chats/:id/mcp",
			"GET /api/chats/:id/mcp-apps/resource",
			"POST /api/chats/:id/mcp-apps/call",
			"GET /api/chats/mcp-connectors",
			"POST /api/chats/mcp-connectors",
			"GET /api/chats/mcp-connectors/:id",
			"PUT /api/chats/mcp-connectors/:id",
			"DELETE /api/chats/mcp-connectors/:id",
			"POST /api/chats/mcp-connectors/:id/enable",
			"POST /api/chats/mcp-connectors/:id/disable",
			"POST /api/chats/mcp-connectors/:id/test",
			"GET /api/chats/notifications",
			"DELETE /api/chats/notifications",
			"GET /api/chats/notifications/count",
			"GET /api/chats/conversion",
			"POST /api/chats/import",
			"POST /api/chats/notifications/read",
			"GET /api/chats/notifications/events",
			"GET /api/chats/triggers",
			"POST /api/chats/triggers",
			"GET /api/chats/triggers/preview",
			"GET /api/chats/triggers/proposals",
			"POST /api/chats/triggers/proposals/:id/confirm",
			"DELETE /api/chats/triggers/proposals/:id",
			"GET /api/chats/triggers/:id",
			"PUT /api/chats/triggers/:id",
			"DELETE /api/chats/triggers/:id",
			"POST /api/chats/triggers/:id/run",
			"POST /api/chats/triggers/:id/enable",
			"POST /api/chats/triggers/:id/disable",
			"GET /api/chats/triggers/:id/runs",
			"GET /api/chats/push/key",
			"POST /api/chats/push/subscribe",
			"DELETE /api/chats/push/subscribe",
			"GET /api/chats/connectors",
			"POST /api/chats/connectors",
			"GET /api/chats/connectors/:id",
			"PUT /api/chats/connectors/:id",
			"DELETE /api/chats/connectors/:id",
			"POST /api/chats/connectors/:id/enable",
			"POST /api/chats/connectors/:id/disable",
			"POST /api/chats/connectors/:id/test",
			"GET /api/chats/connectors/:id/senders",
			"POST /api/chats/connectors/:id/senders/:sender/allow",
			"GET /api/chats/agents",
			"POST /api/chats/agents",
			"GET /api/chats/agents/:id",
			"PUT /api/chats/agents/:id",
			"DELETE /api/chats/agents/:id",
		))
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
