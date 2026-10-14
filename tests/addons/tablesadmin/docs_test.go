// SPDX-License-Identifier: MIT
package tablesadmin_test

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the advertised surface of the resource management routes", func() {
	It("documents every route, every emitted field and every typed answer in the Swagger spec", func() {
		spec, err := os.ReadFile("../../../swagger/swagger.json")
		Expect(err).NotTo(HaveOccurred())
		var doc struct {
			Paths       map[string]map[string]json.RawMessage `json:"paths"`
			Definitions map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"definitions"`
		}
		Expect(json.Unmarshal(spec, &doc)).To(Succeed())
		for path, methods := range map[string][]string{
			"/api/addons/resources":                                          {"get", "post"},
			"/api/addons/resources/{resourceId}/grants":                      {"get"},
			"/api/addons/resources/{resourceId}/grants/{userId}":             {"put", "delete"},
			"/api/addons/resources/{resourceId}/users":                       {"get"},
			"/api/addons/resources/{resourceId}/rename":                      {"post"},
			"/api/addons/resources/{resourceId}/delete":                      {"post"},
			"/api/addons/resources/{resourceId}/apply":                       {"post"},
			"/api/addons/resources/{resourceId}/operations/{operationId}":    {"get"},
			"/api/addons/conversations/{conversationId}/selection/{addonId}": {"get", "put", "delete"},
		} {
			Expect(doc.Paths).To(HaveKey(path))
			for _, method := range methods {
				Expect(doc.Paths[path]).To(HaveKey(method), path)
				text := string(doc.Paths[path][method])
				Expect(text).To(ContainSubstring(`"Addons"`), path+" "+method)
				Expect(text).To(ContainSubstring("MCP"), path+" "+method+" must say that no MCP tool maps to it, or that it is read-only")
			}
		}
		for _, check := range []struct{ path, method string }{
			{"/api/addons/resources/{resourceId}/rename", "post"}, {"/api/addons/resources/{resourceId}/delete", "post"}, {"/api/addons/resources/{resourceId}/apply", "post"},
		} {
			text := string(doc.Paths[check.path][check.method])
			for _, want := range []string{`"409"`, `"503"`, "stale", "outcome_unknown", "X-LocalAI-Confirm"} {
				Expect(text).To(ContainSubstring(want), check.path+" "+want)
			}
		}
		Expect(string(doc.Paths["/api/addons/resources/{resourceId}/grants"]["get"])).To(ContainSubstring("sharing_disabled"))

		emitted := func(v any) map[string]any {
			b, err := json.Marshal(v)
			Expect(err).NotTo(HaveOccurred())
			var m map[string]any
			Expect(json.Unmarshal(b, &m)).To(Succeed())
			return m
		}
		rev := contract.Revision{Schema: 1, Data: 2}
		n := 1
		info := gateway.ResourceInfo{ID: "r", AddonID: "tables", Name: "N", Role: "owner", Owner: "O", SharedWith: &n, Revision: &rev}
		for name, v := range map[string]any{
			"gateway.ResourceInfo":          info,
			"gateway.ResourceList":          gateway.ResourceList{AddonID: "tables", Generation: 1, Sharing: true, Noun: "database", Operations: []string{"create"}, Resources: []gateway.ResourceInfo{info}, Truncated: true},
			"gateway.Grant":                 gateway.Grant{UserID: "u", DisplayName: "D", Role: "viewer"},
			"gateway.GrantList":             gateway.GrantList{ResourceID: "r", Grants: []gateway.Grant{}},
			"gateway.UserRef":               gateway.UserRef{ID: "u", DisplayName: "D"},
			"gateway.UserList":              gateway.UserList{Users: []gateway.UserRef{}},
			"gateway.DeleteResult":          gateway.DeleteResult{ResourceID: "r", Deleted: true},
			"gateway.OperationStatus":       gateway.OperationStatus{OperationID: "o", State: "committed", Tool: "rows-insert", RequestHash: "h"},
			"gateway.Selection":             gateway.Selection{AddonID: "tables", ConversationID: "c", ResourceID: "r", Role: "owner", Name: "N"},
			"gateway.CreateResourceRequest": gateway.CreateResourceRequest{AddonID: "tables", OperationID: "o", Generation: 1, Arguments: json.RawMessage(`{}`)},
			"gateway.RenameResourceRequest": gateway.RenameResourceRequest{OperationID: "o", Generation: 1, Revision: rev, Name: "N"},
			"gateway.DeleteResourceRequest": gateway.DeleteResourceRequest{OperationID: "o", Generation: 1, Revision: rev, Confirm: "N"},
			"gateway.SetGrantRequest":       gateway.SetGrantRequest{Role: "viewer", OperationID: "o"},
			"gateway.ApplyResourceRequest":  gateway.ApplyResourceRequest{OperationID: "o", Generation: 1, Revision: rev, Tool: "rows-insert", Arguments: json.RawMessage(`{}`)},
			"gateway.ApplyResult":           gateway.ApplyResult{ResourceID: "r", OperationID: "o", Revision: rev, Result: json.RawMessage(`{}`), RequestHash: "h"},
			"gateway.SelectionRequest":      gateway.SelectionRequest{ResourceID: "r"},
		} {
			def, ok := doc.Definitions[name]
			Expect(ok).To(BeTrue(), name)
			shape := emitted(v)
			for field := range shape {
				Expect(def.Properties).To(HaveKey(field), name+"."+field)
			}
			for field := range def.Properties {
				Expect(shape).To(HaveKey(field), name+" documents "+field+" that is never emitted")
			}
		}
		// The error object documents the new types.
		text, _ := json.Marshal(doc.Definitions["addons.AddonError"].Properties["type"])
		for _, typ := range []string{"sharing_disabled", "stale", "invalid_request", "quota_reached", "not_selected", "operation_conflict"} {
			Expect(string(text)).To(ContainSubstring(typ))
		}
	})

	It("lists every route in the instructions registry, the feature registry and the documentation, with no MCP tool for any", func() {
		h := ready(false)
		got := h.send("GET", "/api/instructions/addons", "", h.bearer("alice"))
		Expect(got.code).To(Equal(http.StatusOK), got.raw)
		for _, want := range []string{"/api/addons/resources", "grants", "users?q=", "rename", "delete", "apply", "selection", "select_resource", "never tools"} {
			Expect(got.raw).To(ContainSubstring(want), want)
		}
		registered := map[string]bool{}
		for _, entry := range auth.RouteFeatureRegistry {
			if entry.Feature == auth.FeatureAddons {
				registered[entry.Method+" "+entry.Pattern] = true
			}
		}
		docs, err := os.ReadFile("../../../docs/content/features/addons.md")
		Expect(err).NotTo(HaveOccurred())
		tables, err := os.ReadFile("../../../docs/content/features/tables.md")
		Expect(err).NotTo(HaveOccurred())
		for _, key := range []string{
			"GET /api/addons/resources", "POST /api/addons/resources",
			"GET /api/addons/resources/:resourceId/grants", "PUT /api/addons/resources/:resourceId/grants/:userId",
			"DELETE /api/addons/resources/:resourceId/grants/:userId", "GET /api/addons/resources/:resourceId/users",
			"POST /api/addons/resources/:resourceId/rename", "POST /api/addons/resources/:resourceId/delete",
			"POST /api/addons/resources/:resourceId/apply",
			"GET /api/addons/resources/:resourceId/operations/:operationId",
			"GET /api/addons/conversations/:conversationId/selection/:addonId",
			"PUT /api/addons/conversations/:conversationId/selection/:addonId",
			"DELETE /api/addons/conversations/:conversationId/selection/:addonId",
		} {
			Expect(registered).To(HaveKey(key))
		}
		for _, want := range []string{"never tools", "sharing_disabled", "outcome_unknown", "capabilities.adminTools", "capabilities.resources", "select_resource", "X-LocalAI-Conversation-Id", "deleting"} {
			Expect(string(docs)).To(ContainSubstring(want), want)
		}
		Expect(string(tables)).To(ContainSubstring("databases-delete"))
		Expect(strings.Count(string(tables), "not tools")).To(BeNumerically(">=", 1))
		// The admin assistant catalog names none of it.
		for _, file := range []string{"../../../pkg/mcp/localaitools/tools.go"} {
			b, err := os.ReadFile(file)
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.ToLower(string(b))).NotTo(ContainSubstring("/api/addons"))
		}
	})
})
