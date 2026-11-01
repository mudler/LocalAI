// SPDX-License-Identifier: MIT
package agents_test

import (
	"encoding/json"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("memory policy normalization", func() {
	It("migrates names only to search only without aliasing", func() {
		a := agents.Agent{Collections: []string{"docs"}}
		n, err := agents.NormalizeMemory(a)
		Expect(err).NotTo(HaveOccurred())
		Expect(n.CollectionPermissions.Grants).To(Equal([]agents.CollectionGrant{{Collection: "docs", Search: true}}))
		Expect(n.Memory).To(Equal(&agents.MemorySettings{Version: 1, ToolStorage: "content", AutomaticIngestion: "off"}))
		n.CollectionPermissions.Grants[0].Collection = "other"
		Expect(a.Collections).To(Equal([]string{"docs"}))
	})
	It("accepts independent grants and all modes including tool-only", func() {
		for _, mode := range []string{"off", "incoming_only", "incoming_and_replies", "summary"} {
			a := agents.Agent{Name: "a", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "docs", DeleteEntries: true}}}, Memory: &agents.MemorySettings{Version: 1, AddMemory: true, Destination: "docs", ToolStorage: "summary", AutomaticIngestion: mode}}
			n, err := agents.NormalizeMemory(a)
			Expect(err).NotTo(HaveOccurred())
			Expect(n.Collections).To(BeEmpty())
			Expect(agents.Validate(n)).To(Succeed())
			a.Memory.Destination = ""
			Expect(agents.Validate(a)).To(MatchError(agents.ErrInvalid))
		}
	})
	It("rejects conflicting projections, duplicates and unknown policy values", func() {
		for _, raw := range []string{
			`{"version":2,"grants":[]}`, `{"version":1,"grants":[{"collection":"x","search":true,"write":true}]}`,
		} {
			var p agents.CollectionPermissions
			Expect(json.Unmarshal([]byte(raw), &p)).NotTo(Succeed())
		}
		for _, raw := range []string{`{"version":2}`, `{"version":1,"tool_storage":"raw"}`, `{"version":1,"automatic_ingestion":"all"}`, `{"version":1,"write":true}`} {
			var m agents.MemorySettings
			Expect(json.Unmarshal([]byte(raw), &m)).NotTo(Succeed())
		}
		a := agents.Agent{Collections: []string{}, CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "x", Search: true}}}}
		_, err := agents.NormalizeMemory(a)
		Expect(err).To(MatchError(agents.ErrInvalid))
		a.Collections = nil
		a.CollectionPermissions.Grants = append(a.CollectionPermissions.Grants, a.CollectionPermissions.Grants[0])
		_, err = agents.NormalizeMemory(a)
		Expect(err).To(MatchError(agents.ErrInvalid))
	})
})
