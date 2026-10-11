// SPDX-License-Identifier: MIT
package http_test

import (
	"net/http/httptest"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Application dispatch admission", func() {
	It("attaches identity before global authentication rejects the request", func() {
		root := GinkgoT().TempDir()
		var events []diagnostics.Event
		recorder := diagnostics.NewRecorder(func(e diagnostics.Event) {
			if e.Kind == diagnostics.KindRequest {
				events = append(events, e)
			}
		})
		app, err := application.New(config.WithDataPath(root), config.WithDisableStats(true), config.WithDisableLocalAIAssistant(true), config.WithApiKeys([]string{"secret"}), config.WithDiagnosticsRecorder(recorder), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		e, err := API(app)
		Expect(err).NotTo(HaveOccurred())
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		req.Header.Set(diagnostics.HeaderDiagnosticID, "spoof")
		res := httptest.NewRecorder()
		e.ServeHTTP(res, req)
		Expect(res.Code).To(Equal(401))
		Expect(res.Header().Get("WWW-Authenticate")).To(Equal("Bearer"))
		Expect(events).To(HaveLen(4))
		Expect(events[0].Phase).To(Equal(diagnostics.PhaseHTTPEntry))
		Expect(events[2].Phase).To(Equal(diagnostics.PhaseAuth))
		Expect(events[2].Outcome).To(Equal(diagnostics.OutcomeError))
		Expect(res.Header().Get(diagnostics.HeaderDiagnosticID)).To(Equal(events[0].ID))
		Expect(events[0].ID).NotTo(Equal("spoof"))
	})
})
