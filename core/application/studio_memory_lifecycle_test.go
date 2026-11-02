//go:build auth

// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/collections"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/memory"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Emit the same runtime pause event as GateHarness after the first scripted
// answer. Later Send/Regenerate calls use ordinary ToolHarness completion.
type pauseFirstMemoryHarness struct {
	tools *studiotest.ToolHarness
	turns atomic.Int32
}

func (h *pauseFirstMemoryHarness) Start(ctx context.Context, env session.HarnessEnv) (session.HarnessSession, error) {
	s, err := h.tools.Start(ctx, env)
	if err != nil {
		return nil, err
	}
	return &pauseFirstMemorySession{HarnessSession: s, h: h, emit: env.Emit}, nil
}

type pauseFirstMemorySession struct {
	session.HarnessSession
	h    *pauseFirstMemoryHarness
	emit func(session.Event)
}

func (s *pauseFirstMemorySession) Send(ctx context.Context, text string) error {
	turn := s.h.turns.Add(1)
	if err := s.HarnessSession.Send(ctx, text); err != nil {
		return err
	}
	if turn == 1 {
		s.emit(session.Event{Kind: session.KindPaused, Steps: 4})
	}
	return nil
}

var _ = Describe("memory runtime pause resume regeneration application acceptance", func() {
	It("excludes paused text and saves distinct final replies without recapturing incoming text", func() {
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
		const name = "lifecycle_memory"
		const incoming = "Original accepted input"
		const partial = "Paused partial reply must stay out of memory"
		const final = "Successful resumed final reply"
		const regenerated = "Distinct regenerated final reply"
		Expect(cs.CreateForUser("", name)).To(Succeed())
		chats, err := store.NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		h := &pauseFirstMemoryHarness{}
		h.tools = &studiotest.ToolHarness{Script: func(workspace.Actor, string) []studiotest.ToolStep {
			switch h.turns.Load() {
			case 1:
				return []studiotest.ToolStep{{Say: partial}}
			case 2:
				return []studiotest.ToolStep{{Say: final}}
			default:
				return []studiotest.ToolStep{{Say: regenerated}}
			}
		}}
		app := &Application{}
		app.collectionsService.Store(cs)
		adapter := studioCollections{app}
		saved := make(chan store.CaptureRecord, 4)
		svc, err := studio.New(studio.Deps{StateDir: GinkgoT().TempDir(), Store: chats, Agents: as, Provider: provider.New(), Collections: adapter, Harness: h, CaptureWorker: studio.CaptureWorkerOptions{Owner: "lifecycle-worker", Publish: func(r store.CaptureRecord, status memory.PublishStatus) {
			if status.Status == "Saved" {
				saved <- r
			}
		}}})
		Expect(err).NotTo(HaveOccurred())
		app.studioService = svc
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		ag := agents.Agent{ID: "lifecycle-agent", Principal: studio.AnonymousPrincipal, Name: "Lifecycle", Model: "m", CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: name}}}, Memory: &agents.MemorySettings{Version: 1, Destination: name, ToolStorage: "content", AutomaticIngestion: "incoming_and_replies"}}
		Expect(as.Put(ctx, ag)).To(Succeed())
		chat, err := svc.Create(ctx, studio.AnonymousPrincipal, studio.CreateInput{AgentID: ag.ID, Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		join := func(status store.TurnStatus) {
			Eventually(func(g Gomega) {
				turn, e := svc.Turn(ctx, studio.AnonymousPrincipal, chat.ID)
				g.Expect(e).NotTo(HaveOccurred())
				g.Expect(turn.Running).To(BeFalse())
				g.Expect(turn.Last).To(Equal(status))
			}, 5*time.Second).Should(Succeed())
		}
		_, err = svc.Send(ctx, studio.AnonymousPrincipal, chat.ID, incoming)
		Expect(err).NotTo(HaveOccurred())
		join(store.TurnPaused)
		paused, err := chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(paused).To(HaveLen(1))
		Expect(paused[0].Identity.Kind).To(Equal("incoming"))
		Expect(paused[0].RawText).To(Equal(incoming))
		workerCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- svc.ProcessMemoryCaptures(workerCtx) }()
		DeferCleanup(func() { cancel(); Eventually(done, 3*time.Second).Should(Receive(MatchError(context.Canceled))) })
		var original store.CaptureRecord
		Eventually(saved, 5*time.Second).Should(Receive(&original))
		Expect(original.ID).To(Equal(paused[0].ID))
		hits, err := cs.SearchForUser(ctx, "", name, "reply", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(1))
		Expect(hits[0].Content).To(Equal(incoming))
		// Product resume is a normal Send after pause, and accepts "Continue" once.
		_, err = svc.Send(ctx, studio.AnonymousPrincipal, chat.ID, "Continue")
		Expect(err).NotTo(HaveOccurred())
		join(store.TurnDone)
		for i := 0; i < 2; i++ {
			Eventually(saved, 5*time.Second).Should(Receive())
		}
		before, err := chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(before).To(HaveLen(3))
		incomingBefore := map[string]store.CaptureRecord{}
		var firstReply store.CaptureRecord
		for _, r := range before {
			if r.Identity.Kind == "incoming" {
				incomingBefore[r.ID] = r
			} else {
				firstReply = r
			}
		}
		Expect(incomingBefore).To(HaveLen(2))
		Expect(firstReply.RawText).To(Equal(final))
		Expect(firstReply.State).To(Equal("Saved"))
		_, err = svc.Regenerate(ctx, studio.AnonymousPrincipal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		join(store.TurnDone)
		var secondReply store.CaptureRecord
		Eventually(saved, 5*time.Second).Should(Receive(&secondReply))
		Expect(secondReply.Identity.Kind).To(Equal("reply"))
		Expect(secondReply.ID).NotTo(Equal(firstReply.ID))
		Expect(secondReply.Identity.Source).NotTo(Equal(firstReply.Identity.Source))
		Expect(secondReply.RawText).To(Equal(regenerated))
		after, err := chats.ListCaptures(ctx, studio.AnonymousPrincipal, chat.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(HaveLen(4))
		for _, r := range after {
			Expect(r.State).To(Equal("Saved"))
			if r.Identity.Kind == "incoming" {
				Expect(r).To(Equal(incomingBefore[r.ID]))
			}
		}
		firstReceipt, err := cs.ReconcileMemory(ctx, "", name, firstReply.ID)
		Expect(err).NotTo(HaveOccurred())
		secondReceipt, err := cs.ReconcileMemory(ctx, "", name, secondReply.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(firstReceipt.EntryID).NotTo(Equal(secondReceipt.EntryID))
		hits, err = cs.SearchForUser(ctx, "", name, "reply", 10)
		Expect(err).NotTo(HaveOccurred())
		var contents []string
		for _, hit := range hits {
			contents = append(contents, hit.Content)
		}
		Expect(contents).To(ConsistOf(incoming, "Continue", final, regenerated))
		entries, err := cs.ListEntriesForUser("", name)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(4))
		Expect(h.turns.Load()).To(Equal(int32(3)))
	})
})
