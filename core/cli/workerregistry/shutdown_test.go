package workerregistry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

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
		c := &RegistrationClient{FrontendURL: srv.URL + "/", credLoaded: true, nodeToken: "tok"}
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

	It("tells the frontend that the worker shuts down and its loads ended", func() {
		var (
			mu   sync.Mutex
			body map[string]any
		)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			switch r.URL.Path {
			case "/api/node/n1/drain":
				Expect(r.Header.Get("Content-Type")).To(Equal("application/json"))
				Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			case "/api/node/n1/models":
				_, _ = w.Write([]byte(`[]`))
			}
		}))
		DeferCleanup(srv.Close)
		c := &RegistrationClient{FrontendURL: srv.URL}
		c.GracefulShutdown("n1", ShutdownOptions{LoadsEnded: true})
		mu.Lock()
		defer mu.Unlock()
		Expect(body).To(Equal(map[string]any{"shutdown": true, "loads_ended": true}))
	})

	It("bounds the wait for requests in flight by the configured window", func() {
		var deregistered bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/node/n1/models":
				_, _ = w.Write([]byte(`[{"in_flight":3}]`))
			case "/api/node/n1/deregister":
				deregistered = true
			}
		}))
		DeferCleanup(srv.Close)
		c := &RegistrationClient{FrontendURL: srv.URL}
		start := time.Now()
		c.GracefulShutdown("n1", ShutdownOptions{RequestWait: 1500 * time.Millisecond, Timeout: 10 * time.Second})
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
		Expect(deregistered).To(BeTrue(), "the worker deregisters after the window even with requests left")
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
