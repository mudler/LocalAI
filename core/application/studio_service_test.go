// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("studio service at startup", func() {
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

	It("builds the service with and without a database", func() {
		Expect(newApp().StudioService()).NotTo(BeNil())
		withAuth := newApp(config.WithAuthEnabled(true), config.WithAuthDatabaseURL(filepath.Join(GinkgoT().TempDir(), "auth.db")))
		Expect(withAuth.StudioService()).NotTo(BeNil())
	})

	Describe("turns left running in a shared database", func() {
		start := func(dbPath, dataDir string) *Application {
			return newApp(config.WithAuthEnabled(true), config.WithAuthDatabaseURL(dbPath), config.WithDataPath(dataDir))
		}

		It("keeps the turn that another live process is running", func() {
			dbPath := filepath.Join(GinkgoT().TempDir(), "auth.db")
			dataDir := GinkgoT().TempDir()
			ctx := context.Background()
			first := start(dbPath, dataDir)
			rec, err := first.StudioService().Create(ctx, "u1", studio.CreateInput{Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			// A second process on the same database is beating this claim.
			Expect(first.StudioStore().ClaimTurn(ctx, "u1", rec.ID, "live-run")).To(Succeed())

			second := start(dbPath, dataDir)
			info, err := second.StudioService().Turn(ctx, "u1", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Running).To(BeTrue(), "a process starting must not end a live turn of another process")
			Expect(info.RunID).To(Equal("live-run"))
		})

		It("frees a turn that stopped beating when a process starts", func() {
			dbPath := filepath.Join(GinkgoT().TempDir(), "auth.db")
			dataDir := GinkgoT().TempDir()
			ctx := context.Background()
			first := start(dbPath, dataDir)
			rec, err := first.StudioService().Create(ctx, "u1", studio.CreateInput{Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			// The process dies in the middle of a turn: its claim is never released
			// and its beat goes stale.
			Expect(first.StudioStore().ClaimTurn(ctx, "u1", rec.ID, "dead-run")).To(Succeed())
			Expect(first.Shutdown()).To(Succeed())
			stale := time.Now().Add(-2 * studioTurnTimeout).UTC()
			Expect(first.authDB.Exec("UPDATE studio_chats SET run_beat = ? WHERE id = ?", stale, rec.ID).Error).To(Succeed())

			second := start(dbPath, dataDir)
			info, err := second.StudioService().Turn(ctx, "u1", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Running).To(BeFalse())
			Expect(info.Last).To(Equal(store.TurnInterrupted))
		})
	})

	It("gives each model its configured context size", func() {
		app := newApp()
		size := 4096
		app.ModelConfigLoader().ReplaceModelConfigs([]config.ModelConfig{{Name: "small", LLMConfig: config.LLMConfig{ContextSize: &size}}, {Name: "unset"}})
		Expect(app.studioContextTokens("small")).To(Equal(4096))
		// No context_size: the backend runs with its default window.
		Expect(app.studioContextTokens("unset")).To(Equal(config.DefaultContextSize))
		Expect(app.studioContextTokens("missing")).To(Equal(0))
	})

	It("reads the window of an alias from its target, not from the alias", func() {
		app := newApp()
		big, fallback := 100096, 4096
		app.ModelConfigLoader().ReplaceModelConfigs([]config.ModelConfig{
			{Name: "target-model", LLMConfig: config.LLMConfig{ContextSize: &big}},
			// The loader gives every alias the generic default window.
			{Name: "default", Alias: "target-model", LLMConfig: config.LLMConfig{ContextSize: &fallback}},
		})
		Expect(app.studioContextTokens("default")).To(Equal(100096))
		Expect(app.studioContextTokens("target-model")).To(Equal(100096))
	})

	It("reads the agent options of an alias from its target", func() {
		app := newApp()
		app.ModelConfigLoader().ReplaceModelConfigs([]config.ModelConfig{
			{Name: "target-model", Agent: config.AgentConfig{MaxIterations: 7}},
			{Name: "default", Alias: "target-model"},
		})
		Expect(app.studioAgentOptions("default").Iterations).To(Equal(7))
	})

	Describe("MCP servers of a chat", func() {
		servers := "mcpServers:\n  crm:\n    url: http://127.0.0.1:1/crm\n  mail:\n    url: http://127.0.0.1:1/mail\n"

		It("turns on every configured server for a model with mcp.auto", func() {
			cfg := &config.ModelConfig{MCP: config.MCPConfig{Servers: servers, Auto: true}}
			Expect(studioMCPServers(cfg, true)).To(Equal([]string{"crm", "mail"}))
		})

		It("turns on none for a model without mcp.auto, as the chat route does", func() {
			cfg := &config.ModelConfig{MCP: config.MCPConfig{Servers: servers}}
			Expect(studioMCPServers(cfg, true)).To(BeEmpty())
		})

		It("turns on none when the user may not use MCP", func() {
			cfg := &config.ModelConfig{MCP: config.MCPConfig{Servers: servers, Auto: true}}
			Expect(studioMCPServers(cfg, false)).To(BeEmpty())
		})

		It("turns on none for a model with no servers", func() {
			Expect(studioMCPServers(&config.ModelConfig{MCP: config.MCPConfig{Auto: true}}, true)).To(BeEmpty())
		})
	})

	Describe("the user of a chat", func() {
		It("loads an active user and refuses one who is not active", func() {
			app := newApp(config.WithAuthEnabled(true), config.WithAuthDatabaseURL(filepath.Join(GinkgoT().TempDir(), "auth.db")))
			db := app.AuthDB()
			active := &auth.User{ID: "u-active", Email: "a@example.test", Name: "a", Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal}
			pending := &auth.User{ID: "u-pending", Email: "p@example.test", Name: "p", Role: auth.RoleUser, Status: auth.StatusPending, Provider: auth.ProviderLocal}
			Expect(db.Create(active).Error).To(Succeed())
			Expect(db.Create(pending).Error).To(Succeed())

			u, err := app.loadStudioUser(context.Background(), "u-active")
			Expect(err).NotTo(HaveOccurred())
			Expect(u.ID).To(Equal("u-active"))

			_, err = app.loadStudioUser(context.Background(), "u-pending")
			Expect(err).To(HaveOccurred())
			_, err = app.loadStudioUser(context.Background(), "nobody")
			Expect(err).To(HaveOccurred())
		})

		It("knows the synthetic user of a legacy API key", func() {
			app := newApp(config.WithAuthEnabled(true), config.WithAuthDatabaseURL(filepath.Join(GinkgoT().TempDir(), "auth.db")))
			u, err := app.loadStudioUser(context.Background(), "legacy-api-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(u.Role).To(Equal(auth.RoleAdmin))
		})
	})
	Describe("chat worker", func() {
		It("starts the hosts of unattended runs on a frontend and none on a chat worker", func() {
			front := newApp()
			Expect(front.StudioTriggers()).NotTo(BeNil())
			Expect(front.StudioJobs()).NotTo(BeNil())
			Expect(front.StudioConnectors()).NotTo(BeNil())

			worker := newApp(config.EnableChatWorker)
			Expect(worker.StudioService()).NotTo(BeNil(), "a chat worker runs turns")
			Expect(worker.StudioTriggers()).To(BeNil())
			Expect(worker.StudioJobs()).To(BeNil())
			Expect(worker.StudioConnectors()).To(BeNil())
		})

		It("serves no starts on a frontend", func() {
			Expect(newApp().StartChatWorker()).To(Succeed())
		})

		It("bounds a job and a trigger run past their own limits, and nothing else", func() {
			app := newApp(config.WithMCPCIJobTimeout(3 * time.Minute))
			Expect(app.studioWorkerRunLimit(session.OriginJob)).To(Equal(3*time.Minute + chatWorkerGrace))
			Expect(app.studioWorkerRunLimit(session.OriginTrigger)).To(Equal(studio.DefaultRunLimit + chatWorkerGrace))
			Expect(app.studioWorkerRunLimit(session.OriginConnector)).To(BeZero())
			Expect(app.studioWorkerRunLimit("")).To(BeZero())
		})

		It("refuses to serve starts without distributed mode", func() {
			Expect(newApp(config.EnableChatWorker).StartChatWorker()).To(MatchError(ContainSubstring("distributed mode")))
		})
	})
})
