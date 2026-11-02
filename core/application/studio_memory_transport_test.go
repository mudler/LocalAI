//go:build auth

// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/addons/slack/slacktest"
	slack "github.com/mudler/LocalAI/addons/slack/transport"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/collections"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Real NewTriggers/Pass -> start -> durable capture worker -> application
// adapter -> Chromem/PostgreSQL, then Slack Socket Mode -> runtime -> connector
// manager -> bound chat -> actual search_collection tool. Only the model,
// embedding response and remote Slack platform are deterministic fixtures.
// This is not an HTTP webhook, external Slack service or multi-frontend test.
var _ = Describe("shared collection trigger-to-Slack transport acceptance", func() {
	DescribeTable("searches the curator capture with live principal and grant checks", func(engine string) {
		ctx := context.Background()
		embeddings := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"data":[{"embedding":[1,0,0],"index":0}]}`)
		}))
		DeferCleanup(embeddings.Close)
		cfg := config.NewApplicationConfig()
		cfg.DataPath = GinkgoT().TempDir()
		cfg.Collections.VectorEngine = engine
		if engine == "postgres" {
			cfg.Collections.DatabaseURL = os.Getenv("MEMORY_DATABASE_URL")
			if cfg.Collections.DatabaseURL == "" {
				Skip("MEMORY_DATABASE_URL is required for real PostgreSQL transport acceptance")
			}
		}
		cfg.Collections.APIURL, cfg.Collections.EmbeddingModel = embeddings.URL+"/v1", "test"
		cs, err := collections.New(cfg, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(cs.Close)
		name := fmt.Sprintf("transport_memory_%d", time.Now().UnixNano())
		const principal = "a9d95bda-b870-4a0b-9f6d-d186d080b123"
		const other = "b7d95bda-b870-4a0b-9f6d-d186d080b456"
		const text = "Curator reference: the launch window is Tuesday"
		Expect(cs.CreateForUser(principal, name)).To(Succeed())
		chats, err := store.NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		a := &Application{}
		a.collectionsService.Store(cs)
		adapter := studioCollections{a}
		h := &studiotest.ToolHarness{Script: func(_ workspace.Actor, message string) []studiotest.ToolStep {
			if strings.HasPrefix(message, text) {
				return []studiotest.ToolStep{{Say: "Recorded."}}
			}
			return []studiotest.ToolStep{
				{Tool: "search_collection", Args: fmt.Sprintf(`{"collection":%q,"query":"launch window"}`, name)},
				{Tool: "add_memory", Args: `{"content":"unauthorized Slack write"}`},
				{Tool: "delete_memory", Args: fmt.Sprintf(`{"collection":%q,"entry_id":"forbidden"}`, name)},
				{Say: "Done."},
			}
		}}
		svc, err := studio.New(studio.Deps{StateDir: GinkgoT().TempDir(), Store: chats, Agents: as, Provider: provider.New(), Collections: adapter, Harness: h})
		Expect(err).NotTo(HaveOccurred())
		a.studioService = svc
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		curator := agents.Agent{ID: "transport-curator", Principal: principal, Name: "Curator", Model: "m",
			CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: name}}},
			Memory:                &agents.MemorySettings{Version: 1, Destination: name, ToolStorage: "content", AutomaticIngestion: "incoming_only"}}
		Expect(as.Put(ctx, curator)).To(Succeed())
		ts, err := triggers.NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		var clock atomic.Int64
		clock.Store(time.Date(2026, 10, 27, 7, 0, 0, 0, time.UTC).UnixNano())
		host, err := studio.NewTriggers(svc, studio.TriggerOptions{Store: ts, Clock: func() time.Time { return time.Unix(0, clock.Load()).UTC() }})
		Expect(err).NotTo(HaveOccurred())
		svc.SetTriggers(host)
		DeferCleanup(host.Close)
		tr, err := host.CreateTrigger(ctx, principal, studio.TriggerInput{Name: "Curator capture", Kind: triggers.KindSchedule, Schedule: "daily 08:00", TZ: "UTC", Prompt: text, AgentID: curator.ID, Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Pass(ctx)).To(Succeed())
		clock.Add(int64(61 * time.Minute))
		Expect(host.Pass(ctx)).To(Succeed())
		var writer string
		Eventually(func(g Gomega) {
			runs, e := host.TriggerRuns(ctx, principal, tr.ID, 0)
			g.Expect(e).NotTo(HaveOccurred())
			g.Expect(runs).To(HaveLen(1))
			g.Expect(runs[0].Status).To(Equal(triggers.StatusDone))
			writer = runs[0].ChatID
		}, 10*time.Second).Should(Succeed())
		rec, err := svc.Get(ctx, principal, writer)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Origin).To(Equal(session.OriginTrigger))
		Expect(rec.OriginID).To(Equal(tr.ID))
		Expect(h.ToolNames(writer)).NotTo(ContainElement("add_memory"))
		workerCtx, cancelWorker := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- svc.ProcessMemoryCaptures(workerCtx) }()
		DeferCleanup(func() { cancelWorker(); Eventually(done, 5*time.Second).Should(Receive(MatchError(context.Canceled))) })
		var entryID string
		Eventually(func(g Gomega) {
			statuses, e := svc.MemoryStatuses(ctx, principal, writer)
			g.Expect(e).NotTo(HaveOccurred())
			g.Expect(statuses).To(HaveLen(1))
			g.Expect(statuses[0].Status).To(Equal("saved"))
			entryID = statuses[0].EntryID
			g.Expect(entryID).NotTo(BeEmpty())
		}, 10*time.Second).Should(Succeed())
		// Replaying the same due slot must not create a second run or capture.
		Expect(host.Pass(ctx)).To(Succeed())
		runs, err := host.TriggerRuns(ctx, principal, tr.ID, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(1))
		captures, err := chats.ListCaptures(ctx, principal, writer)
		Expect(err).NotTo(HaveOccurred())
		Expect(captures).To(HaveLen(1))
		Expect(captures[0].RawText).To(ContainSubstring(text))

		reader := agents.Agent{ID: "transport-reader", Principal: principal, Name: "Read-only peer", Model: "m",
			CollectionPermissions: &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: name, Search: true}}},
			Memory:                &agents.MemorySettings{Version: 1, ToolStorage: "content", AutomaticIngestion: "off"}}
		Expect(as.Put(ctx, reader)).To(Succeed())
		const bot = "xoxb-memory-bot"
		const app = "xapp-memory-app"
		platform := slacktest.New()
		DeferCleanup(platform.Close)
		platform.AddApp(slacktest.App{Bot: bot, AppToken: app, UserID: "UBOT"})
		platform.AddUser("U1", "ada", "Ada")
		rt := slack.New(&slack.API{BaseURL: platform.URL, FilesHost: platform.FilesHost})
		DeferCleanup(rt.Close)
		transport := studiobot.NewTransport(studiobot.Slack(rt))
		bindings, err := connectors.NewJSON(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		conns, err := studio.NewConnectors(svc, studio.ConnectorOptions{Store: bindings, Transport: transport, Tick: 20 * time.Millisecond, Backoff: 10 * time.Millisecond, RatePerMinute: 100})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(conns.Close)
		connCtx, cancelConn := context.WithCancel(ctx)
		DeferCleanup(cancelConn)
		go conns.Run(connCtx)
		bind := func(owner, agent, token string) connectors.Binding {
			b, e := conns.CreateConnector(ctx, owner, studio.ConnectorInput{Kind: "slack", Name: "Memory peer", Token: token, AgentID: agent, Allow: []string{"U1"}, Enabled: true, Reply: connectors.ReplyOff})
			Expect(e).NotTo(HaveOccurred())
			return b.Binding
		}
		b := bind(principal, reader.ID, bot+" "+app)
		Eventually(func() bool { return platform.Connected(app) }, 10*time.Second).Should(BeTrue())
		event := slacktest.DM("D1", "U1", "find the launch window")
		platform.Push(app, event)
		Eventually(func() int { return len(h.Results()) }, 10*time.Second).Should(Equal(3))
		results := h.Results()
		peer := results[0].Chat
		Expect(results[0].Err).NotTo(HaveOccurred())
		Expect(results[0].Out).To(ContainSubstring(text))
		Expect(results[0].Out).To(ContainSubstring(entryID))
		for _, r := range results[1:] {
			Expect(r.Err).To(MatchError(ContainSubstring("no tool named")))
		}
		Expect(h.ToolNames(peer)).NotTo(ContainElement("add_memory"))
		Expect(h.ToolNames(peer)).NotTo(ContainElement("delete_memory"))
		idle := func(owner, id string) {
			Eventually(func(g Gomega) {
				turn, e := svc.Turn(ctx, owner, id)
				g.Expect(e).NotTo(HaveOccurred())
				g.Expect(turn.Running).To(BeFalse())
				g.Expect(turn.Last).NotTo(BeEmpty())
			}, 10*time.Second).Should(Succeed())
		}
		idle(principal, peer)
		peerRec, err := svc.Get(ctx, principal, peer)
		Expect(err).NotTo(HaveOccurred())
		Expect(peerRec.OriginID).To(Equal(b.ID))
		// Same Slack message identity re-enters the public manager ingress used by
		// the real poll loop above, rather than a fabricated capture queue record.
		Expect(conns.Ingest(ctx, b.AddonID, contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{Chat: "D1", SenderID: "U1", MessageID: event["ts"].(string), Direct: true, Text: "find the launch window"}})).To(Succeed())
		Expect(h.Sent(peer)).To(HaveLen(1))
		Expect(h.Results()).To(HaveLen(3))
		statuses, err := svc.MemoryStatuses(ctx, principal, peer)
		Expect(err).NotTo(HaveOccurred())
		Expect(statuses).To(BeEmpty())

		reader.CollectionPermissions.Grants[0].Search = false
		Expect(as.Put(ctx, reader)).To(Succeed())
		platform.Push(app, slacktest.DM("D1", "U1", "search after revocation"))
		Eventually(func() int { return len(h.Results()) }, 10*time.Second).Should(Equal(6))
		Expect(h.Results()[3].Chat).To(Equal(peer), "revocation is checked on the already-bound chat")
		Expect(h.Results()[3].Err).To(HaveOccurred())
		Expect(h.Results()[3].Out).NotTo(ContainSubstring(entryID))
		idle(principal, peer)

		// A second binding with the same collection association cannot promote a
		// different principal, even though its own saved agent grants search.
		stranger := reader
		stranger.ID, stranger.Principal = "transport-stranger", other
		stranger.CollectionPermissions = &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: name, Search: true}}}
		Expect(as.Put(ctx, stranger)).To(Succeed())
		const otherBot, otherApp = "xoxb-other-memory", "xapp-other-memory"
		platform.AddApp(slacktest.App{Bot: otherBot, AppToken: otherApp, UserID: "UOTHER"})
		foreign := bind(other, stranger.ID, otherBot+" "+otherApp)
		Eventually(func() bool { return platform.Connected(otherApp) }, 10*time.Second).Should(BeTrue())
		platform.Push(otherApp, slacktest.DM("D2", "U1", "search another principal's collection"))
		Eventually(func() int { return len(h.Results()) }, 10*time.Second).Should(Equal(9))
		Expect(h.Results()[6].Err).To(HaveOccurred())
		Expect(h.Results()[6].Out).NotTo(ContainSubstring(entryID))
		foreignRec, err := svc.Get(ctx, other, h.Results()[6].Chat)
		Expect(err).NotTo(HaveOccurred())
		Expect(foreignRec.OriginID).To(Equal(foreign.ID))
		idle(other, foreignRec.ID)
		hits, err := adapter.Search(ctx, principal, name, "launch window", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(hits).To(HaveLen(1), "duplicate delivery and denied tools must leave one backend entry")
		Expect(hits[0].EntryID).To(Equal(entryID))
		captures, err = chats.ListCaptures(ctx, principal, writer)
		Expect(err).NotTo(HaveOccurred())
		Expect(captures).To(HaveLen(1))
	}, Entry("Chromem", "chromem"), Entry("PostgreSQL", "postgres"))
})
