// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/remotes"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("remotes wiring", func() {
	const key = "sk-test-wiring-key"

	newApp := func(ctx context.Context, models, data string) *Application {
		state, err := system.GetSystemState(system.WithModelPath(models), system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		app, err := New(config.WithContext(ctx), config.WithSystemState(state), config.DisableMetricsEndpoint, config.WithDataPath(data))
		if app != nil {
			DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		}
		Expect(err).NotTo(HaveOccurred())
		return app
	}

	It("builds the service, writes entries the loader sees, serves the key and refreshes at startup", func() {
		models, data := GinkgoT().TempDir(), GinkgoT().TempDir()
		listed := []string{"one"}
		remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+key {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			items := []map[string]any{}
			for _, id := range listed {
				items = append(items, map[string]any{"id": id})
			}
			if r.URL.Path == "/v1/models" {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
				return
			}
			http.NotFound(w, r)
		}))
		DeferCleanup(remote.Close)
		DeferCleanup(func() { config.SetProxyKeyResolver(nil) })

		ctx1, cancel1 := context.WithCancel(context.Background())
		app := newApp(ctx1, models, data)
		svc := app.Remotes()
		Expect(svc).NotTo(BeNil())
		res, err := svc.Create(context.Background(), remotes.CreateRequest{
			Kind: remotes.KindLocalAI, Name: "Remote", URL: remote.URL, APIKey: key,
			Models: []remotes.UpstreamModel{{ID: "one", Usecases: []string{"chat"}}},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Created).To(Equal([]string{"one"}))
		cfg, ok := app.ModelConfigLoader().GetModelConfig("one")
		Expect(ok).To(BeTrue(), "the new model is visible without a restart")
		Expect(cfg.Proxy.Remote).To(Equal(res.Remote.ID))
		got, err := config.ResolveProxyRemoteKey(context.Background(), res.Remote.ID, cfg.Proxy.UpstreamURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(key))

		// A restart on the same directories: the remote is still there
		// and the startup refresh finds the model that appeared meanwhile.
		cancel1()
		Expect(app.Shutdown()).To(Succeed())
		listed = []string{"one", "two"}
		app2 := newApp(context.Background(), models, data)
		Eventually(func() []string {
			c, err := app2.Remotes().Get(context.Background(), res.Remote.ID)
			Expect(err).NotTo(HaveOccurred())
			var ids []string
			for _, m := range c.New {
				ids = append(ids, m.ID)
			}
			return ids
		}, "10s", "100ms").Should(Equal([]string{"two"}))
	})

	It("stops the startup refresh at shutdown and waits for it", func() {
		models, data := GinkgoT().TempDir(), GinkgoT().TempDir()
		var hang atomic.Bool
		entered := make(chan struct{}, 1)
		remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hang.Load() {
				select {
				case entered <- struct{}{}:
				default:
				}
				<-r.Context().Done()
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "one"}}})
		}))
		DeferCleanup(remote.Close)
		DeferCleanup(func() { config.SetProxyKeyResolver(nil) })

		app := newApp(context.Background(), models, data)
		_, err := app.Remotes().Create(context.Background(), remotes.CreateRequest{Kind: remotes.KindLocalAI, Name: "Remote", URL: remote.URL})
		Expect(err).NotTo(HaveOccurred())
		Expect(app.Shutdown()).To(Succeed())

		hang.Store(true)
		app2 := newApp(context.Background(), models, data)
		Eventually(entered, "10s").Should(Receive())
		start := time.Now()
		Expect(app2.Shutdown()).To(Succeed())
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second), "the refresh was cancelled, not waited out")
	})
})
