// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("saved agent memory API", func() {
	It("round trips every mode through create, list, export, import and legacy replacement", func() {
		ctx := context.Background()
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ags, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := studiosvc.New(studiosvc.Deps{Store: st, Agents: ags, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: studiotest.NewGateHarness()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		h := New(svc, nil)
		call := func(fn func(echo.Context) error, body, id string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			out := httptest.NewRecorder()
			c := echo.New().NewContext(req, out)
			c.Set("auth_user", &auth.User{ID: "alice", Role: auth.RoleAdmin})
			c.SetParamNames("id")
			c.SetParamValues(id)
			Expect(fn(c)).To(Succeed())
			return out
		}
		for _, mode := range []string{"off", "incoming_only", "incoming_and_replies", "summary"} {
			body := `{"name":"` + mode + `","collection_permissions":{"version":1,"grants":[{"collection":"docs","search":false,"delete_entries":true}]},"memory":{"version":1,"add_memory":true,"destination":"docs","tool_storage":"summary","automatic_ingestion":"` + mode + `"}}`
			out := call(h.CreateAgent, body, "")
			Expect(out.Code).To(Equal(http.StatusCreated), out.Body.String())
			var saved schema.ChatsAgent
			Expect(json.Unmarshal(out.Body.Bytes(), &saved)).To(Succeed())
			Expect(saved.Memory.AutomaticIngestion).To(Equal(mode))
			Expect(saved.Collections).To(BeEmpty())
			listed := call(h.ListAgents, "", "")
			Expect(listed.Body.String()).To(ContainSubstring(`"destination":"docs"`))
			exported := call(h.ExportAgent, "", saved.ID)
			Expect(exported.Code).To(Equal(http.StatusOK))
			imported := call(h.ImportAgent, exported.Body.String(), "")
			Expect(imported.Code).To(Equal(http.StatusCreated), imported.Body.String())
			var result schema.ChatsAgentImportResult
			Expect(json.Unmarshal(imported.Body.Bytes(), &result)).To(Succeed())
			Expect(result.Agent.Memory).To(Equal(saved.Memory))
			Expect(result.Agent.CollectionPermissions).To(Equal(saved.CollectionPermissions))
			replaced := call(h.UpdateAgent, `{"name":"`+mode+`","collections":["read"]}`, saved.ID)
			Expect(replaced.Code).To(Equal(http.StatusOK))
			Expect(json.Unmarshal(replaced.Body.Bytes(), &saved)).To(Succeed())
			Expect(saved.Memory.AddMemory).To(BeFalse())
			Expect(saved.Memory.AutomaticIngestion).To(Equal("off"))
			Expect(saved.CollectionPermissions.Grants).To(Equal([]agents.CollectionGrant{{Collection: "read", Search: true}}))
		}
	})
	It("refuses malformed or ambiguous versioned policies in both API and import decoding", func() {
		for _, fields := range []string{
			`"memory":null`, `"collection_permissions":null`,
			`"memory":{"version":1,"add_memory":null}`,
			`"memory":{"version":1,"Version":1}`,
			`"memory":{"version":1,"version":1}`,
			`"memory":{"version":1,"tool_storage":"raw"}`,
			`"collection_permissions":{"version":1,"grants":[{"collection":"x","delete":true}]}`,
			`"memory":{"version":1},"memory":{"version":1}`,
		} {
			raw := `{"name":"bad",` + fields + `}`
			var def schema.ChatsAgentWrite
			Expect(json.Unmarshal([]byte(raw), &def)).NotTo(Succeed(), raw)
			_, err := readAgentFile([]byte(raw))
			Expect(err).To(HaveOccurred(), raw)
		}
	})
	for _, attack := range []struct{ name, fields string }{
		{"aliased null memory", `"Memory":null`},
		{"aliased duplicate memory", `"memory":{"version":1},"Memory":null`},
		{"aliased collections projection", `"collections":[],"Collections":null,"collection_permissions":{"version":1,"grants":[{"collection":"docs","search":true}]}`},
		{"aliased null permissions", `"Collection_Permissions":null`},
	} {
		for _, surface := range []string{"API", "bare import", "enveloped import"} {
			It("rejects "+attack.name+" in "+surface, func() {
				raw := `{"name":"bad",` + attack.fields + `}`
				if surface == "API" {
					var def schema.ChatsAgentWrite
					Expect(json.Unmarshal([]byte(raw), &def)).NotTo(Succeed(), raw)
				} else {
					if surface == "enveloped import" {
						raw = `{"version":1,"kind":"localai-agent","agent":` + raw + `}`
					}
					_, err := readAgentFile([]byte(raw))
					Expect(err).To(HaveOccurred(), raw)
				}
			})
		}
	}
	It("preserves explicit empty and null compatibility projections for conflict checking", func() {
		for _, value := range []string{`[]`, `null`} {
			var def schema.ChatsAgentWrite
			Expect(json.Unmarshal([]byte(`{"name":"bad","collections":`+value+`,"collection_permissions":{"version":1,"grants":[{"collection":"x","search":true}]}}`), &def)).To(Succeed())
			_, err := agents.NormalizeMemory(agents.Agent{Collections: def.Collections, CollectionPermissions: def.CollectionPermissions})
			Expect(err).To(MatchError(agents.ErrInvalid))
		}
	})
})
