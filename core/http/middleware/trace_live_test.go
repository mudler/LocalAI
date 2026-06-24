// SPDX-License-Identifier: MIT

package middleware

import (
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("live API traces", func() {
	newApp := func(root string) *application.Application {
		app, err := application.New(
			config.EnableTracing,
			config.WithDataPath(root),
			config.WithDisableLocalAIAssistant(true),
			config.WithDisableStats(true),
			config.WithSystemState(&system.SystemState{
				Model:   system.Model{ModelsPath: root},
				Backend: system.Backend{BackendsPath: root},
			}),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		ClearTraces()
		return app
	}

	It("lists a request while its handler is still running", func() {
		root := GinkgoT().TempDir()
		app := newApp(root)

		started := make(chan struct{})
		release := make(chan struct{})
		DeferCleanup(func() {
			select {
			case <-release:
			default:
				close(release)
			}
		})
		handler := TraceMiddleware(app)(func(c echo.Context) error {
			close(started)
			<-release
			return c.NoContent(http.StatusNoContent)
		})

		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/slow", http.NoBody)
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetPath("/slow")
		done := make(chan error, 1)
		go func() {
			done <- handler(ctx)
		}()
		<-started

		var running APIExchange
		Eventually(func() bool {
			traces := GetTraces()
			if len(traces) != 1 {
				return false
			}
			running = traces[0]
			return running.Request.Path == "/slow"
		}).Should(BeTrue())
		Expect(running.Response.Status).To(Equal(0))
		Expect(running.Duration).To(BeNumerically(">", 0))

		close(release)
		Expect(<-done).To(Succeed())
		Eventually(func() []APIExchange { return GetTraces() }).Should(ConsistOf(
			And(
				HaveField("ID", running.ID),
				HaveField("Response.Status", http.StatusNoContent),
				HaveField("Duration", BeNumerically(">", time.Duration(0))),
			),
		))
	})

	It("removes an in-flight trace when the handler panics", func() {
		app := newApp(GinkgoT().TempDir())
		handler := TraceMiddleware(app)(func(echo.Context) error {
			panic("handler panic")
		})
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/panic", http.NoBody)
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		ctx := e.NewContext(req, httptest.NewRecorder())
		ctx.SetPath("/panic")

		func() {
			defer func() { _ = recover() }()
			_ = handler(ctx)
		}()

		Expect(GetTraces()).To(BeEmpty())
	})
})
