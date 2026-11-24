package auth_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("User impersonation helpers", func() {
	ctx := func(query string, user *auth.User) echo.Context {
		req := httptest.NewRequest(http.MethodGet, "/x"+query, nil)
		c := echo.New().NewContext(req, httptest.NewRecorder())
		if user != nil {
			c.Set("auth_user", user)
		}
		return c
	}

	It("lets only an administrator impersonate", func() {
		Expect(auth.CanImpersonate(ctx("", nil))).To(BeFalse())
		Expect(auth.CanImpersonate(ctx("", &auth.User{ID: "alice", Role: auth.RoleUser}))).To(BeFalse())
		Expect(auth.CanImpersonate(ctx("", &auth.User{ID: "root", Role: auth.RoleAdmin}))).To(BeTrue())
	})

	It("scopes an administrator to the user_id and anyone else to themselves", func() {
		admin := &auth.User{ID: "root", Role: auth.RoleAdmin}
		user := &auth.User{ID: "alice", Role: auth.RoleUser}
		Expect(auth.EffectiveUserID(ctx("?user_id=bob", admin))).To(Equal("bob"))
		Expect(auth.EffectiveUserID(ctx("", admin))).To(Equal("root"))
		Expect(auth.EffectiveUserID(ctx("?user_id=bob", user))).To(Equal("alice"))
		Expect(auth.EffectiveUserID(ctx("?user_id=bob", nil))).To(Equal(""))
	})
})
