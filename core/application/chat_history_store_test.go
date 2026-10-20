// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The conversation routes are registered only when the store exists, so a
// store that New() never creates means /api/conversations answers 404 even
// with auth on. Built through New(), the same path the server takes.
var _ = Describe("chat history store at startup", func() {
	newApp := func(opts ...config.AppOption) *Application {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()), system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		base := []config.AppOption{config.WithContext(ctx), config.WithSystemState(state), config.DisableMetricsEndpoint,
			config.WithDisableLocalAIAssistant(true), config.WithDisableStats(true), config.WithDataPath(GinkgoT().TempDir())}
		app, err := New(append(base, opts...)...)
		if app != nil {
			DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		}
		Expect(err).NotTo(HaveOccurred())
		return app
	}
	withAuth := func() []config.AppOption {
		return []config.AppOption{config.WithAuthEnabled(true),
			config.WithAuthDatabaseURL(filepath.Join(GinkgoT().TempDir(), "auth.db"))}
	}

	It("is created when auth is enabled", func() {
		Expect(newApp(withAuth()...).ChatHistoryStore()).NotTo(BeNil())
	})

	It("stays nil with the WebUI disabled", func() {
		Expect(newApp(append(withAuth(), config.DisableWebUI)...).ChatHistoryStore()).To(BeNil())
	})

	It("stays nil without auth, so the UI keeps localStorage", func() {
		Expect(newApp().ChatHistoryStore()).To(BeNil())
	})
})
