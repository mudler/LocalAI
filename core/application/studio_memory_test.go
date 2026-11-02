//go:build auth

package application

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/collections"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/memoryaudit"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("principal memory application adapter", func() {
	It("writes, retries, and deletes exact documents with content-free outcomes on a real PostgreSQL backend", func() {
		ctx := context.Background()
		url := os.Getenv("MEMORY_DATABASE_URL")
		Expect(url).NotTo(BeEmpty())
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"embedding":[1,0,0],"index":0}]}`)
		}))
		DeferCleanup(server.Close)
		cfg := &config.ApplicationConfig{}
		cfg.Collections.StateDir = GinkgoT().TempDir()
		cfg.Collections.CollectionDBPath = filepath.Join(cfg.Collections.StateDir, "db")
		cfg.Collections.VectorEngine = "postgres"
		cfg.Collections.DatabaseURL = url
		cfg.Collections.APIURL = server.URL + "/v1"
		cfg.Collections.EmbeddingModel = "test"
		cfg.Collections.MaxChunkingSize = 1024
		cs, err := collections.New(cfg, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cs.Close)
		name := fmt.Sprintf("studio_mem_%d", time.Now().UnixNano())
		Expect(cs.CreateForUser("", name)).To(Succeed())
		chats, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		audit, err := memoryaudit.NewJSONAudit("")
		Expect(err).NotTo(HaveOccurred())
		a := &Application{studioMemoryAudit: audit}
		a.collectionsService.Store(cs)
		adapter := studioCollections{a}
		svc, err := studio.New(studio.Deps{StateDir: GinkgoT().TempDir(), Store: chats, Agents: as, Provider: provider.New(), Collections: adapter})
		Expect(err).NotTo(HaveOccurred())
		a.studioService = svc
		DeferCleanup(func() { _ = svc.Close(ctx) })
		ag := agents.Agent{ID: "writer", Principal: studio.AnonymousPrincipal, Name: "writer", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: name, DeleteEntries: true}}}, Memory: &agents.MemorySettings{Version: 1, AddMemory: true, Destination: name, ToolStorage: "content", AutomaticIngestion: "off"}}
		Expect(as.Put(ctx, ag)).To(Succeed())
		chat, err := svc.Create(ctx, studio.AnonymousPrincipal, studio.CreateInput{AgentID: ag.ID, Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		req := studio.MemoryRequest{MemoryAuthorization: studio.MemoryAuthorization{Principal: studio.AnonymousPrincipal, AgentID: ag.ID, ChatID: chat.ID, Source: "tool", Destination: name, Storage: "content"}, Content: "PRIVATE EXACT CONTENT", Key: "stable-call"}
		check := func(context.Context) error { return nil }
		receipt, err := adapter.WriteMemory(ctx, req, check)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.EntryID).NotTo(BeEmpty())
		retry, err := adapter.WriteMemory(ctx, req, check)
		Expect(err).NotTo(HaveOccurred())
		Expect(retry).To(Equal(receipt))
		authz := req.MemoryAuthorization
		authz.Delete = true
		_, err = adapter.DeleteMemory(ctx, studio.DeleteMemoryRequest{MemoryAuthorization: authz, EntryID: "*"}, check)
		Expect(err).To(HaveOccurred())
		_, err = adapter.DeleteMemory(ctx, studio.DeleteMemoryRequest{MemoryAuthorization: authz, EntryID: "guessed"}, check)
		Expect(err).To(HaveOccurred())
		deleted, err := adapter.DeleteMemory(ctx, studio.DeleteMemoryRequest{MemoryAuthorization: authz, EntryID: receipt.EntryID}, check)
		Expect(err).NotTo(HaveOccurred())
		Expect(deleted.EntryID).To(Equal(receipt.EntryID))
		events, err := audit.Events(ctx, 50)
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(6))
		Expect(events[0].Outcome).To(Equal("deleted"))
		Expect(events[2].Outcome).To(Equal("failed"))
		for _, e := range events {
			Expect(e.Principal).To(Equal(studio.AnonymousPrincipal))
			Expect(e.Agent).To(Equal(ag.ID))
			Expect(e.Chat).To(Equal(chat.ID))
			Expect(e.Collection).To(Equal(name))
			Expect(fmt.Sprint(e)).NotTo(ContainSubstring(req.Content))
		}
		// Deletion does not reset the collection; another producer can still write.
		req.Key = "another-call"
		_, err = adapter.WriteMemory(ctx, req, check)
		Expect(err).NotTo(HaveOccurred())
		req.Key = "revoked"
		_, err = adapter.WriteMemory(ctx, req, func(context.Context) error {
			ag.Memory.AddMemory = false
			Expect(as.Put(ctx, ag)).To(Succeed())
			return studio.ErrMemoryDenied
		})
		Expect(err).To(HaveOccurred())
	})
})
