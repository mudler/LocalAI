package localai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/connections"
	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const connTestKey = "sk-test-0123456789"

// stubConnections is a connections.Service whose methods are set per spec.
// It records what the handlers pass in.
type stubConnections struct {
	test    func(kind connections.Kind, url, key string) (connections.TestResult, error)
	create  func(r connections.CreateRequest) (connections.Result, error)
	list    func() ([]connections.Connection, error)
	get     func(id string) (connections.Connection, error)
	update  func(id string, r connections.UpdateRequest) (connections.Connection, error)
	delete  func(id string, remove bool) error
	refresh func(id string) (connections.Connection, error)
	adopt   func(id string, m []connections.UpstreamModel) (connections.Result, error)

	calls []string
	got   any
}

func (s *stubConnections) Test(_ context.Context, k connections.Kind, url, key string) (connections.TestResult, error) {
	s.calls = append(s.calls, "test")
	s.got = []string{string(k), url, key}
	return s.test(k, url, key)
}
func (s *stubConnections) Create(_ context.Context, r connections.CreateRequest) (connections.Result, error) {
	s.calls = append(s.calls, "create")
	s.got = r
	return s.create(r)
}
func (s *stubConnections) List(context.Context) ([]connections.Connection, error) {
	s.calls = append(s.calls, "list")
	return s.list()
}
func (s *stubConnections) Get(_ context.Context, id string) (connections.Connection, error) {
	s.calls = append(s.calls, "get")
	return s.get(id)
}
func (s *stubConnections) Update(_ context.Context, id string, r connections.UpdateRequest) (connections.Connection, error) {
	s.calls = append(s.calls, "update")
	s.got = r
	return s.update(id, r)
}
func (s *stubConnections) Delete(_ context.Context, id string, remove bool) error {
	s.calls = append(s.calls, "delete")
	s.got = remove
	return s.delete(id, remove)
}
func (s *stubConnections) Refresh(_ context.Context, id string) (connections.Connection, error) {
	s.calls = append(s.calls, "refresh")
	return s.refresh(id)
}
func (s *stubConnections) Adopt(_ context.Context, id string, m []connections.UpstreamModel) (connections.Result, error) {
	s.calls = append(s.calls, "adopt")
	s.got = m
	return s.adopt(id, m)
}
func (s *stubConnections) ResolveKey(context.Context, string) (string, error) {
	return "", errors.New("never called over HTTP")
}

