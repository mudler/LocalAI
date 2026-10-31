package localai

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Per-user isolation contract for the collections endpoints: a regular user is scoped
// to their own data, only admins can override
// scope via ?user_id=, and ?all_users=true is admin-only.
var _ = Describe("Collections endpoint per-user isolation", func() {
	var e *echo.Echo

	makeContext := func(query string) echo.Context {
		req := httptest.NewRequest(http.MethodGet, "/api/collections"+query, nil)
		rec := httptest.NewRecorder()
		return e.NewContext(req, rec)
	}

	BeforeEach(func() {
		e = echo.New()
	})

	Describe("getUserID", func() {
		It("returns empty when no user is in context", func() {
			c := makeContext("")
			Expect(getUserID(c)).To(Equal(""))
		})

		It("returns the authenticated user's ID", func() {
			c := makeContext("")
			c.Set("auth_user", &auth.User{ID: "alice", Role: auth.RoleUser})
			Expect(getUserID(c)).To(Equal("alice"))
		})
	})

	Describe("isAdminUser", func() {
		It("is false when no user is in context", func() {
			c := makeContext("")
			Expect(isAdminUser(c)).To(BeFalse())
		})

		It("is false for a regular user", func() {
			c := makeContext("")
			c.Set("auth_user", &auth.User{ID: "alice", Role: auth.RoleUser})
			Expect(isAdminUser(c)).To(BeFalse())
		})

		It("is true for admin", func() {
			c := makeContext("")
			c.Set("auth_user", &auth.User{ID: "root", Role: auth.RoleAdmin})
			Expect(isAdminUser(c)).To(BeTrue())
		})
	})

	Describe("canImpersonateUser", func() {
		It("is false for unauthenticated", func() {
			c := makeContext("")
			Expect(canImpersonateUser(c)).To(BeFalse())
		})

		It("is false for a regular user", func() {
			c := makeContext("")
			c.Set("auth_user", &auth.User{ID: "alice", Role: auth.RoleUser})
			Expect(canImpersonateUser(c)).To(BeFalse())
		})

		It("is true for admin", func() {
			c := makeContext("")
			c.Set("auth_user", &auth.User{ID: "root", Role: auth.RoleAdmin})
			Expect(canImpersonateUser(c)).To(BeTrue())
		})

		It("is false for a user with the agent-worker provider", func() {
			// The agent worker no longer calls the HTTP API, so nothing
			// legitimate holds this provider. Rows an earlier release
			// created must not keep a cross-user power.
			c := makeContext("")
			c.Set("auth_user", &auth.User{
				ID: "worker-1", Role: auth.RoleUser,
				Provider: auth.ProviderAgentWorker,
			})
			Expect(canImpersonateUser(c)).To(BeFalse())
		})
	})

	Describe("effectiveUserID", func() {
		It("returns the caller's own ID when no query param is set", func() {
			c := makeContext("")
			c.Set("auth_user", &auth.User{ID: "alice", Role: auth.RoleUser})
			Expect(effectiveUserID(c)).To(Equal("alice"))
		})

		It("ignores ?user_id= for a regular user (no impersonation)", func() {
			c := makeContext("?user_id=bob")
			c.Set("auth_user", &auth.User{ID: "alice", Role: auth.RoleUser})
			Expect(effectiveUserID(c)).To(Equal("alice"),
				"regular user must not be able to scope queries to another user")
		})

		It("ignores ?user_id= for an unauthenticated caller", func() {
			c := makeContext("?user_id=alice")
			// no auth_user set
			Expect(effectiveUserID(c)).To(Equal(""))
		})

		It("honors ?user_id= for admin", func() {
			c := makeContext("?user_id=alice")
			c.Set("auth_user", &auth.User{ID: "root", Role: auth.RoleAdmin})
			Expect(effectiveUserID(c)).To(Equal("alice"))
		})

		It("ignores ?user_id= for a user with the agent-worker provider", func() {
			c := makeContext("?user_id=alice")
			c.Set("auth_user", &auth.User{
				ID: "worker-1", Role: auth.RoleUser,
				Provider: auth.ProviderAgentWorker,
			})
			Expect(effectiveUserID(c)).To(Equal("worker-1"))
		})

		It("falls back to caller's own ID when impersonation is allowed but query is empty", func() {
			c := makeContext("")
			c.Set("auth_user", &auth.User{ID: "root", Role: auth.RoleAdmin})
			Expect(effectiveUserID(c)).To(Equal("root"))
		})
	})

	Describe("wantsAllUsers", func() {
		It("is false when ?all_users is not set", func() {
			c := makeContext("")
			c.Set("auth_user", &auth.User{ID: "root", Role: auth.RoleAdmin})
			Expect(wantsAllUsers(c)).To(BeFalse())
		})

		It("is false for a regular user even with ?all_users=true", func() {
			c := makeContext("?all_users=true")
			c.Set("auth_user", &auth.User{ID: "alice", Role: auth.RoleUser})
			Expect(wantsAllUsers(c)).To(BeFalse(),
				"regular user must not be able to fan out to all users")
		})

		It("is true for admin with ?all_users=true", func() {
			c := makeContext("?all_users=true")
			c.Set("auth_user", &auth.User{ID: "root", Role: auth.RoleAdmin})
			Expect(wantsAllUsers(c)).To(BeTrue())
		})

		It("only accepts the literal 'true' string", func() {
			// Sanity — the query string parser should be strict about the
			// value, otherwise typos and case variants might bypass.
			c := makeContext("?all_users=1")
			c.Set("auth_user", &auth.User{ID: "root", Role: auth.RoleAdmin})
			Expect(wantsAllUsers(c)).To(BeFalse())
		})
	})
})
