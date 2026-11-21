package workerregistry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The shutdown of a worker", func() {
	It("drains, waits for the requests in flight, and deregisters", func() {
		var (
			mu    sync.Mutex
			calls []string
			polls int
		)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			Expect(r.Header.Get("Authorization")).To(Equal("Bearer tok"))
			calls = append(calls, r.Method+" "+r.URL.Path)
			if r.URL.Path == "/api/node/n1/models" {
				polls++
				if polls == 1 {
					_, _ = w.Write([]byte(`[{"in_flight":1}]`))
					return
				}
				_, _ = w.Write([]byte(`[{"in_flight":0}]`))
			}
		}))
		DeferCleanup(srv.Close)
		c := &RegistrationClient{FrontendURL: srv.URL + "/", RegistrationToken: "tok"}
		c.GracefulDeregister("n1")
		mu.Lock()
		defer mu.Unlock()
		Expect(calls).To(Equal([]string{
			"POST /api/node/n1/drain",
			"GET /api/node/n1/models",
			"GET /api/node/n1/models",
			"POST /api/node/n1/deregister",
		}))
	})

	It("reports a refused drain and a refused deregistration", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
		DeferCleanup(srv.Close)
		c := &RegistrationClient{FrontendURL: srv.URL}
		Expect(c.Drain(context.Background(), "n1")).To(MatchError(ContainSubstring("403")))
		Expect(c.Deregister(context.Background(), "n1")).To(MatchError(ContainSubstring("403")))
		_, err := c.RegisterFull(context.Background(), map[string]any{"name": "w"})
		Expect(err).To(MatchError(ContainSubstring("403")))
		_, err = c.RegisterFullWithRetry(context.Background(), map[string]any{"name": "w"}, 1)
		Expect(err).To(HaveOccurred())
		_, err = c.RegisterFullWithRetry(context.Background(), nil, 0)
		Expect(err).To(MatchError(ContainSubstring("at least one")))
	})

	It("does nothing without a frontend or a node", func() {
		(&RegistrationClient{}).GracefulDeregister("n1")
		(&RegistrationClient{FrontendURL: "http://127.0.0.1:1"}).GracefulDeregister("")
	})
})
