//go:build auth

package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	clienthttp "github.com/mudler/LocalAI/core/http/endpoints/client"
	"github.com/mudler/LocalAI/core/services/remotes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// remotesAuthStub answers every call with an empty success, so a request
// that passes the middleware gets 2xx and one that does not never reaches it.
type remotesAuthStub struct {
	remotes.Service
	calls int
}

func (s *remotesAuthStub) Test(context.Context, remotes.Kind, string, string) (remotes.TestResult, error) {
	s.calls++
	return remotes.TestResult{OK: true}, nil
}
func (s *remotesAuthStub) Create(context.Context, remotes.CreateRequest) (remotes.Result, error) {
	s.calls++
	return remotes.Result{Remote: remotes.Remote{ID: "r1"}}, nil
}
func (s *remotesAuthStub) List(context.Context) ([]remotes.Remote, error) {
	s.calls++
	return nil, nil
}
func (s *remotesAuthStub) Get(context.Context, string) (remotes.Remote, error) {
	s.calls++
	return remotes.Remote{ID: "r1"}, nil
}
func (s *remotesAuthStub) Update(context.Context, string, remotes.UpdateRequest) (remotes.Remote, error) {
	s.calls++
	return remotes.Remote{ID: "r1"}, nil
}
func (s *remotesAuthStub) Delete(context.Context, string, bool) error {
	s.calls++
	return nil
}
func (s *remotesAuthStub) Refresh(context.Context, string) (remotes.Remote, error) {
	s.calls++
	return remotes.Remote{ID: "r1"}, nil
}
func (s *remotesAuthStub) Adopt(context.Context, string, []remotes.UpstreamModel) (remotes.Result, error) {
	s.calls++
	return remotes.Result{Remote: remotes.Remote{ID: "r1"}}, nil
}

var _ = Describe("remote routes access", func() {
	var (
		e     *echo.Echo
		stub  *remotesAuthStub
		admin string
		user  string
	)
	routes := []struct{ method, path, body string }{
		{http.MethodPost, "/api/remotes/test", `{"kind":"localai","url":"https://example.com"}`},
		{http.MethodPost, "/api/remotes", `{"kind":"localai","name":"n","url":"https://example.com"}`},
		{http.MethodGet, "/api/remotes", ""},
		{http.MethodGet, "/api/remotes/r1", ""},
		{http.MethodPatch, "/api/remotes/r1", `{"name":"x"}`},
		{http.MethodDelete, "/api/remotes/r1", ""},
		{http.MethodPost, "/api/remotes/r1/refresh", ""},
		{http.MethodPost, "/api/remotes/r1/adopt", `{"models":[{"id":"m"}]}`},
	}
	do := func(method, path, token, body string) int {
		req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		return w.Code
	}

	BeforeEach(func() {
		db, err := auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		owner := &auth.User{ID: "owner-1", Email: "o@example.com", Role: auth.RoleAdmin, Status: auth.StatusActive, Provider: auth.ProviderLocal}
		Expect(db.Create(owner).Error).To(Succeed())
		admin, err = auth.CreateSession(db, owner.ID, "")
		Expect(err).ToNot(HaveOccurred())
		plain := &auth.User{ID: "user-1", Email: "u@example.com", Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal}
		Expect(db.Create(plain).Error).To(Succeed())
		user, err = auth.CreateSession(db, plain.ID, "")
		Expect(err).ToNot(HaveOccurred())

		stub = &remotesAuthStub{}
		e = echo.New()
		e.Use(auth.Middleware(db, &config.ApplicationConfig{}))
		e.Use(auth.RequireRouteFeature(db))
		registerClientRoutes(context.Background(), e, db, "", auth.RequireAdmin(), func() clienthttp.Host { return nil }, func(context.Context, string, string) {}, testModelCheck)
		registerRemoteRoutes(e, func() remotes.Service { return stub }, auth.RequireAdmin())
	})

	It("refuses a request without credentials", func() {
		for _, r := range routes {
			Expect(do(r.method, r.path, "", r.body)).To(Equal(http.StatusUnauthorized), r.method+" "+r.path)
		}
		Expect(stub.calls).To(BeZero())
	})

	It("refuses a user who is not an admin", func() {
		for _, r := range routes {
			Expect(do(r.method, r.path, user, r.body)).To(Equal(http.StatusForbidden), r.method+" "+r.path)
		}
		Expect(stub.calls).To(BeZero())
	})

	It("refuses the key of a paired client, even of an admin", func() {
		code, resp := func() (int, map[string]any) {
			req := httptest.NewRequest(http.MethodPost, clientCodesPath, bytes.NewReader([]byte(`{"name":"box","features":["client_self"]}`)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+admin)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, req)
			var out map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &out)
			return w.Code, out
		}()
		Expect(code).To(Equal(http.StatusCreated))
		req := httptest.NewRequest(http.MethodPost, clientPairPath, bytes.NewReader([]byte(`{"code":"`+resp["code"].(string)+`"}`)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(http.StatusCreated))
		var paired map[string]any
		Expect(json.Unmarshal(w.Body.Bytes(), &paired)).To(Succeed())
		key := paired["key"].(string)
		for _, r := range routes {
			Expect(do(r.method, r.path, key, r.body)).To(Equal(http.StatusForbidden), r.method+" "+r.path)
		}
		Expect(stub.calls).To(BeZero())
	})

	It("serves an admin", func() {
		for _, r := range routes {
			Expect(do(r.method, r.path, admin, r.body)).To(BeNumerically("<", 300), r.method+" "+r.path)
		}
		Expect(stub.calls).To(Equal(len(routes)))
	})
})
