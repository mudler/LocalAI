// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The coordinator host needs its store beside the chats: GORM on the auth
// database when there is one, JSON files under the data path when there is
// not, and memory only with neither. It exists only when the chats do.
var _ = Describe("coordinator host at startup", func() {
	newApp := func(opts ...config.AppOption) *Application {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()), system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		base := []config.AppOption{config.WithContext(ctx), config.WithSystemState(state), config.DisableMetricsEndpoint,
			config.WithDisableStats(true)}
		app, err := New(append(base, opts...)...)
		if app != nil {
			DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		}
		Expect(err).NotTo(HaveOccurred())
		return app
	}

	It("builds the host on the auth database", func() {
		app := newApp(config.WithDataPath(GinkgoT().TempDir()), config.WithAuthEnabled(true),
			config.WithAuthDatabaseURL(filepath.Join(GinkgoT().TempDir(), "auth.db")))
		Expect(app.StudioService().Coordinators()).NotTo(BeNil())
		Expect(app.studioCoordinators).To(BeIdenticalTo(app.StudioService().Coordinators()))
		Expect(app.authDB.Migrator().HasTable("studio_coordinators")).To(BeTrue())
		Expect(app.authDB.Migrator().HasTable("studio_coordinator_audit")).To(BeTrue())
	})

	It("builds the host on a data path", func() {
		dir := GinkgoT().TempDir()
		app := newApp(config.WithDataPath(dir))
		Expect(app.StudioService().Coordinators()).NotTo(BeNil())
		Expect(os.Stat(filepath.Join(dir, "chats", "coordinators"))).NotTo(BeNil())
	})

	It("builds the host in memory when there is no data path", func() {
		app := newApp(config.WithDataPath(""))
		Expect(app.StudioService().Coordinators()).NotTo(BeNil())
	})

	It("has no host when the chats are off", func() {
		app := &Application{applicationConfig: config.NewApplicationConfig()}
		app.startStudioCoordinators(nil)
		Expect(app.studioCoordinators).To(BeNil(), "without a store there is no host")
		Expect(app.StudioService()).To(BeNil())
	})
})
