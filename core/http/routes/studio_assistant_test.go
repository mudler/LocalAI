// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"net/http"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/functions"
)

// studioAssistant is an assistant whose setting a spec can flip.
type studioAssistant struct{ on atomic.Bool }

func (a *studioAssistant) Enabled() bool                      { return a.on.Load() }
func (a *studioAssistant) SystemPrompt() string               { return "manage the instance" }
func (a *studioAssistant) Executor() mcpendpoint.ToolExecutor { return a }
func (a *studioAssistant) HasTools() bool                     { return true }
func (a *studioAssistant) IsTool(string) bool                 { return false }
func (a *studioAssistant) ExecuteTool(context.Context, string, string) (string, error) {
	return "", nil
}
func (a *studioAssistant) DiscoverTools(context.Context) ([]functions.Function, error) {
	return nil, nil
}

var _ = Describe("Chats assistant endpoint", func() {
	var (
		s  *studioServer
		as *studioAssistant
	)
	BeforeEach(func() {
		as = &studioAssistant{}
		as.on.Store(true)
		s = newStudioServerWith(newTestFiles(), func(d *studio.Deps) {
			d.Assistant = as
			d.Users = func(_ context.Context, id string) (*auth.User, error) {
				role := auth.RoleUser
				if id == "root" {
					role = auth.RoleAdmin
				}
				return &auth.User{ID: id, Role: role, Status: auth.StatusActive}, nil
			}
		})
	})

	It("starts an assistant chat for an administrator and lists it like any other", func() {
		rec := s.do("root", http.MethodPost, "/api/chats", map[string]any{"model": "test-model", "title": "LocalAI Assistant", "assistant": true})
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("assistant", true))
		list := decodeJSON(s.do("root", http.MethodGet, "/api/chats", nil))
		Expect(list["conversations"]).To(HaveLen(1))
	})

	It("refuses a user who is not an administrator", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats", map[string]any{"model": "test-model", "assistant": true})
		Expect(rec.Code).To(Equal(http.StatusForbidden), rec.Body.String())
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats", nil))["conversations"]).To(BeEmpty())
	})

	It("refuses everyone while the setting is off", func() {
		as.on.Store(false)
		rec := s.do("root", http.MethodPost, "/api/chats", map[string]any{"model": "test-model", "assistant": true})
		Expect(rec.Code).To(Equal(http.StatusServiceUnavailable), rec.Body.String())
	})

	It("does not mark an ordinary chat", func() {
		rec := s.do("alice", http.MethodPost, "/api/chats", map[string]any{"model": "test-model"})
		Expect(rec.Code).To(Equal(http.StatusCreated))
		Expect(decodeJSON(rec)).NotTo(HaveKey("assistant"))
	})
})
