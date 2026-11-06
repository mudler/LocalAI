package openai

import (
	"context"
	"errors"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A non-admin caller who asks for metadata.localai_assistant=true must be
// refused. The assistant's in-process MCP server can install/delete models,
// edit configs, and rebrand the server, so a user-level caller driving it
// via prompt-injected tool calls would be a confused deputy.
var _ = Describe("requireAssistantAccess", func() {
	var (
		e *echo.Echo
		c echo.Context
	)

	makeContext := func() (echo.Context, *httptest.ResponseRecorder) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		rec := httptest.NewRecorder()
		return e.NewContext(req, rec), rec
	}

	BeforeEach(func() {
		e = echo.New()
		c, _ = makeContext()
	})

	Context("when auth is disabled", func() {
		It("admits any caller", func() {
			Expect(requireAssistantAccess(c, false)).To(BeNil())
		})

		It("admits even when no user is in context", func() {
			Expect(requireAssistantAccess(c, false)).To(BeNil())
		})
	})

	Context("when auth is enabled", func() {
		It("rejects an unauthenticated caller with 403", func() {
			err := requireAssistantAccess(c, true)
			Expect(err).To(HaveOccurred())
			httpErr, ok := err.(*echo.HTTPError)
			Expect(ok).To(BeTrue(), "expected echo.HTTPError, got %T", err)
			Expect(httpErr.Code).To(Equal(http.StatusForbidden))
			Expect(httpErr.Message).To(ContainSubstring("admin"))
		})

		It("rejects a regular user with 403", func() {
			c.Set("auth_user", &auth.User{ID: "u-1", Role: auth.RoleUser})
			err := requireAssistantAccess(c, true)
			Expect(err).To(HaveOccurred())
			httpErr, ok := err.(*echo.HTTPError)
			Expect(ok).To(BeTrue())
			Expect(httpErr.Code).To(Equal(http.StatusForbidden))
		})

		It("rejects a user with empty role with 403", func() {
			c.Set("auth_user", &auth.User{ID: "u-2", Role: ""})
			err := requireAssistantAccess(c, true)
			Expect(err).To(HaveOccurred())
		})

		It("admits an admin", func() {
			c.Set("auth_user", &auth.User{ID: "admin-1", Role: auth.RoleAdmin})
			Expect(requireAssistantAccess(c, true)).To(BeNil())
		})

		It("admits a synthetic admin from a legacy API key", func() {
			// Legacy API key callers get a synthetic admin user from the
			// auth middleware. They must continue to work — that's the
			// shape every existing single-key deployment has today.
			c.Set("auth_user", &auth.User{ID: "legacy-api-key", Role: auth.RoleAdmin})
			Expect(requireAssistantAccess(c, true)).To(BeNil())
		})
	})
})

var _ = Describe("requireAssistantOn", func() {
	ctxFor := func(u *auth.User) echo.Context {
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), httptest.NewRecorder())
		if u != nil {
			c.Set("auth_user", u)
		}
		return c
	}
	code := func(err error) int {
		GinkgoHelper()
		httpErr, ok := err.(*echo.HTTPError)
		Expect(ok).To(BeTrue(), "expected echo.HTTPError, got %T", err)
		return httpErr.Code
	}
	admin := &auth.User{ID: "a", Role: auth.RoleAdmin}
	user := &auth.User{ID: "u", Role: auth.RoleUser}

	It("answers 503 to an administrator when the admin group is switched off", func() {
		err := requireAssistantOn(ctxFor(admin), true, true)
		Expect(code(err)).To(Equal(http.StatusServiceUnavailable))
		Expect(err.(*echo.HTTPError).Message).To(ContainSubstring("switched off"))
	})

	It("answers 503 without accounts too, when the admin group is switched off", func() {
		Expect(code(requireAssistantOn(ctxFor(nil), false, true))).To(Equal(http.StatusServiceUnavailable))
	})

	It("still answers 403 to a non-admin, whatever the switch says", func() {
		Expect(code(requireAssistantOn(ctxFor(user), true, true))).To(Equal(http.StatusForbidden))
		Expect(code(requireAssistantOn(ctxFor(user), true, false))).To(Equal(http.StatusForbidden))
	})

	It("lets an administrator through while the group is on", func() {
		Expect(requireAssistantOn(ctxFor(admin), true, false)).To(Succeed())
	})
})

var _ = Describe("realtimeAssistantRefusal", func() {
	It("checks the admin role first, then the instance switch", func() {
		code, _ := realtimeAssistantRefusal(RealtimeSessionOptions{IsAdmin: false}, true)
		Expect(code).To(Equal("forbidden"))
		code, _ = realtimeAssistantRefusal(RealtimeSessionOptions{IsAdmin: false}, false)
		Expect(code).To(Equal("forbidden"))
		code, msg := realtimeAssistantRefusal(RealtimeSessionOptions{IsAdmin: true}, true)
		Expect(code).To(Equal("unavailable"))
		Expect(msg).To(ContainSubstring("switched off"))
		code, _ = realtimeAssistantRefusal(RealtimeSessionOptions{IsAdmin: true}, false)
		Expect(code).To(BeEmpty())
	})
})

var _ = Describe("assistantAllowedNow", func() {
	admin := &auth.User{ID: "a", Role: auth.RoleAdmin, Status: auth.StatusActive}
	user := func(u *auth.User) func() (*auth.User, error) {
		return func() (*auth.User, error) { return u, nil }
	}
	off := func(v bool) func() bool { return func() bool { return v } }

	It("allows an active administrator while the switch is on, and a server without accounts", func() {
		Expect(assistantAllowedNow(off(false), user(admin))()).To(Succeed())
		Expect(assistantAllowedNow(off(false), nil)()).To(Succeed())
	})

	It("refuses when the switch is turned off during the session", func() {
		Expect(assistantAllowedNow(off(true), user(admin))()).To(MatchError(ContainSubstring("switched off")))
		Expect(assistantAllowedNow(off(true), nil)()).To(MatchError(ContainSubstring("switched off")))
	})

	It("refuses when the role was revoked, the account is not active or it is gone", func() {
		Expect(assistantAllowedNow(off(false), user(&auth.User{ID: "a", Role: auth.RoleUser, Status: auth.StatusActive}))()).To(HaveOccurred())
		Expect(assistantAllowedNow(off(false), user(&auth.User{ID: "a", Role: auth.RoleAdmin, Status: "pending"}))()).To(HaveOccurred())
		Expect(assistantAllowedNow(off(false), func() (*auth.User, error) { return nil, errors.New("gone") })()).To(HaveOccurred())
	})

	It("stops a tool call at the executor when the check fails", func() {
		inner := &countingExec{}
		e := &stillAllowedExecutor{ToolExecutor: inner, check: assistantAllowedNow(off(true), nil)}
		_, err := e.ExecuteTool(context.Background(), "t", "{}")
		Expect(err).To(HaveOccurred())
		Expect(inner.n).To(BeZero())
	})
})

type countingExec struct {
	mcpTools.ToolExecutor
	n int
}

func (c *countingExec) ExecuteTool(context.Context, string, string) (string, error) {
	c.n++
	return "ok", nil
}
