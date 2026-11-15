// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"net/http"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// routeToolSet is an MCP tool executor with fixed tool names; it serves both
// LocalAI groups of the route specs.
type routeToolSet struct{ names []string }

func (r routeToolSet) DiscoverTools(context.Context) ([]functions.Function, error) {
	var out []functions.Function
	for _, n := range r.names {
		out = append(out, functions.Function{Name: n, Parameters: map[string]any{"type": "object"}})
	}
	return out, nil
}
func (r routeToolSet) IsTool(n string) bool { return len(r.names) > 0 }
func (r routeToolSet) HasTools() bool       { return len(r.names) > 0 }
func (r routeToolSet) ExecuteTool(context.Context, string, string) (string, error) {
	return "", nil
}

type routeAssistant struct{ routeToolSet }

func (routeAssistant) Enabled() bool                        { return true }
func (a routeAssistant) Executor() mcpendpoint.ToolExecutor { return a.routeToolSet }
func (routeAssistant) SystemPrompt() string                 { return "You manage this server." }

type routeClient struct{ routeToolSet }

func (c routeClient) ClientExecutor() mcpendpoint.ToolExecutor { return c.routeToolSet }
func (routeClient) FirstModel(context.Context, config.ModelConfigUsecase) (string, error) {
	return "a-model", nil
}

var _ = Describe("Chats LocalAI client and admin tool groups", func() {
	var (
		s        *studioServer
		clientOn = true
		adminOn  = true
	)
	BeforeEach(func() {
		clientOn, adminOn = true, true
		s = newStudioServerWith(newTestFiles(), func(d *studio.Deps) {
			d.IsAdmin = func(_ context.Context, user string) bool { return user == "root" }
			d.Assistant = routeAssistant{routeToolSet{names: []string{"install_model"}}}
			d.ClientTools = routeClient{routeToolSet{names: []string{"speak"}}}
			d.LocalAIClientOn = func() bool { return clientOn }
			d.LocalAIAdminOn = func() bool { return adminOn }
			d.Users = func(_ context.Context, id string) (*auth.User, error) {
				role := auth.RoleUser
				if id == "root" {
					role = auth.RoleAdmin
				}
				return &auth.User{ID: id, Role: role, Status: auth.StatusActive}, nil
			}
		})
	})

	group := func(user, id, name string) map[string]any {
		rec := s.do(user, http.MethodGet, "/api/chats/"+id+"/tools", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		for _, g := range decodeJSON(rec)["builtins"].([]any) {
			if g.(map[string]any)["group"] == name {
				return g.(map[string]any)
			}
		}
		return nil
	}

	It("lists both groups for Settings > Tools, each with its own setting, and follows the setting", func() {
		list := func() map[string]map[string]any {
			rec := s.do("root", http.MethodGet, "/api/chats/admin/tools", nil)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			out := map[string]map[string]any{}
			for _, t := range decodeJSON(rec)["tools"].([]any) {
				m := t.(map[string]any)
				out[m["id"].(string)] = m
			}
			return out
		}
		tools := list()
		Expect(tools).To(HaveKey("localai_client"))
		Expect(tools).To(HaveKey("localai_admin"))
		Expect(tools["localai_client"]).To(HaveKeyWithValue("setting", "chats_localai_client"))
		Expect(tools["localai_admin"]).To(HaveKeyWithValue("setting", "chats_localai_admin"))
		Expect(tools["localai_client"]).To(HaveKeyWithValue("enabled", true))
		Expect(tools["localai_client"]).To(HaveKeyWithValue("replaceable", false))
		adminOn = false
		Expect(list()["localai_admin"]).To(HaveKeyWithValue("enabled", false))
		Expect(list()["localai_client"]).To(HaveKeyWithValue("enabled", true))
	})

	It("offers the groups in the chat's tool panel with the usual switches", func() {
		id := s.create("root")
		client, admin := group("root", id, "localai_client"), group("root", id, "localai_admin")
		Expect(client).To(HaveKeyWithValue("usable", true))
		Expect(client["tools"]).To(Equal([]any{"speak"}))
		Expect(client).To(HaveKeyWithValue("off", false))
		Expect(admin).To(HaveKeyWithValue("opt_in", true))
		Expect(admin).To(HaveKeyWithValue("off", true))

		rec := s.do("root", http.MethodPut, "/api/chats/"+id+"/builtins", map[string]any{"off": []string{"localai_client"}, "on": []string{"localai_admin"}})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(group("root", id, "localai_client")).To(HaveKeyWithValue("off", true))
		Expect(group("root", id, "localai_admin")).To(HaveKeyWithValue("off", false))
	})

	It("tells a user that the admin group is not for them", func() {
		id := s.create("alice")
		Expect(group("alice", id, "localai_client")).To(HaveKeyWithValue("usable", true))
		admin := group("alice", id, "localai_admin")
		Expect(admin).To(HaveKeyWithValue("usable", false))
		Expect(admin["tools"]).NotTo(ContainElement("install_model"))
	})
})
