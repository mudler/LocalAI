// SPDX-License-Identifier: MIT
package http_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	api "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Diagnostics public API isolation", Serial, func() {
	for _, debug := range []bool{false, true} {
		for _, profiling := range []bool{false, true} {
			for _, timing := range []bool{false, true} {
				It(fmt.Sprintf("keeps pprof private with debug=%t profiling=%t timing=%t", debug, profiling, timing), func() {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					dir := GinkgoT().TempDir()
					modelDir, backendDir := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
					Expect(os.Mkdir(modelDir, 0700)).To(Succeed())
					Expect(os.Mkdir(backendDir, 0700)).To(Succeed())
					state, err := system.GetSystemState(system.WithModelPath(modelDir), system.WithBackendPath(backendDir))
					Expect(err).NotTo(HaveOccurred())
					// Reserve the public address independently; changing the private option must not change it.
					publicListener, err := net.Listen("tcp", "127.0.0.1:0")
					Expect(err).NotTo(HaveOccurred())
					defer publicListener.Close()
					privateReservation, err := net.Listen("tcp", "127.0.0.1:0")
					Expect(err).NotTo(HaveOccurred())
					opts := diagnostics.DefaultOptions()
					opts.Pprof = profiling
					opts.RequestPhaseTiming = timing
					opts.Address = privateReservation.Addr().String()
					Expect(privateReservation.Close()).To(Succeed())
					private, err := diagnostics.Start(opts)
					Expect(err).NotTo(HaveOccurred())
					defer func() {
						c, done := context.WithTimeout(context.Background(), 3*time.Second)
						defer done()
						Expect(private.Shutdown(c)).To(Succeed())
					}()
					var recorder *diagnostics.Recorder
					if timing {
						recorder = diagnostics.NewRecorder(func(diagnostics.Event) {})
					}
					app, err := application.New(config.WithContext(ctx), config.WithSystemState(state), config.WithDebug(debug), config.WithDataPath(filepath.Join(dir, "data")), config.DisableRuntimeSettings, config.DisableAgentPool, config.DisableMCP, config.DisableMetricsEndpoint, config.WithDisableStats(true), config.WithAPIAddress(publicListener.Addr().String()), func(c *config.ApplicationConfig) {
						c.DiagnosticsRecorder = recorder
						c.DisableLocalAIAssistant = true
					})
					Expect(err).NotTo(HaveOccurred())
					defer func() { cancel(); Expect(app.Shutdown()).To(Succeed()) }()
					router, err := api.API(app)
					Expect(err).NotTo(HaveOccurred())
					defer router.Close()
					Expect(app.ApplicationConfig().APIAddress).To(Equal(publicListener.Addr().String()))
					Expect(app.ApplicationConfig().APIAddress).NotTo(Equal(opts.Address))
					for _, route := range router.Routes() {
						Expect(route.Path).NotTo(ContainSubstring("/debug/pprof"))
						Expect(strings.ToLower(route.Name)).NotTo(ContainSubstring("pprof"))
					}
					for _, path := range []string{"/debug/pprof/", "/debug/pprof/profile?seconds=1", "/debug/pprof/heap", "/debug/pprof/cmdline"} {
						for _, accept := range []string{"application/json", "text/html"} {
							req := httptest.NewRequest(http.MethodGet, path, nil)
							req.Header.Set("Accept", accept)
							response := httptest.NewRecorder()
							router.ServeHTTP(response, req)
							Expect(response.Header().Get("Content-Type")).NotTo(ContainSubstring("application/octet-stream"))
							Expect(response.Header().Get("Content-Disposition")).To(BeEmpty())
							Expect(response.Body.String()).NotTo(ContainSubstring("Types of profiles available:"))
							Expect(strings.HasPrefix(response.Body.String(), "\x1f\x8b")).To(BeFalse())
						}
					}
					if !profiling {
						Expect(private.Addr()).To(BeNil())
						return
					}
					Expect(private.Addr().String()).To(Equal(opts.Address))
					client := httpclient.NewWithTimeout(5 * time.Second)
					defer client.CloseIdleConnections()
					for _, path := range []string{"/debug/pprof/", "/debug/pprof/profile?seconds=1", "/debug/pprof/heap", "/debug/pprof/cmdline", "/debug/pprof/cmdline?x=1", "/debug/pprof/cmdline/"} {
						response, err := client.Get("http://" + private.Addr().String() + path)
						Expect(err).NotTo(HaveOccurred())
						body, err := io.ReadAll(response.Body)
						response.Body.Close()
						Expect(err).NotTo(HaveOccurred())
						if strings.Contains(path, "cmdline") {
							Expect(response.StatusCode).To(Equal(http.StatusNotFound))
							continue
						}
						Expect(response.StatusCode).To(Equal(http.StatusOK))
						if path == "/debug/pprof/" {
							Expect(string(body)).To(ContainSubstring("Types of profiles available:"))
						} else {
							Expect(response.Header.Get("Content-Type")).To(Equal("application/octet-stream"))
							Expect(strings.HasPrefix(string(body), "\x1f\x8b")).To(BeTrue())
						}
					}
				})
			}
		}
	}
})
