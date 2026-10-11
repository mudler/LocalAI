// SPDX-License-Identifier: MIT
package middleware_test

import (
	"context"
	"errors"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/pkg/system"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HTTP dispatch boundaries", func() {
	It("finishes auth before downstream for accepted, exempt, disabled and rejected requests", func() {
		for _, mode := range []string{"accepted", "exempt", "disabled", "rejected", "downstream-error"} {
			var events []diagnostics.Event
			ac := config.NewApplicationConfig()
			ac.ApiKeys = []string{"secret"}
			if mode == "disabled" {
				ac.ApiKeys = nil
			}
			path := "/v1/test"
			if mode == "exempt" {
				path = "/exempt/test"
				ac.PathWithoutAuth = []string{"/exempt/"}
			}
			recorder := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) })
			e := echo.New()
			e.Pre(DispatchMiddleware(recorder))
			e.Use(DispatchAuth(auth.Middleware(nil, ac)))
			var downstream time.Time
			e.GET(path, func(c echo.Context) error {
				downstream = time.Now()
				Expect(diagnostics.RequestID(c.Request().Context())).NotTo(BeEmpty())
				Expect(events).To(BeEmpty()) // no sink in measured boundaries
				time.Sleep(10 * time.Millisecond)
				if mode == "downstream-error" {
					return echo.ErrBadGateway
				}
				return c.NoContent(204)
			})
			req := httptest.NewRequest("GET", path, nil)
			req.Header.Set(diagnostics.HeaderDiagnosticID, "spoof")
			if mode == "accepted" || mode == "downstream-error" {
				req.Header.Set("Authorization", "Bearer secret")
			}
			res := httptest.NewRecorder()
			e.ServeHTTP(res, req)
			expected := 204
			if mode == "downstream-error" {
				expected = 502
			}
			if mode == "rejected" {
				expected = 401
			}
			Expect(res.Code).To(Equal(expected), mode)
			Expect(events).To(HaveLen(4))
			Expect(events[0].Phase).To(Equal(diagnostics.PhaseHTTPEntry))
			Expect(events[1].Phase).To(Equal(diagnostics.PhaseAuth))
			Expect(events[2].State).To(Equal(diagnostics.StateEnd))
			Expect(events[3].Phase).To(Equal(diagnostics.PhaseDispatchSummary))
			Expect(res.Header().Get(diagnostics.HeaderDiagnosticID)).To(Equal(events[0].ID))
			Expect(events[0].ID).NotTo(Equal("spoof"))
			if mode == "rejected" {
				Expect(events[2].Outcome).To(Equal(diagnostics.OutcomeError))
			} else {
				Expect(events[2].Outcome).To(Equal(diagnostics.OutcomeOK))
				Expect(events[2].Elapsed).To(BeNumerically("<", time.Since(downstream)))
			}
		}
	})
	It("keeps disabled diagnostics inert and preserves cancellation and errors", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c := echo.New().NewContext(httptest.NewRequest("GET", "/", nil).WithContext(ctx), httptest.NewRecorder())
		h := DispatchMiddleware(nil)(DispatchAuth(auth.NoopMiddleware())(func(c echo.Context) error {
			Expect(c.Request().Context()).To(Equal(ctx))
			return echo.ErrBadGateway
		}))
		Expect(h(c)).To(Equal(echo.ErrBadGateway))
		Expect(c.Response().Header().Get(diagnostics.HeaderDiagnosticID)).To(BeEmpty())
	})
	It("records existing trace reads without consuming an extra body and flushes after stream unwinding", func() {
		root := GinkgoT().TempDir()
		app, err := application.New(config.WithDataPath(root), config.WithDisableStats(true), config.WithDisableLocalAIAssistant(true), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		app.ApplicationConfig().EnableTracing = true
		var events []diagnostics.Event
		r := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) })
		e := echo.New()
		e.Pre(DispatchMiddleware(r))
		e.POST("/trace", func(c echo.Context) error {
			Expect(events).To(BeEmpty())
			body, err := io.ReadAll(c.Request().Body)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(body)).To(Equal(`{"model":"private"}`))
			// A joined streaming producer completes before the handler returns.
			done := make(chan struct{})
			ctx := c.Request().Context()
			go func() {
				diagnostics.Dispatch(ctx).MarkOnce(diagnostics.PhaseFrontendMessage, diagnostics.OutcomeOK)
				close(done)
			}()
			<-done
			c.Response().Header().Set("Content-Type", "text/event-stream")
			_, err = c.Response().Write([]byte("data: {}\n\n"))
			c.Response().Flush()
			return err
		}, TraceMiddleware(app))
		req := httptest.NewRequest("POST", "/trace", strings.NewReader(`{"model":"private"}`))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		e.ServeHTTP(res, req)
		Expect(res.Body.String()).To(Equal("data: {}\n\n"))
		Expect(res.Flushed).To(BeTrue())
		var phases []diagnostics.Phase
		for _, event := range events {
			phases = append(phases, event.Phase)
		}
		Expect(phases).To(Equal([]diagnostics.Phase{diagnostics.PhaseHTTPEntry, diagnostics.PhaseBodyRead, diagnostics.PhaseTraceStart, diagnostics.PhaseFrontendMessage, diagnostics.PhaseDispatchSummary}))
	})

	It("retains extraction identity and isolates concurrent canceled requests", func() {
		var mu sync.Mutex
		groups := map[string][]diagnostics.Event{}
		recorder := diagnostics.NewRecorder(func(e diagnostics.Event) { mu.Lock(); defer mu.Unlock(); groups[e.ID] = append(groups[e.ID], e) })
		ac := config.NewApplicationConfig()
		ac.DiagnosticsRecorder = recorder
		re := NewRequestExtractor(nil, nil, ac)
		e := echo.New()
		e.Pre(DispatchMiddleware(recorder))
		e.Use(DispatchAuth(auth.NoopMiddleware()))
		e.GET("/", func(c echo.Context) error { return echo.ErrBadGateway }, re.BuildConstantDefaultModelNameMiddleware("model"))
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				res := httptest.NewRecorder()
				e.ServeHTTP(res, httptest.NewRequest("GET", "/", nil).WithContext(ctx))
				Expect(res.Code).To(Equal(502))
			}()
		}
		wg.Wait()
		Expect(groups).To(HaveLen(8))
		for _, events := range groups {
			var authEnd *diagnostics.Event
			for i := range events {
				if events[i].Phase == diagnostics.PhaseAuth && events[i].State == diagnostics.StateEnd {
					authEnd = &events[i]
				}
			}
			Expect(authEnd).NotTo(BeNil())
			Expect(authEnd.Outcome).To(Equal(diagnostics.OutcomeCanceled))
			Expect(events[len(events)-1].Phase).To(Equal(diagnostics.PhaseDispatchSummary))
		}
	})
	It("records read failure without starting a trace or changing the returned error", func() {
		root := GinkgoT().TempDir()
		app, err := application.New(config.WithDataPath(root), config.WithDisableStats(true), config.WithDisableLocalAIAssistant(true), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		app.ApplicationConfig().EnableTracing = true
		var events []diagnostics.Event
		r := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) })
		failure := errors.New("private read error")
		body := &dispatchFailReader{err: failure}
		req := httptest.NewRequest("POST", "/", body)
		req.Header.Set("Content-Type", "application/json")
		c := echo.New().NewContext(req, httptest.NewRecorder())
		h := DispatchMiddleware(r)(TraceMiddleware(app)(func(echo.Context) error { Fail("unexpected handler"); return nil }))
		Expect(h(c)).To(BeIdenticalTo(failure))
		Expect(body.reads).To(Equal(1))
		Expect(events).To(HaveLen(3))
		Expect(events[1].Phase).To(Equal(diagnostics.PhaseBodyRead))
		Expect(events[1].Outcome).To(Equal(diagnostics.OutcomeError))
	})

})

type dispatchFailReader struct {
	reads int
	err   error
}

func (r *dispatchFailReader) Read([]byte) (int, error) { r.reads++; return 0, r.err }
