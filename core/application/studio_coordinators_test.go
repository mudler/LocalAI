// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
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

	// The reaper step carries the wake-ups of the coordinators: on the leader
	// with a database, and in the one process without one.
	DescribeTable("the reaper step ticks the coordinators",
		func(authDB bool) {
			opts := []config.AppOption{config.WithDataPath(GinkgoT().TempDir())}
			if authDB {
				opts = append(opts, config.WithAuthEnabled(true), config.WithAuthDatabaseURL(filepath.Join(GinkgoT().TempDir(), "auth.db")))
			}
			app := newApp(opts...)
			host := app.StudioService().Coordinators()
			Expect(host).NotTo(BeNil())
			v, err := host.Create(context.Background(), "", studio.CoordinatorInput{
				Agent:  agents.Agent{Name: "Desk", Model: "m"},
				Config: coordinator.Config{WakeEvery: 10 * time.Minute},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(v.Config.LastWake.IsZero()).To(BeTrue())
			app.studioReaperStep(context.Background(), app.StudioService(), authDB)()
			got, err := app.studioCoordinatorStore.Get(context.Background(), v.Agent.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.LastWake.IsZero()).To(BeFalse(), "the step woke the due coordinator")
		},
		Entry("with a database", true),
		Entry("without a database", false),
	)

	It("has no host when the chats are off", func() {
		app := &Application{applicationConfig: config.NewApplicationConfig()}
		app.startStudioCoordinators(nil)
		Expect(app.studioCoordinators).To(BeNil(), "without a store there is no host")
		Expect(app.StudioService()).To(BeNil())
	})
})
