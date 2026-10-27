// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/convert"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("conversion of the old data at start", func() {
	var data, state, dbFile string

	start := func() *Application {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		sys, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()), system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		app, err := New(config.WithContext(ctx), config.WithSystemState(sys), config.DisableMetricsEndpoint,
			config.WithDisableLocalAIAssistant(true), config.WithDisableStats(true), config.WithDataPath(data),
			config.WithAgentPoolStateDir(state), config.WithAuthEnabled(true), config.WithAuthDatabaseURL(dbFile),
			config.WithAuthAPIKeyHMACSecret("convert-test-secret"))
		Expect(err).NotTo(HaveOccurred())
		return app
	}

	inbox := func(app *Application, user string) int {
		list, err := app.StudioService().Notifications(context.Background(), user, false, 50)
		Expect(err).NotTo(HaveOccurred())
		return len(list)
	}

	BeforeEach(func() {
		data, state = GinkgoT().TempDir(), GinkgoT().TempDir()
		dbFile = filepath.Join(GinkgoT().TempDir(), "auth.db")
		app := start() // nothing to convert yet; it makes the database and the users
		for _, id := range []string{"alice", "bob"} {
			Expect(app.AuthDB().Create(&auth.User{ID: id, Email: id + "@example.com", Name: id, Provider: auth.ProviderLocal,
				Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
		}
		Expect(app.Shutdown()).To(Succeed())
		pool, _ := json.Marshal(map[string]any{
			"alice:Helper": map[string]any{"name": "alice:Helper", "model": "m", "system_prompt": "Help."},
			"bob:Writer":   map[string]any{"name": "bob:Writer", "model": "m", "system_prompt": "Write."},
		})
		Expect(os.WriteFile(filepath.Join(state, "pool.json"), pool, 0o600)).To(Succeed())
	})

	It("converts the agents and tells each user once, and a second start does nothing", func() {
		app := start()
		DeferCleanup(func() { _ = app.Shutdown() })
		for _, u := range []string{"alice", "bob"} {
			list, err := app.StudioService().Agents(context.Background(), u)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1), u)
			Expect(inbox(app, u)).To(Equal(1), u)
		}
		Expect(app.Shutdown()).To(Succeed())

		again := start()
		DeferCleanup(func() { _ = again.Shutdown() })
		for _, u := range []string{"alice", "bob"} {
			list, _ := again.StudioService().Agents(context.Background(), u)
			Expect(list).To(HaveLen(1), u)
			Expect(inbox(again, u)).To(Equal(1), "no second notification for "+u)
		}
	})

	It("keeps a report in which one user's items are not another's", func() {
		app := start()
		DeferCleanup(func() { _ = app.Shutdown() })
		rep, ok, err := app.StudioConversion(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		alice := rep.ForUser("alice")
		Expect(alice.Items).To(HaveLen(1))
		Expect(alice.Items[0].Name).To(Equal("Helper"))
		Expect(alice.Items[0].Status).To(Equal(convert.StatusConverted))
		Expect(rep.Users()).To(ConsistOf("alice", "bob"))
	})
})
