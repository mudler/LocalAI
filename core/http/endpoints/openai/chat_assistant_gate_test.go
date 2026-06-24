package openai

import (
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
