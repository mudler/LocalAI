package studio

import (
	"context"
	"path/filepath"
	"sync"
	"testing/fstest"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// addonNotesSkill is the skill that the add-on provider in these specs ships.
const addonNotesSkill = "notes-recall"

// notesHost is the provider host of the specs with the skill of the provider.
type notesHost struct{ *callHost }

func (h notesHost) AddonSkills(context.Context, string) ([]AddonSkillSet, error) {
	return []AddonSkillSet{{AddonID: callProviderID, Title: "Notes service", Running: true, Skills: []string{addonNotesSkill}}}, nil
}

// The host grants the skills of the active memory provider to a chat that
// reads the notes: a saved agent never lists them, and one that was saved
// before still gets them. A chat that reads nothing (the agent chose none,
// Settings > Memory is Off, the chat is private) and a sender who may not read
// the personal notes get none.
var _ = Describe("the memory skills the host grants", func() {
	now := time.Unix(1_800_000_000, 0)
	builtinSkills := []string{"memory", "memory-check-own-files"}

	type env struct {
		svc    *Service
		stored map[string]string
		mu     sync.Mutex
	}
	build := func(addon bool) *env {
		GinkgoHelper()
		e := &env{stored: map[string]string{}}
		ctx := context.Background()
		db, err := sqlitedb.Open("file:"+filepath.Join(GinkgoT().TempDir(), "ws.db")+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		blobs, _ := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		f := files.New(ws)
		Expect(f.SyncBuiltin(ctx, builtinskills.FS())).To(Succeed())
		st, _ := store.NewJSON("")
		ag, _ := agents.NewJSON("")
		var broker AddonTools = &memBroker{}
		if addon {
			host := newCallHost()
			broker = notesHost{host}
			Expect(f.SyncAddonSkills(ctx, callProviderID, fstest.MapFS{addonNotesSkill + "/SKILL.md": {Data: md(addonNotesSkill, "Read the notes.")}})).To(Succeed())
		}
		e.svc, err = New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, Agents: ag, AddonTools: broker,
			Clock: func() time.Time { return now },
			SetUserSetting: func(_ context.Context, user, a, key, value string) error {
				e.mu.Lock()
				defer e.mu.Unlock()
				e.stored[user+"|"+a+"/"+key] = value
				return nil
			},
			UserSetting: func(_ context.Context, user, a, key string) (string, bool, error) {
				e.mu.Lock()
				defer e.mu.Unlock()
				v, ok := e.stored[user+"|"+a+"/"+key]
				return v, ok, nil
			},
			ProposalGate: proposals.GateFunc(func(context.Context, proposals.Request) error { return nil })})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = e.svc.Close(context.Background())
			if raw, err := db.DB(); err == nil {
				_ = raw.Close()
			}
		})
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		if addon {
			_, err = e.svc.SelectProvider(ctx, "alice", MemorySlot, callProviderID)
			Expect(err).NotTo(HaveOccurred())
		}
		return e
	}

	type sender struct {
		name   string
		bind   connectors.Binding
		id     string
		opened bool
	}
	owner := sender{"the owner", connectors.Binding{ID: "b1", Allow: []string{"owner"}, Paired: []connectors.PairedSender{{SenderID: "owner", At: now}}}, "owner", true}
	paired := sender{"a paired sender", connectors.Binding{ID: "b1", Allow: []string{"pal"}, Paired: []connectors.PairedSender{{SenderID: "pal", At: now}}}, "pal", true}
	unpaired := sender{"an unpaired sender", connectors.Binding{ID: "b1", Allow: []string{"friend"}}, "friend", false}

	// where the chat runs.
	type place struct {
		name   string
		origin Origin
		sender *sender
	}
	places := []place{
		{"a plain chat", Origin{}, nil},
		{"a trigger", Origin{Kind: session.OriginTrigger, ID: "t1"}, nil},
		{"a messaging chat of the owner", Origin{Kind: session.OriginConnector, ID: "b1"}, &owner},
		{"a messaging chat of a paired sender", Origin{Kind: session.OriginConnector, ID: "b1"}, &paired},
		{"a messaging chat of an unpaired sender", Origin{Kind: session.OriginConnector, ID: "b1"}, &unpaired},
	}
	// how memory stands.
	type state struct {
		name    string
		choice  string // the agent's choice
		setOff  bool   // Settings > Memory is Off
		private bool
		reads   bool
	}
	states := []state{
		{"on, the agent reads the notes", agents.MemoryMineAndOwn, false, false, true},
		{"on, the agent chose none", agents.MemoryNone, false, false, false},
		{"Off in Settings", agents.MemoryMineAndOwn, true, false, false},
		{"a private chat", agents.MemoryMineAndOwn, false, true, false},
	}

	// listed returns the skills that the index of the turn names.
	listed := func(e *env, agentID string, pl place, st state) string {
		GinkgoHelper()
		ctx := context.Background()
		sw := Switches{}
		if st.private {
			sw.BuiltinsOff = []string{"memory"}
		}
		if st.setOff {
			_, err := e.svc.SelectProvider(ctx, "alice", MemorySlot, contract.ProviderOff)
			Expect(err).NotTo(HaveOccurred())
		}
		rec, err := e.svc.CreateAs(ctx, "alice", CreateInput{Model: "m", AgentID: agentID, Switches: sw}, pl.origin)
		Expect(err).NotTo(HaveOccurred())
		if pl.sender != nil {
			e.svc.access.set(rec.ID, turnGrant{Binding: pl.sender.bind.ID, Kind: "chatty", Senders: []string{pl.sender.id}, Direct: true, Paired: pl.sender.bind.IsPaired(pl.sender.id, now)})
		}
		x, err := e.svc.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		tt, err := e.svc.chatTools(session.WithTurnMessage(ctx, "hello"), x)
		Expect(err).NotTo(HaveOccurred())
		return tt.Prompt
	}

	for _, addon := range []bool{false, true} {
		addon := addon
		provName, names := "the built-in provider", builtinSkills
		if addon {
			provName, names = "an add-on provider", []string{addonNotesSkill}
		}
		Describe(provName, func() {
			for _, pl := range places {
				for _, st := range states {
					pl, st := pl, st
					It("grants "+st.name+" in "+pl.name+" to a saved agent that does not list them", func() {
						e := build(addon)
						a, err := e.svc.SaveAgentWithFolder(context.Background(), "alice", agents.Agent{Name: "A", Model: "m", Memory: st.choice}, nil)
						Expect(err).NotTo(HaveOccurred())
						prompt := listed(e, a.ID, pl, st)
						want := st.reads && (pl.sender == nil || pl.sender.opened)
						for _, n := range names {
							if want {
								Expect(prompt).To(ContainSubstring("- "+n+":"), n)
							} else {
								Expect(prompt).NotTo(ContainSubstring("- "+n+":"), n)
							}
						}
					})
				}
			}

			for _, pl := range places[:2] {
				pl := pl
				It("grants them in "+pl.name+" without an agent", func() {
					e := build(addon)
					prompt := listed(e, "", pl, states[0])
					for _, n := range names {
						Expect(prompt).To(ContainSubstring("- " + n + ":"))
					}
				})
			}

			It("leaves a saved agent from before the memory choice as it was", func() {
				e := build(addon)
				a, err := e.svc.SaveAgentWithFolder(context.Background(), "alice", agents.Agent{Name: "Old", Model: "m"}, nil)
				Expect(err).NotTo(HaveOccurred())
				prompt := listed(e, a.ID, places[0], states[0])
				for _, n := range names {
					Expect(prompt).NotTo(ContainSubstring("- " + n + ":"))
				}
			})

			It("marks the skills as granted by the host in the list", func() {
				e := build(addon)
				list, _, err := e.svc.Skills(context.Background(), "alice", "")
				Expect(err).NotTo(HaveOccurred())
				locked := map[string]bool{}
				for _, sk := range list {
					if sk.Locked {
						Expect(sk.Provider).To(Equal(MemorySlot))
						locked[sk.Name] = true
					}
					if sk.Layer == skills.LayerBuiltin && sk.Name == "memory" && addon {
						Expect(sk.Locked).To(BeFalse(), "the built-in copy is not the provider's")
					}
				}
				for _, n := range names {
					Expect(locked).To(HaveKey(n))
				}
				Expect(locked).To(HaveLen(len(names)))
			})
		})
	}

	It("keeps a copy of the same name in the skills of the agent harmless", func() {
		e := build(false)
		a, err := e.svc.SaveAgentWithFolder(context.Background(), "alice", agents.Agent{Name: "A", Model: "m", Memory: agents.MemoryMineAndOwn, Skills: builtinSkills}, nil)
		Expect(err).NotTo(HaveOccurred())
		prompt := listed(e, a.ID, places[0], states[0])
		Expect(prompt).To(ContainSubstring("- memory:"))
		none := listed(e, a.ID, places[0], states[3])
		Expect(none).NotTo(ContainSubstring("- memory-check-own-files:"), "a private chat gets none, even when listed")
	})
})
