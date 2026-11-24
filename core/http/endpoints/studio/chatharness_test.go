package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// These specs read the window check of a chat through GET /api/chats/{id},
// the way the chat page does, for an administrator and a regular user.

var _ = Describe("GET /api/chats/{id} harness", func() {
	const admin, member = "root", "bob"

	// window is what the model reports as its context window, in tokens.
	rigFor := func(window int) (*studiosvc.Service, *echo.Echo) {
		h := &studiotest.ToolHarness{Script: func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "ok"}}
		}}
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := studiosvc.New(studiosvc.Deps{
			Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h,
			IsAdmin:       func(_ context.Context, id string) bool { return id == admin },
			ContextTokens: func(string) int { return window },
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })

		e := echo.New()
		e.GET("/api/chats/:id", New(svc, nil).Get)
		return svc, e
	}

	// get opens a chat of user and reads it back as that user.
	get := func(window int, user string) (int, schema.ChatsConversationDetail) {
		svc, e := rigFor(window)
		rec, err := svc.Create(context.Background(), user, studiosvc.CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodGet, "/api/chats/"+rec.ID, nil)
		w := httptest.NewRecorder()
		role := auth.RoleUser
		if user == admin {
			role = auth.RoleAdmin
		}
		c := e.NewContext(req, w)
		c.Set("auth_user", &auth.User{ID: user, Role: role})
		c.SetParamNames("id")
		c.SetParamValues(rec.ID)
		Expect(New(svc, nil).Get(c)).To(Succeed())

		var out schema.ChatsConversationDetail
		if w.Code == http.StatusOK {
			Expect(json.Unmarshal(w.Body.Bytes(), &out)).To(Succeed(), w.Body.String())
		}
		return w.Code, out
	}

	It("tells a regular user the model is too small and that only an administrator can fix it", func() {
		code, out := get(8192, member)
		Expect(code).To(Equal(http.StatusOK))
		Expect(out.Harness).NotTo(BeNil())
		Expect(*out.Harness).To(Equal(schema.ChatsHarness{
			OK: false, Window: 8192, Minimum: 16384, Recommended: 32768,
			CanFix: false, Admin: false, Source: "model",
		}))
	})

	It("tells an administrator the same, with their own minimum, and that they can fix it", func() {
		code, out := get(16384, admin)
		Expect(code).To(Equal(http.StatusOK))
		Expect(out.Harness).NotTo(BeNil())
		Expect(*out.Harness).To(Equal(schema.ChatsHarness{
			OK: false, Window: 16384, Minimum: 24576, Recommended: 32768,
			CanFix: true, Admin: true, Source: "model",
		}))
	})

	It("reports a large enough window as fine, for both roles", func() {
		for _, user := range []string{member, admin} {
			code, out := get(32768, user)
			Expect(code).To(Equal(http.StatusOK), user)
			Expect(out.Harness).NotTo(BeNil(), user)
			Expect(out.Harness.OK).To(BeTrue(), user)
			Expect(out.Harness.Source).To(BeEmpty(), user)
		}
	})

	It("carries the check under the harness key of the JSON body", func() {
		svc, e := rigFor(8192)
		rec, err := svc.Create(context.Background(), member, studiosvc.CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.Set("auth_user", &auth.User{ID: member, Role: auth.RoleUser})
				return next(c)
			}
		})
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/chats/"+rec.ID, nil))
		Expect(w.Code).To(Equal(http.StatusOK))
		var raw map[string]json.RawMessage
		Expect(json.Unmarshal(w.Body.Bytes(), &raw)).To(Succeed())
		Expect(raw).To(HaveKey("harness"))
		Expect(string(raw["harness"])).To(ContainSubstring(`"can_fix":false`))
	})
})
