package localai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/remotes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// stubFederation answers with the error set per spec and records the
// requests.
type stubFederation struct {
	err     error
	created remotes.GroupRequest
	updated remotes.GroupUpdate
	drained []any
}

func (s *stubFederation) View(context.Context) (remotes.FederationView, error) {
	return remotes.FederationView{Instances: []remotes.Instance{{ID: remotes.LocalInstance, Local: true}}, Groups: []remotes.Group{}}, s.err
}
func (s *stubFederation) Group(_ context.Context, name string) (remotes.Group, error) {
	return remotes.Group{Name: name}, s.err
}
func (s *stubFederation) CreateGroup(_ context.Context, r remotes.GroupRequest) (remotes.Group, error) {
	s.created = r
	return remotes.Group{Name: r.Name, Policy: r.Policy}, s.err
}
func (s *stubFederation) UpdateGroup(_ context.Context, name string, r remotes.GroupUpdate) (remotes.Group, error) {
	s.updated = r
	return remotes.Group{Name: name}, s.err
}
func (s *stubFederation) DeleteGroup(context.Context, string) error { return s.err }
func (s *stubFederation) Drain(_ context.Context, group, model string, drain bool) (remotes.Group, error) {
	s.drained = []any{group, model, drain}
	return remotes.Group{Name: group}, s.err
}

var _ = Describe("Federation endpoints", func() {
	var (
		e    *echo.Echo
		stub *stubFederation
	)
	BeforeEach(func() {
		stub = &stubFederation{}
		get := func() remotes.Federation { return stub }
		e = echo.New()
		e.GET("/api/federation", GetFederationEndpoint(get))
		e.POST("/api/federation/groups", CreateFederationGroupEndpoint(get))
		e.GET("/api/federation/groups/:name", GetFederationGroupEndpoint(get))
		e.PATCH("/api/federation/groups/:name", UpdateFederationGroupEndpoint(get))
		e.DELETE("/api/federation/groups/:name", DeleteFederationGroupEndpoint(get))
		e.POST("/api/federation/groups/:name/drain", DrainFederationMemberEndpoint(get))
		e.GET("/none", GetFederationEndpoint(func() remotes.Federation { return nil }))
	})
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	It("passes a create, an update and a drain to the service", func() {
		rec := do(http.MethodPost, "/api/federation/groups", `{"name":"g","policy":"round_robin","members":["a","b"]}`)
		Expect(rec.Code).To(Equal(http.StatusCreated))
		Expect(stub.created).To(Equal(remotes.GroupRequest{Name: "g", Policy: config.FailoverPolicyRoundRobin, Members: []string{"a", "b"}}))

		rec = do(http.MethodPatch, "/api/federation/groups/g", `{"members":["a","c"]}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(stub.updated.Policy).To(BeNil())
		Expect(*stub.updated.Members).To(Equal([]string{"a", "c"}))

		rec = do(http.MethodPost, "/api/federation/groups/g/drain", `{"model":"a","drain":true}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(stub.drained).To(Equal([]any{"g", "a", true}))

		Expect(do(http.MethodDelete, "/api/federation/groups/g", "").Code).To(Equal(http.StatusNoContent))
		rec = do(http.MethodGet, "/api/federation", "")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"id":"local"`))
	})

	DescribeTable("maps service errors to status codes",
		func(err error, code int, text string) {
			stub.err = err
			rec := do(http.MethodGet, "/api/federation/groups/g", "")
			Expect(rec.Code).To(Equal(code))
			Expect(rec.Body.String()).To(ContainSubstring(text))
		},
		Entry("invalid input shows its message", &remotes.InvalidError{Message: "A group needs at least two models."}, http.StatusBadRequest, "at least two"),
		Entry("not a group", fmt.Errorf("%w: %q", remotes.ErrGroupNotFound, "g"), http.StatusNotFound, "not found"),
		Entry("a name in use", remotes.ErrConflict, http.StatusConflict, "already exists"),
		Entry("anything else is not shown", errors.New("disk on fire"), http.StatusInternalServerError, "see the server log"),
	)

	It("refuses an unknown field and answers 503 without a service", func() {
		Expect(do(http.MethodPost, "/api/federation/groups", `{"name":"g","members":["a","b"],"weight":2}`).Code).To(Equal(http.StatusBadRequest))
		Expect(do(http.MethodGet, "/none", "").Code).To(Equal(http.StatusServiceUnavailable))
	})
})
