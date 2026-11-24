// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The permissions migration of the connector bindings runs on its own
// goroutine and writes the connector store under DataPath. Shutdown must wait
// for it, or whoever removes the directory next (a test's TempDir cleanup, an
// operator) races its write: "unlinkat .../chats/connectors: directory not
// empty".
var _ = Describe("connector binding migration at shutdown", func() {
	start := func(opts ...config.AppOption) *Application {
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()), system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		base := []config.AppOption{config.WithSystemState(state), config.DisableMetricsEndpoint,
			config.WithDisableStats(true), config.WithDataPath(GinkgoT().TempDir())}
		app, err := New(append(base, opts...)...)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) }) // Shutdown is idempotent
		return app
	}
	hook := func(f func(context.Context)) {
		prev := bindingCapsMigrationForTests
		bindingCapsMigrationForTests = f
		DeferCleanup(func() { bindingCapsMigrationForTests = prev })
	}

	It("returns from Shutdown only after the migration returned", func() {
		entered, finished := make(chan struct{}), atomic.Bool{}
		hook(func(ctx context.Context) {
			close(entered)
			<-ctx.Done()
			// A migration still busy when Shutdown begins.
			time.Sleep(100 * time.Millisecond)
			finished.Store(true)
		})
		app := start()
		Eventually(entered).Should(BeClosed())
		Expect(app.Shutdown()).To(Succeed())
		Expect(finished.Load()).To(BeTrue(), "Shutdown returned while the migration could still write DataPath")
	})

	It("does not run the migration when it is skipped, and Shutdown still returns", func() {
		var ran atomic.Bool
		hook(func(context.Context) { ran.Store(true) })
		app := start(config.SkipBindingCapsMigration)
		Expect(app.Shutdown()).To(Succeed())
		Consistently(ran.Load, 200*time.Millisecond).Should(BeFalse())
	})
})
