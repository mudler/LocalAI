// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/convert"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("conversion of the old data at start", func() {
	var data, state, dbFile string

	// boot builds the application as cmd does. The server converts after it has
	// started the collections; start does the same call, boot leaves it out.
	boot := func(extra ...config.AppOption) *Application {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		sys, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()), system.WithBackendPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		opts := append([]config.AppOption{}, extra...)
		app, err := New(append(opts, config.WithContext(ctx), config.WithSystemState(sys), config.DisableMetricsEndpoint,
			config.WithDisableLocalAIAssistant(true), config.WithDisableStats(true), config.WithDataPath(data),
			config.WithCollectionsStateDir(state), config.WithAuthEnabled(true), config.WithAuthDatabaseURL(dbFile),
			config.WithAuthAPIKeyHMACSecret("convert-test-secret"))...)
		Expect(err).NotTo(HaveOccurred())
		return app
	}
	start := func() *Application {
		app := boot()
		app.ConvertStudioData()
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

	It("does not tell a user again about an item that can never convert", func() {
		app := start()
		Expect(app.Shutdown()).To(Succeed())
		// A task that only ran when called has no schedule to convert.
		task, _ := json.Marshal(map[string]any{"tasks": []any{map[string]any{"id": "w", "name": "Hook", "model": "m", "prompt": "p"}}})
		Expect(os.MkdirAll(filepath.Join(state, "users", "alice"), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(state, "users", "alice", "agent_tasks.json"), task, 0o600)).To(Succeed())

		second := start()
		Expect(inbox(second, "alice")).To(Equal(2), "the new dropped task is news once")
		Expect(second.Shutdown()).To(Succeed())

		third := start()
		DeferCleanup(func() { _ = third.Shutdown() })
		Expect(inbox(third, "alice")).To(Equal(2), "and not again at the next start")
		rep, _, err := third.StudioConversion(context.Background())
		Expect(err).NotTo(HaveOccurred())
		var reason string
		for _, i := range rep.ForUser("alice").Items {
			if i.Name == "Hook" {
				reason = i.Status + ": " + i.Reason
			}
		}
		Expect(reason).To(ContainSubstring("dropped: the task has no cron schedule"), "the first reason is kept")
	})

	It("waits for the collections to convert, and does not convert an agent with a knowledge base without its grant", func() {
		pool, _ := json.Marshal(map[string]any{
			"alice:Docs": map[string]any{"name": "alice:Docs", "model": "m", "system_prompt": "x", "enable_kb": true},
		})
		Expect(os.WriteFile(filepath.Join(state, "pool.json"), pool, 0o600)).To(Succeed())
		enabled := func(o *config.ApplicationConfig) { o.Collections.Enabled = true }
		app := boot(enabled)
		DeferCleanup(func() { _ = app.Shutdown() })
		list, _ := app.StudioService().Agents(context.Background(), "alice")
		Expect(list).To(BeEmpty(), "the conversion waits for the collections to start")
		app.ConvertStudioData() // as cli/run.go does after StartCollections; here the service never started
		list, _ = app.StudioService().Agents(context.Background(), "alice")
		Expect(list).To(BeEmpty(), "Docs is not converted without the collection grant")
		rep, _, _ := app.StudioConversion(context.Background())
		var status string
		for _, i := range rep.ForUser("alice").Items {
			if i.Name == "Docs" {
				status = i.Status
			}
		}
		Expect(status).To(Equal(convert.StatusFailed))
	})

	It("converts on its own when the collections are off, since nothing starts later", func() {
		off := func(o *config.ApplicationConfig) { o.Collections.Enabled = false }
		app := boot(off)
		DeferCleanup(func() { _ = app.Shutdown() })
		// It runs beside the start-up, so the agent shows a moment after New returns.
		Eventually(func() []agents.Agent {
			list, err := app.StudioService().Agents(context.Background(), "alice")
			Expect(err).NotTo(HaveOccurred())
			return list
		}, 10*time.Second, 50*time.Millisecond).Should(HaveLen(1))
	})
})
