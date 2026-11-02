//go:build auth

// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/collections"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/memory"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("memory modes runtime application acceptance", func() {
	DescribeTable("stores only content enabled by the mode and actual answer outcome", func(scenario string) {
		ctx := context.Background()
		embeddings := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"data":[{"embedding":[1,0,0],"index":0}]}`)
		}))
		DeferCleanup(embeddings.Close)
		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.VectorEngine = "chromem"
		cfg.Collections.APIURL = embeddings.URL + "/v1"
		cfg.Collections.EmbeddingModel = "test"
		cs, err := collections.New(cfg, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cs.Close)
		const name = "runtime_mode_memory"
		const incoming = "Exact accepted incoming text"
		const reply = "Successful final reply text"
		const explicit = "Explicit tool-only memory text"
		Expect(cs.CreateForUser("", name)).To(Succeed())
		chats, err := store.NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		app := &Application{}
		app.collectionsService.Store(cs)
		adapter := studioCollections{app}
		h := &studiotest.ToolHarness{Script: func(_ workspace.Actor, message string) []studiotest.ToolStep {
			if scenario == "incoming_failed" {
				return []studiotest.ToolStep{{Say: "Partial answer must not become memory"}, {Err: errors.New("answer model failed")}}
			}
			if scenario == "tool_only" && message == "explicit write" {
				return []studiotest.ToolStep{{Tool: "add_memory", Args: fmt.Sprintf(`{"content":%q}`, explicit)}, {Say: reply}}
			}
			return []studiotest.ToolStep{{Say: reply}}
		}}
		saved := make(chan store.CaptureRecord, 2)
		svc, err := studio.New(studio.Deps{StateDir: GinkgoT().TempDir(), Store: chats, Agents: as, Provider: provider.New(), Collections: adapter, Harness: h, CaptureWorker: studio.CaptureWorkerOptions{Owner: "mode-worker", Publish: func(r store.CaptureRecord, status memory.PublishStatus) {
			if status.Status == "Saved" {
				saved <- r
			}
		}}})
		Expect(err).NotTo(HaveOccurred())
		app.studioService = svc
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		mode := "off"
		if scenario == "incoming_failed" {
			mode = "incoming_only"
		}
		if scenario == "incoming_replies" {
			mode = "incoming_and_replies"
		}
		ag := agents.Agent{ID: "mode-agent", Principal: studio.AnonymousPrincipal, Name: "Mode agent", Model: "m", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: name}}}, Memory: &agents.MemorySettings{Version: 1, Destination: name, AddMemory: scenario == "tool_only", ToolStorage: "content", AutomaticIngestion: mode}}
		Expect(as.Put(ctx, ag)).To(Succeed())
		chat, err := svc.Create(ctx, studio.AnonymousPrincipal, studio.CreateInput{AgentID: ag.ID, Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		sendAndJoin := func(text string, status store.TurnStatus) {
			_, e := svc.Send(ctx, studio.AnonymousPrincipal, chat.ID, text)
			Expect(e).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				turn, e := svc.Turn(ctx, studio.AnonymousPrincipal, chat.ID)
				g.Expect(e).NotTo(HaveOccurred())
				g.Expect(turn.Running).To(BeFalse())
				g.Expect(turn.Last).To(Equal(status))
			}, 5*time.Second).Should(Succeed())
		}
		expectedTurn := store.TurnDone
		if scenario == "incoming_failed" {
			expectedTurn = store.TurnFailed
		}
		sendAndJoin(incoming, expectedTurn)
		records, err := chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		expectedCaptures := 0
		if scenario == "incoming_failed" {
			expectedCaptures = 1
		}
		if scenario == "incoming_replies" {
			expectedCaptures = 2
		}
		Expect(records).To(HaveLen(expectedCaptures))
		if scenario == "off" || scenario == "tool_only" {
			entries, e := cs.ListEntriesForUser("", name)
			Expect(e).NotTo(HaveOccurred())
			Expect(entries).To(BeEmpty())
			if scenario == "off" {
				Expect(h.ToolNames(chat.ID)).NotTo(ContainElement("add_memory"))
			}
			if scenario == "tool_only" {
				Expect(h.ToolNames(chat.ID)).To(ContainElement("add_memory"))
				sendAndJoin("explicit write", store.TurnDone)
				results := h.Results()
				Expect(results).To(HaveLen(1))
				Expect(results[0].Err).NotTo(HaveOccurred())
				var receipt studio.MemoryResult
				Expect(json.Unmarshal([]byte(results[0].Out), &receipt)).To(Succeed())
				Expect(receipt.EntryID).NotTo(BeEmpty())
				records, err = chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(records).To(BeEmpty())
			}
		}
		workerCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- svc.ProcessMemoryCaptures(workerCtx) }()
		workerJoined := false
		DeferCleanup(func() {
			if !workerJoined {
				cancel()
				Eventually(done, 3*time.Second).Should(Receive(MatchError(context.Canceled)))
			}
		})
		for i := 0; i < expectedCaptures; i++ {
			Eventually(saved, 5*time.Second).Should(Receive())
		}
		cancel()
		Eventually(done, 3*time.Second).Should(Receive(MatchError(context.Canceled)))
		workerJoined = true
		final, err := chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(final).To(HaveLen(expectedCaptures))
		expectedContents := []string{}
		switch scenario {
		case "tool_only":
			expectedContents = []string{explicit}
		case "incoming_failed":
			expectedContents = []string{incoming}
		case "incoming_replies":
			expectedContents = []string{incoming, reply}
		}
		for _, r := range final {
			Expect(r.State).To(Equal("Saved"))
			Expect(r.RuleID).To(Equal("R05-saved"))
		}
		if scenario == "incoming_failed" {
			Expect(final[0].Identity.Kind).To(Equal("incoming"))
		}
		if scenario == "incoming_replies" {
			Expect(final[0].ID).NotTo(Equal(final[1].ID))
			Expect([]string{final[0].Identity.Kind, final[1].Identity.Kind}).To(ConsistOf("incoming", "reply"))
		}
		entries, err := cs.ListEntriesForUser("", name)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(len(expectedContents)))
		hits, err := cs.SearchForUser(ctx, "", name, "memory text", 10)
		Expect(err).NotTo(HaveOccurred())
		var actual []string
		for _, hit := range hits {
			actual = append(actual, hit.Content)
			Expect(hit.EntryID).NotTo(BeEmpty())
		}
		Expect(actual).To(ConsistOf(expectedContents))
	}, Entry("off stores neither ordinary input nor answer", "off"), Entry("tool-only writes explicitly but never captures ordinary turns", "tool_only"), Entry("incoming capture survives an actual failed answer", "incoming_failed"), Entry("successful incoming and final reply are separate documents", "incoming_replies"))
})