var _ = Describe("remote connection endpoints", func() {
	var (
		e     *echo.Echo
		stub  *stubConnections
		nilFn bool
		logs  *bytes.Buffer
		conn  connections.Connection
	)

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}
	errorOf := func(rec *httptest.ResponseRecorder) schema.APIError {
		var resp schema.ErrorResponse
		ExpectWithOffset(1, json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed(), rec.Body.String())
		ExpectWithOffset(1, resp.Error).NotTo(BeNil())
		return *resp.Error
	}

	BeforeEach(func() {
		nilFn = false
		conn = connections.Connection{ID: "c1", Name: "home", Kind: connections.KindLocalAI, URL: "https://example.com", KeySet: true,
			Status: connections.StatusOK, Entries: []string{"home-a"}, Unavailable: []string{}, New: []connections.UpstreamModel{{ID: "m2", Usecases: []string{"chat"}}}}
		stub = &stubConnections{}
		logs = &bytes.Buffer{}
		xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}), xlog.LogLevelDebug))
		DeferCleanup(func() { xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text")) })

		get := ConnectionsProvider(func() connections.Service {
			if nilFn {
				return nil
			}
			return stub
		})
		e = echo.New()
		e.POST("/api/connections/test", CheckConnectionEndpoint(get))
		e.POST("/api/connections", CreateConnectionEndpoint(get))
		e.GET("/api/connections", ListConnectionsEndpoint(get))
		e.GET("/api/connections/:id", GetConnectionEndpoint(get))
		e.PATCH("/api/connections/:id", UpdateConnectionEndpoint(get))
		e.DELETE("/api/connections/:id", DeleteConnectionEndpoint(get))
		e.POST("/api/connections/:id/refresh", RefreshConnectionEndpoint(get))
		e.POST("/api/connections/:id/adopt", AdoptConnectionModelsEndpoint(get))
	})

	Describe("success", func() {
		It("tests a remote and answers 200 with the result", func() {
			stub.test = func(connections.Kind, string, string) (connections.TestResult, error) {
				return connections.TestResult{OK: true, Models: []connections.UpstreamModel{{ID: "m1", Usecases: []string{"chat"}}}}, nil
			}
			rec := do(http.MethodPost, "/api/connections/test", fmt.Sprintf(`{"kind":"localai","url":"https://example.com","api_key":%q}`, connTestKey))
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(stub.got).To(Equal([]string{"localai", "https://example.com", connTestKey}))
			Expect(rec.Body.String()).To(MatchJSON(`{"ok":true,"models":[{"id":"m1","usecases":["chat"]}]}`))
		})

		It("answers 200 for a failed test", func() {
			stub.test = func(connections.Kind, string, string) (connections.TestResult, error) {
				return connections.TestResult{OK: false, Step: connections.StepAuth, Message: "the remote refused the key"}, nil
			}
			rec := do(http.MethodPost, "/api/connections/test", `{"kind":"openai","url":"https://example.com/v1","api_key":"x"}`)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Body.String()).To(MatchJSON(`{"ok":false,"step":"auth","message":"the remote refused the key"}`))
		})

		It("creates a connection and answers 201 with created and skipped", func() {
			stub.create = func(r connections.CreateRequest) (connections.Result, error) {
				return connections.Result{Connection: conn, Created: []string{"home-a"}, Skipped: []connections.Skip{{ID: "m9", Reason: "unsupported"}}}, nil
			}
			rec := do(http.MethodPost, "/api/connections", fmt.Sprintf(`{"kind":"localai","name":"home","url":"https://example.com","api_key":%q,"models":[{"id":"m1","usecases":["chat"]}]}`, connTestKey))
			Expect(rec.Code).To(Equal(http.StatusCreated))
			Expect(stub.got).To(Equal(connections.CreateRequest{Kind: connections.KindLocalAI, Name: "home", URL: "https://example.com", APIKey: connTestKey,
				Models: []connections.UpstreamModel{{ID: "m1", Usecases: []string{"chat"}}}}))
			var resp map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp).To(HaveKey("connection"))
			Expect(resp["created"]).To(Equal([]any{"home-a"}))
			Expect(resp["skipped"]).To(Equal([]any{map[string]any{"id": "m9", "reason": "unsupported"}}))
		})

		It("answers created and skipped as arrays when they are empty", func() {
			stub.create = func(connections.CreateRequest) (connections.Result, error) {
				return connections.Result{Connection: conn}, nil
			}
			rec := do(http.MethodPost, "/api/connections", `{"kind":"localai","name":"home","url":"https://example.com"}`)
			Expect(rec.Code).To(Equal(http.StatusCreated))
			Expect(rec.Body.String()).To(ContainSubstring(`"created":[]`))
			Expect(rec.Body.String()).To(ContainSubstring(`"skipped":[]`))
		})

		It("lists connections in data, never null", func() {
			stub.list = func() ([]connections.Connection, error) { return nil, nil }
			rec := do(http.MethodGet, "/api/connections", "")
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Body.String()).To(MatchJSON(`{"data":[]}`))

			stub.list = func() ([]connections.Connection, error) { return []connections.Connection{conn}, nil }
			rec = do(http.MethodGet, "/api/connections", "")
			var resp ConnectionListResponse
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.Data).To(HaveLen(1))
			Expect(resp.Data[0].ID).To(Equal("c1"))
		})

		It("gets one connection by id", func() {
			stub.get = func(id string) (connections.Connection, error) {
				Expect(id).To(Equal("c1"))
				return conn, nil
			}
			rec := do(http.MethodGet, "/api/connections/c1", "")
			Expect(rec.Code).To(Equal(http.StatusOK))
			var got connections.Connection
			Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
			Expect(got.Name).To(Equal("home"))
			Expect(got.KeySet).To(BeTrue())
		})

		It("keeps an absent field of a PATCH as nil and an empty api_key as an empty string", func() {
			stub.update = func(string, connections.UpdateRequest) (connections.Connection, error) { return conn, nil }
			rec := do(http.MethodPatch, "/api/connections/c1", `{"name":"office"}`)
			Expect(rec.Code).To(Equal(http.StatusOK))
			r := stub.got.(connections.UpdateRequest)
			Expect(r.Name).To(HaveValue(Equal("office")))
			Expect(r.URL).To(BeNil())
			Expect(r.APIKey).To(BeNil())

			rec = do(http.MethodPatch, "/api/connections/c1", `{"api_key":""}`)
			Expect(rec.Code).To(Equal(http.StatusOK))
			r = stub.got.(connections.UpdateRequest)
			Expect(r.Name).To(BeNil())
			Expect(r.APIKey).To(HaveValue(Equal("")))
		})

		It("deletes with entries=keep by default and answers 204", func() {
			stub.delete = func(string, bool) error { return nil }
			rec := do(http.MethodDelete, "/api/connections/c1", "")
			Expect(rec.Code).To(Equal(http.StatusNoContent))
			Expect(rec.Body.Len()).To(BeZero())
			Expect(stub.got).To(BeFalse())

			rec = do(http.MethodDelete, "/api/connections/c1?entries=keep", "")
			Expect(rec.Code).To(Equal(http.StatusNoContent))
			Expect(stub.got).To(BeFalse())
		})

		It("deletes the entries with entries=remove", func() {
			stub.delete = func(string, bool) error { return nil }
			rec := do(http.MethodDelete, "/api/connections/c1?entries=remove", "")
			Expect(rec.Code).To(Equal(http.StatusNoContent))
			Expect(stub.got).To(BeTrue())
		})

		It("refreshes a connection", func() {
			stub.refresh = func(id string) (connections.Connection, error) { return conn, nil }
			rec := do(http.MethodPost, "/api/connections/c1/refresh", "")
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Body.String()).To(ContainSubstring(`"new":[{"id":"m2","usecases":["chat"]}]`))
		})

		It("adopts models and answers 200", func() {
			stub.adopt = func(string, []connections.UpstreamModel) (connections.Result, error) {
				return connections.Result{Connection: conn, Created: []string{"home-b"}}, nil
			}
			rec := do(http.MethodPost, "/api/connections/c1/adopt", `{"models":[{"id":"m2","usecases":["chat"]}]}`)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(stub.got).To(Equal([]connections.UpstreamModel{{ID: "m2", Usecases: []string{"chat"}}}))
			Expect(rec.Body.String()).To(ContainSubstring(`"created":["home-b"]`))
		})
	})

	Describe("error mapping", func() {
		// Every route, driven with each service error.
		type route struct {
			name, method, path, body string
			fail                     func(err error)
		}
		routes := func() []route {
			return []route{
				{"test", http.MethodPost, "/api/connections/test", `{"kind":"openai"}`, func(err error) {
					stub.test = func(connections.Kind, string, string) (connections.TestResult, error) {
						return connections.TestResult{}, err
					}
				}},
				{"create", http.MethodPost, "/api/connections", `{"kind":"openai"}`, func(err error) {
					stub.create = func(connections.CreateRequest) (connections.Result, error) { return connections.Result{}, err }
				}},
				{"list", http.MethodGet, "/api/connections", "", func(err error) {
					stub.list = func() ([]connections.Connection, error) { return nil, err }
				}},
				{"get", http.MethodGet, "/api/connections/c1", "", func(err error) {
					stub.get = func(string) (connections.Connection, error) { return connections.Connection{}, err }
				}},
				{"update", http.MethodPatch, "/api/connections/c1", `{}`, func(err error) {
					stub.update = func(string, connections.UpdateRequest) (connections.Connection, error) {
						return connections.Connection{}, err
					}
				}},
				{"delete", http.MethodDelete, "/api/connections/c1", "", func(err error) {
					stub.delete = func(string, bool) error { return err }
				}},
				{"refresh", http.MethodPost, "/api/connections/c1/refresh", "", func(err error) {
					stub.refresh = func(string) (connections.Connection, error) { return connections.Connection{}, err }
				}},
				{"adopt", http.MethodPost, "/api/connections/c1/adopt", `{}`, func(err error) {
					stub.adopt = func(string, []connections.UpstreamModel) (connections.Result, error) {
						return connections.Result{}, err
					}
				}},
			}
		}

		It("maps ErrInvalid to 400 and shows its message", func() {
			for _, r := range routes() {
				r.fail(&connections.InvalidError{Message: "name must not be empty"})
				rec := do(r.method, r.path, r.body)
				Expect(rec.Code).To(Equal(http.StatusBadRequest), r.name)
				Expect(errorOf(rec).Message).To(Equal("name must not be empty"), r.name)
				Expect(errorOf(rec).Code).To(BeEquivalentTo(http.StatusBadRequest), r.name)
			}
		})

		It("maps a wrapped ErrInvalid without a message to 400", func() {
			stub.get = func(string) (connections.Connection, error) {
				return connections.Connection{}, fmt.Errorf("x: %w", connections.ErrInvalid)
			}
			Expect(do(http.MethodGet, "/api/connections/c1", "").Code).To(Equal(http.StatusBadRequest))
		})

		It("maps ErrNotFound to 404", func() {
			for _, r := range routes() {
				r.fail(fmt.Errorf("lookup: %w", connections.ErrNotFound))
				Expect(do(r.method, r.path, r.body).Code).To(Equal(http.StatusNotFound), r.name)
			}
		})

		It("maps ErrConflict to 409", func() {
			for _, r := range routes() {
				r.fail(fmt.Errorf("%w: %q", connections.ErrConflict, "home"))
				Expect(do(r.method, r.path, r.body).Code).To(Equal(http.StatusConflict), r.name)
			}
		})

		It("maps any other error to 500 with a generic message and logs the detail", func() {
			for _, r := range routes() {
				logs.Reset()
				r.fail(errors.New("store exploded: disk /srv/state/connections.json"))
				rec := do(r.method, r.path, r.body)
				Expect(rec.Code).To(Equal(http.StatusInternalServerError), r.name)
				Expect(rec.Body.String()).NotTo(ContainSubstring("store exploded"), r.name)
				Expect(rec.Body.String()).NotTo(ContainSubstring("/srv/state"), r.name)
				Expect(errorOf(rec).Message).NotTo(BeEmpty(), r.name)
				Expect(logs.String()).To(ContainSubstring("store exploded"), r.name)
			}
		})

		It("answers a partial update failure with an error status, not the saved connection", func() {
			stub.update = func(string, connections.UpdateRequest) (connections.Connection, error) {
				return conn, errors.New("some entries could not be updated: home-a: disk full")
			}
			rec := do(http.MethodPatch, "/api/connections/c1", `{"url":"https://example.org"}`)
			Expect(rec.Code).To(Equal(http.StatusInternalServerError))
			Expect(errorOf(rec).Message).To(ContainSubstring("saved"))
			Expect(rec.Body.String()).NotTo(ContainSubstring("disk full"))
			Expect(rec.Body.String()).NotTo(ContainSubstring(`"entries"`))
		})

		It("answers a partial delete failure with an error status", func() {
			stub.delete = func(string, bool) error { return errors.New("not every entry could be handled: home-a: busy") }
			rec := do(http.MethodDelete, "/api/connections/c1?entries=remove", "")
			Expect(rec.Code).To(Equal(http.StatusInternalServerError))
			Expect(rec.Body.String()).NotTo(ContainSubstring("busy"))
		})
	})

	Describe("an unavailable service", func() {
		It("answers 503 on every route and calls nothing", func() {
			nilFn = true
			for _, r := range []struct{ method, path, body string }{
				{http.MethodPost, "/api/connections/test", `{}`},
				{http.MethodPost, "/api/connections", `{}`},
				{http.MethodGet, "/api/connections", ""},
				{http.MethodGet, "/api/connections/c1", ""},
				{http.MethodPatch, "/api/connections/c1", `{}`},
				{http.MethodDelete, "/api/connections/c1", ""},
				{http.MethodPost, "/api/connections/c1/refresh", ""},
				{http.MethodPost, "/api/connections/c1/adopt", `{}`},
			} {
				rec := do(r.method, r.path, r.body)
				Expect(rec.Code).To(Equal(http.StatusServiceUnavailable), r.method+" "+r.path)
				Expect(errorOf(rec).Message).To(ContainSubstring("unavailable"))
			}
			Expect(stub.calls).To(BeEmpty())
		})

		It("answers 503 when the provider itself is nil", func() {
			e = echo.New()
			e.GET("/api/connections", ListConnectionsEndpoint(nil))
			Expect(do(http.MethodGet, "/api/connections", "").Code).To(Equal(http.StatusServiceUnavailable))
		})
	})

	Describe("request validation", func() {
		bodyRoutes := []struct{ method, path string }{
			{http.MethodPost, "/api/connections/test"},
			{http.MethodPost, "/api/connections"},
			{http.MethodPatch, "/api/connections/c1"},
			{http.MethodPost, "/api/connections/c1/adopt"},
		}

		It("rejects a body that is not JSON with 400 and calls nothing", func() {
			for _, r := range bodyRoutes {
				for _, body := range []string{"not json", "", `["a"]`, `{"name":`, `{"name":"a"} {"name":"b"}`, `{"unknown_field":1}`} {
					rec := do(r.method, r.path, body)
					Expect(rec.Code).To(Equal(http.StatusBadRequest), r.method+" "+r.path+" "+body)
					Expect(errorOf(rec).Message).NotTo(BeEmpty())
				}
			}
			Expect(stub.calls).To(BeEmpty())
		})

		It("rejects a body over the limit with 413 and calls nothing", func() {
			big := fmt.Sprintf(`{"name":%q}`, strings.Repeat("a", connectionBodyLimit+1))
			for _, r := range bodyRoutes {
				rec := do(r.method, r.path, big)
				Expect(rec.Code).To(Equal(http.StatusRequestEntityTooLarge), r.method+" "+r.path)
			}
			Expect(stub.calls).To(BeEmpty())
		})

		It("accepts a body just under the limit", func() {
			stub.update = func(string, connections.UpdateRequest) (connections.Connection, error) { return conn, nil }
			ok := fmt.Sprintf(`{"name":%q}`, strings.Repeat("a", connectionBodyLimit-100))
			Expect(do(http.MethodPatch, "/api/connections/c1", ok).Code).To(Equal(http.StatusOK))
		})

		It("rejects an entries value other than keep or remove with 400 and calls nothing", func() {
			for _, v := range []string{"all", "REMOVE", "true", "1", "%20"} {
				rec := do(http.MethodDelete, "/api/connections/c1?entries="+v, "")
				Expect(rec.Code).To(Equal(http.StatusBadRequest), v)
				Expect(errorOf(rec).Message).To(ContainSubstring("entries"))
			}
			Expect(stub.calls).To(BeEmpty())
		})
	})

	Describe("the API key", func() {
		It("is in no response body and no log line, on success or failure", func() {
			stub.test = func(connections.Kind, string, string) (connections.TestResult, error) {
				return connections.TestResult{OK: true}, nil
			}
			stub.create = func(connections.CreateRequest) (connections.Result, error) {
				return connections.Result{Connection: conn}, nil
			}
			stub.update = func(string, connections.UpdateRequest) (connections.Connection, error) { return conn, nil }
			stub.get = func(string) (connections.Connection, error) { return conn, nil }

			var bodies []string
			keyJSON := fmt.Sprintf(`"api_key":%q`, connTestKey)
			for _, r := range []struct{ method, path, body string }{
				{http.MethodPost, "/api/connections/test", `{"kind":"openai",` + keyJSON + `}`},
				{http.MethodPost, "/api/connections", `{"kind":"openai","name":"n",` + keyJSON + `}`},
				{http.MethodPatch, "/api/connections/c1", `{` + keyJSON + `}`},
				{http.MethodGet, "/api/connections/c1", ``},
				// A body that fails to decode, with the key in it.
				{http.MethodPatch, "/api/connections/c1", `{` + keyJSON + `,"bad":}`},
				{http.MethodPatch, "/api/connections/c1", `{` + keyJSON + `,"unknown":1}`},
				{http.MethodPost, "/api/connections", `{` + keyJSON + `,"models":"x"}`},
			} {
				bodies = append(bodies, do(r.method, r.path, r.body).Body.String())
			}

			// A failing service, and a partial failure.
			stub.create = func(connections.CreateRequest) (connections.Result, error) {
				return connections.Result{}, errors.New("upstream said no")
			}
			bodies = append(bodies, do(http.MethodPost, "/api/connections", `{"kind":"openai","name":"n",`+keyJSON+`}`).Body.String())
			stub.update = func(string, connections.UpdateRequest) (connections.Connection, error) {
				return conn, errors.New("partial")
			}
			bodies = append(bodies, do(http.MethodPatch, "/api/connections/c1", `{`+keyJSON+`}`).Body.String())

			for _, b := range bodies {
				Expect(b).NotTo(ContainSubstring(connTestKey))
			}
			Expect(logs.String()).NotTo(ContainSubstring(connTestKey))
			Expect(logs.String()).NotTo(BeEmpty(), "the failures were logged")
		})

		It("is not a field of any response type", func() {
			b, err := json.Marshal(ConnectionResultResponse{Connection: conn})
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring("api_key"))
			b, err = json.Marshal(conn)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(b)).NotTo(ContainSubstring("api_key"))
		})
	})
})
