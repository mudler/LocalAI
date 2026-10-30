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
			Expect(r).To(Or(ContainSubstring(" /api/chats"), ContainSubstring(" /api/workspace")), "a route gated by the chats feature is under /api/chats or /api/workspace")
			Expect(seen).NotTo(HaveKey(r), "a duplicate registry entry")
			seen[r] = true
		}
		Expect(APIFeatures).To(ContainElement(FeatureChats))
		Expect(AllFeatures).To(ContainElement(FeatureChats))
	})

	It("gates the saved agents routes with their own feature, off by default", func() {
		routes := []string{}
		for _, r := range RouteFeatureRegistry {
			if r.Feature == FeatureAgents {
				routes = append(routes, r.Method+" "+r.Pattern)
			}
		}
		Expect(routes).To(ConsistOf(
			"GET /api/chats/agents",
			"POST /api/chats/agents",
			"GET /api/chats/agents/templates",
			"POST /api/chats/agents/templates/:id/create",
			"POST /api/chats/agents/import",
			"GET /api/chats/agents/:id/export",
			"GET /api/chats/agents/:id/runs",
			"GET /api/chats/agents/:id",
			"PUT /api/chats/agents/:id",
			"DELETE /api/chats/agents/:id",
		))
		Expect(AgentFeatures).To(ContainElement(FeatureAgents))
		Expect(APIFeatures).NotTo(ContainElement(FeatureAgents))
		var meta *FeatureMeta
		for _, m := range AgentFeatureMetas() {
			if m.Key == FeatureAgents {
				m := m
				meta = &m
			}
		}
		Expect(meta).NotTo(BeNil())
		Expect(meta.DefaultValue).To(BeFalse())
	})

	It("gates the trigger routes with their own feature, off by default", func() {
		routes := []string{}
		for _, r := range RouteFeatureRegistry {
			if r.Feature == FeatureTriggers {
				routes = append(routes, r.Method+" "+r.Pattern)
			}
		}
		Expect(routes).To(ConsistOf(
			"GET /api/chats/triggers",
			"POST /api/chats/triggers",
			"GET /api/chats/triggers/preview",
			"GET /api/chats/triggers/proposals",
			"POST /api/chats/triggers/proposals/:id/confirm",
			"PUT /api/chats/triggers/proposals/:id",
			"DELETE /api/chats/triggers/proposals/:id",
			"GET /api/chats/triggers/:id",
			"PUT /api/chats/triggers/:id",
			"DELETE /api/chats/triggers/:id",
			"POST /api/chats/triggers/:id/run",
			"POST /api/chats/triggers/:id/enable",
			"POST /api/chats/triggers/:id/disable",
			"GET /api/chats/triggers/:id/runs",
		))
		Expect(AgentFeatures).To(ContainElement(FeatureTriggers))
		Expect(APIFeatures).NotTo(ContainElement(FeatureTriggers))
		var meta *FeatureMeta
		for _, m := range AgentFeatureMetas() {
			if m.Key == FeatureTriggers {
				m := m
				meta = &m
			}
		}
		Expect(meta).NotTo(BeNil())
		Expect(meta.DefaultValue).To(BeFalse())
	})

	It("gates the connector routes with their own feature, off by default", func() {
		routes := []string{}
		for _, r := range RouteFeatureRegistry {
			if r.Feature == FeatureConnectors {
				routes = append(routes, r.Method+" "+r.Pattern)
			}
		}
		Expect(routes).To(ConsistOf(
			"GET /api/chats/connectors",
			"GET /api/chats/connectors/kinds",
			"POST /api/chats/connectors",
			"GET /api/chats/connectors/:id",
			"PUT /api/chats/connectors/:id",
			"DELETE /api/chats/connectors/:id",
			"POST /api/chats/connectors/:id/enable",
			"POST /api/chats/connectors/:id/disable",
			"POST /api/chats/connectors/:id/test",
			"GET /api/chats/connectors/:id/senders",
			"POST /api/chats/connectors/:id/senders/:sender/allow",
		))
		Expect(AgentFeatures).To(ContainElement(FeatureConnectors))
		Expect(APIFeatures).NotTo(ContainElement(FeatureConnectors))
		var meta *FeatureMeta
		for _, m := range AgentFeatureMetas() {
			if m.Key == FeatureConnectors {
				m := m
				meta = &m
			}
		}
		Expect(meta).NotTo(BeNil())
		Expect(meta.DefaultValue).To(BeFalse())
	})

	It("gates the agent tasks and jobs routes with mcp_jobs, off by default", func() {
		routes := []string{}
		for _, r := range RouteFeatureRegistry {
			if r.Feature == FeatureMCPJobs {
				routes = append(routes, r.Method+" "+r.Pattern)
			}
		}
		Expect(routes).To(ConsistOf(
			"POST /api/agent/tasks",
			"GET /api/agent/tasks",
			"GET /api/agent/tasks/:id",
			"PUT /api/agent/tasks/:id",
			"DELETE /api/agent/tasks/:id",
			"POST /api/agent/tasks/:name/execute",
			"POST /api/agent/jobs/execute",
			"GET /api/agent/jobs",
			"GET /api/agent/jobs/:id",
			"POST /api/agent/jobs/:id/cancel",
			"POST /api/agent/jobs/:id/rerun",
			"DELETE /api/agent/jobs/:id",
		))
		Expect(AgentFeatures).To(ContainElement(FeatureMCPJobs))
		Expect(APIFeatures).NotTo(ContainElement(FeatureMCPJobs))
	})

	It("does not gate the chats routes with an automation feature", func() {
		for _, r := range RouteFeatureRegistry {
			if r.Feature == FeatureChats {
				Expect(r.Pattern).NotTo(MatchRegexp(`/api/chats/(agents|triggers|connectors)(/|$)`), r.Method+" "+r.Pattern)
			}
		}
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
