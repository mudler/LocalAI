// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"io"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The new chat must work with and without a database: GORM on the auth
// database when there is one, JSON files under the data path when there is
// not, and memory only when there is no data path either.
var _ = Describe("studio store at startup", func() {
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

	It("uses GORM on the auth database when auth is enabled", func() {
		app := newApp(config.WithAuthEnabled(true), config.WithAuthDatabaseURL(filepath.Join(GinkgoT().TempDir(), "auth.db")))
		Expect(app.StudioStore()).NotTo(BeNil())
		Expect(app.studioStoreKind).To(Equal("gorm"))
	})

	It("uses JSON files and retains ownership through Shutdown", func() {
		dir := GinkgoT().TempDir()
		first := newApp(config.WithDataPath(dir))
		Expect(first.studioStoreKind).To(Equal("json"))
		Expect(first.StudioStore().CreateChat(context.Background(),
			session.ChatRecord{ID: "kept", Principal: "", Title: "Kept"})).To(Succeed())

		Expect(first.Shutdown()).To(Succeed())
		blocked, err := store.NewJSON(filepath.Join(dir, "chats"))
		Expect(err).To(HaveOccurred())
		Expect(blocked).To(BeNil())
		blockedAgent, err := agents.NewJSON(filepath.Join(dir, "chats", "agents"))
		Expect(err).To(HaveOccurred())
		Expect(blockedAgent).To(BeNil())
		rec, err := first.StudioStore().GetChat(context.Background(), "", "kept")
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Title).To(Equal("Kept"))
	})

	It("releases earlier unpublished stores when a later constructor fails", func() {
		for _, failure := range []string{"agents", "notifications"} {
			dir := GinkgoT().TempDir()
			chats := filepath.Join(dir, "chats")
			Expect(os.MkdirAll(chats, 0700)).To(Succeed())
			// Agents fails after chats; notifications fails after both owners.
			Expect(os.WriteFile(filepath.Join(chats, failure), []byte("not a directory"), 0600)).To(Succeed())
			app := &Application{applicationConfig: &config.ApplicationConfig{DataPath: dir}}
			app.initStudioStore()
			Expect(app.studioStore).To(BeNil())
			Expect(app.studioAgents).To(BeNil())
			reopened, err := store.NewJSON(chats)
			Expect(err).NotTo(HaveOccurred())
			Expect(reopened.(io.Closer).Close()).To(Succeed())
			if failure == "notifications" {
				reopenedAgent, err := agents.NewJSON(filepath.Join(chats, "agents"))
				Expect(err).NotTo(HaveOccurred())
				Expect(reopenedAgent.(io.Closer).Close()).To(Succeed())
			}
		}
	})

	It("keeps chats in memory only when there is no data path", func() {
		app := newApp(config.WithDataPath(""))
		Expect(app.studioStoreKind).To(Equal("memory"))
		Expect(app.StudioStore().CreateChat(context.Background(),
			session.ChatRecord{ID: "gone", Title: "Gone"})).To(Succeed())
	})
})
