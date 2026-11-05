package studio

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// The personal notes of the owner in a messaging chat: the user and agent
// notes are read into the turn only for a sender who is paired with the bot.
// The agent profile (the standing rules of the agent) is not a personal note:
// every sender gets it, like the instructions of the agent. The notes of the conversation itself are read for every
// sender. Both the built-in provider and an add-on served through the provider
// slot follow the rule.
var _ = Describe("the personal notes in a messaging chat", func() {
	const (
		userNote, agentNote, channelNote, profileRule = "lives in Rome", "keeps answers terse", "this group plays chess", "always greet in Latin"
		other                                         = "stranger"
	)
	now := time.Unix(1_800_000_000, 0)

	// The four kinds of sender. The access chain gives a turn Paired for the
	// owner (who pairs with their own bot) and for a paired sender, and not for
	// the others, whatever way they were let in. Each row builds its grant from
	// a binding, so the flag is the one IsPaired says.
	type sender struct {
		name   string
		bind   connectors.Binding
		id     string
		opened bool
	}
	senders := []sender{
		{"the owner", connectors.Binding{ID: "b1", Allow: []string{"owner"}, Paired: []connectors.PairedSender{{SenderID: "owner", At: now}}}, "owner", true},
		{"a paired sender", connectors.Binding{ID: "b1", Allow: []string{"pal"}, Paired: []connectors.PairedSender{{SenderID: "pal", At: now}}}, "pal", true},
		{"an allowlisted sender who is not paired", connectors.Binding{ID: "b1", Allow: []string{"friend"}}, "friend", false},
		{"anyone, in an open channel", connectors.Binding{ID: "b1", AcceptAny: true}, other, false},
	}
	// What each choice of the agent reads when the sender is open.
	choices := []struct {
		name, choice string
		user, agent  bool
	}{
		{"own", agents.MemoryOwn, false, true},
		{"mine", agents.MemoryMine, true, false},
		{"mine_and_own", agents.MemoryMineAndOwn, true, true},
	}

	grantOf := func(s sender) turnGrant {
		return turnGrant{Binding: s.bind.ID, Kind: "chatty", Senders: []string{s.id}, Direct: true, Paired: s.bind.IsPaired(s.id, now)}
	}

	Describe("the built-in provider", func() {
		var (
			ctx context.Context
			svc *Service
			f   *files.Service
		)
		home := session.HomePath("alice")
		put := func(rel, text string) {
			GinkgoHelper()
			_, err := f.Store().Put(ctx, f.Actor("alice"), home+"/"+rel, strings.NewReader(text), workspace.PutOptions{Overwrite: true})
			Expect(err).NotTo(HaveOccurred())
		}
		BeforeEach(func() {
			ctx = context.Background()
			dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
			db, err := sqlitedb.Open(dsn, &gorm.Config{})
			Expect(err).NotTo(HaveOccurred())
			blobs, _ := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
			ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
			Expect(err).NotTo(HaveOccurred())
			f = files.New(ws)
			Expect(f.SyncBuiltin(ctx, builtinskills.FS())).To(Succeed())
			st, _ := store.NewJSON("")
			ag, _ := agents.NewJSON("")
			svc, err = New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, Agents: ag, AddonTools: &memBroker{},
				Clock:        func() time.Time { return now },
				ProposalGate: proposals.GateFunc(func(context.Context, proposals.Request) error { return nil })})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_ = svc.Close(context.Background())
				if raw, err := db.DB(); err == nil {
					_ = raw.Close()
				}
			})
			Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
			put("Memory/index.md", "- home: "+userNote+" -> home.md\n")
		})

		// turnFor starts a messaging chat of the agent for a sender and builds
		// its turn.
		turnFor := func(a agents.Agent, s sender) session.TurnTools {
			GinkgoHelper()
			rec, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m", AgentID: a.ID}, Origin{Kind: session.OriginConnector, ID: "b1"})
			Expect(err).NotTo(HaveOccurred())
			dir := proposals.ChannelDir(contract.ChangeSource{ID: rec.OriginID, Chat: rec.ID})
			put("Memory/channels/"+dir+"/index.md", "- game: "+channelNote+" -> game.md\n")
			svc.access.set(rec.ID, grantOf(s))
			x, err := svc.ChatAccess(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			tt, err := svc.chatTools(session.WithTurnMessage(ctx, "hello"), x)
			Expect(err).NotTo(HaveOccurred())
			return tt
		}
		savedAgent := func(choice string) agents.Agent {
			GinkgoHelper()
			a, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "A-" + choice, Model: "m", Memory: choice}, nil)
			Expect(err).NotTo(HaveOccurred())
			put("Memory/agents/"+a.ID+"/index.md", "- style: "+agentNote+" -> style.md\n")
			put(contextfiles.ProfilePath(a.ID), profileRule+"\n")
			return a
		}
		contextRead := func(tt session.TurnTools) string {
			for _, t := range tt.Tools {
				if t.Name == contextfiles.ToolName {
					return string(t.Schema)
				}
			}
			return ""
		}

		for _, c := range choices {
			for _, s := range senders {
				It("reads the right notes for "+s.name+" with the choice "+c.name, func() {
					tt := turnFor(savedAgent(c.choice), s)
					wantUser, wantAgent := s.opened && c.user, s.opened && c.agent
					Expect(strings.Contains(tt.Prompt, userNote)).To(Equal(wantUser), "the notes of the owner")
					Expect(strings.Contains(tt.Prompt, agentNote)).To(Equal(wantAgent), "the notes of the agent")
					Expect(tt.Prompt).To(ContainSubstring(profileRule), "the profile of the agent is for every sender")
					Expect(tt.Prompt).To(ContainSubstring(channelNote), "the notes of the conversation are for every sender")
					schema := contextRead(tt)
					Expect(schema).NotTo(BeEmpty(), "the conversation notes can be read")
					Expect(strings.Contains(schema, `"user"`)).To(Equal(wantUser))
					Expect(strings.Contains(schema, `"agent"`)).To(Equal(wantAgent))
				})
			}
		}

		It("reads nothing of the owner when there is no grant (an expired or missing sender)", func() {
			a := savedAgent(agents.MemoryMineAndOwn)
			rec, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m", AgentID: a.ID}, Origin{Kind: session.OriginConnector, ID: "b1"})
			Expect(err).NotTo(HaveOccurred())
			x, err := svc.ChatAccess(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			tt, err := svc.chatTools(session.WithTurnMessage(ctx, "hello"), x)
			Expect(err).NotTo(HaveOccurred())
			Expect(tt.Prompt).NotTo(ContainSubstring(userNote))
			Expect(tt.Prompt).NotTo(ContainSubstring(agentNote))
			Expect(tt.Prompt).To(ContainSubstring(profileRule), "the profile is not gated, even without a known sender")
		})

		It("leaves a chat outside a messaging app unchanged", func() {
			a := savedAgent(agents.MemoryMineAndOwn)
			rec, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m", AgentID: a.ID}, Origin{})
			Expect(err).NotTo(HaveOccurred())
			x, err := svc.ChatAccess(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			tt, err := svc.chatTools(session.WithTurnMessage(ctx, "hello"), x)
			Expect(err).NotTo(HaveOccurred())
			Expect(tt.Prompt).To(ContainSubstring(userNote))
			Expect(tt.Prompt).To(ContainSubstring(agentNote))
		})
	})

	Describe("an add-on served through the provider slot", func() {
		var (
			ctx    context.Context
			svc    *Service
			host   *callHost
			stored map[string]string
			smu    sync.Mutex
		)
		BeforeEach(func() {
			ctx = context.Background()
			stored = map[string]string{}
			dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
			db, err := sqlitedb.Open(dsn, &gorm.Config{})
			Expect(err).NotTo(HaveOccurred())
			blobs, _ := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
			ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
			Expect(err).NotTo(HaveOccurred())
			f := files.New(ws)
			Expect(f.SyncBuiltin(ctx, builtinskills.FS())).To(Succeed())
			st, _ := store.NewJSON("")
			ag, _ := agents.NewJSON("")
			host = newCallHost()
			host.on("context", func(map[string]any) (string, error) { return result(map[string]any{"text": "- likes tea"}), nil })
			svc, err = New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, Agents: ag, AddonTools: host,
				Clock: func() time.Time { return now },
				SetUserSetting: func(_ context.Context, user, addon, key, value string) error {
					smu.Lock()
					defer smu.Unlock()
					stored[user+"|"+addon+"/"+key] = value
					return nil
				},
				UserSetting: func(_ context.Context, user, addon, key string) (string, bool, error) {
					smu.Lock()
					defer smu.Unlock()
					v, ok := stored[user+"|"+addon+"/"+key]
					return v, ok, nil
				},
				ProposalGate: proposals.GateFunc(func(context.Context, proposals.Request) error { return nil })})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_ = svc.Close(context.Background())
				if raw, err := db.DB(); err == nil {
					_ = raw.Close()
				}
			})
			Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
			_, err = svc.SelectProvider(ctx, "alice", MemorySlot, callProviderID)
			Expect(err).NotTo(HaveOccurred())
		})

		for _, c := range choices {
			for _, s := range senders {
				It("asks the add-on for the right scopes for "+s.name+" with the choice "+c.name, func() {
					a, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "A-" + c.choice, Model: "m", Memory: c.choice}, nil)
					Expect(err).NotTo(HaveOccurred())
					rec, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m", AgentID: a.ID}, Origin{Kind: session.OriginConnector, ID: "b1"})
					Expect(err).NotTo(HaveOccurred())
					svc.access.set(rec.ID, grantOf(s))
					x, err := svc.ChatAccess(ctx, "alice", rec.ID)
					Expect(err).NotTo(HaveOccurred())
					_, err = svc.chatTools(session.WithTurnMessage(ctx, "hello"), x)
					Expect(err).NotTo(HaveOccurred())
					want := []string{"channel"}
					if s.opened {
						want = nil
						if c.user {
							want = append(want, "user")
						}
						if c.agent {
							want = append(want, "agent")
						}
						want = append(want, "channel")
					}
					Expect(host.count("context")).To(Equal(1))
					raw, _ := json.Marshal(host.sentTo("context")["scopes"])
					var got []string
					Expect(json.Unmarshal(raw, &got)).To(Succeed())
					Expect(got).To(ConsistOf(want))
				})
			}
		}
	})
})
