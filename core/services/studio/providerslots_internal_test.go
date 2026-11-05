package studio

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
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

// fakeProvider stands in for the broker with two add-ons: one that declares the
// memory slot with its own index file, skill and role tools, and one that
// declares no slot. It is the conformance fixture for a providing add-on: the
// host sees only what a manifest declares.
type fakeProvider struct {
	mu         sync.Mutex
	installed  bool
	running    bool
	unreadable bool
}

const (
	fakeProviderID = "notes-by-fake"
	fakeOtherID    = "plain-tools"
)

func (b *fakeProvider) set(installed, running bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.installed, b.running = installed, running
}
func (b *fakeProvider) snapshot() (bool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.installed, b.running
}
func (b *fakeProvider) Tools(context.Context, string) ([]AddonTool, error) {
	installed, running := b.snapshot()
	var out []AddonTool
	schema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)
	if installed && running {
		out = append(out,
			AddonTool{Name: addontools.ToolName(fakeProviderID, "recall"), AddonID: fakeProviderID, Description: "recall a note", Schema: schema},
			AddonTool{Name: addontools.ToolName(fakeProviderID, "stats"), AddonID: fakeProviderID, Description: "not a role tool", Schema: schema})
	}
	return append(out, AddonTool{Name: addontools.ToolName(fakeOtherID, "ping"), AddonID: fakeOtherID, Description: "no slot", Schema: schema}), nil
}
func (b *fakeProvider) Call(context.Context, string, string, string, json.RawMessage) (string, error) {
	return "{}", nil
}
func (b *fakeProvider) Unavailable(context.Context, string, string, bool) string { return "" }
func (b *fakeProvider) AddonProviders(context.Context, string) ([]AddonProviderSet, error) {
	installed, running := b.snapshot()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.unreadable {
		return nil, context.DeadlineExceeded
	}
	if !installed {
		return nil, nil
	}
	return []AddonProviderSet{{AddonID: fakeProviderID, Title: "Fake notes", Description: "Keeps notes somewhere else", Running: running,
		Provides: []contract.ProviderDeclaration{{Slot: MemorySlot, SlotVersion: 1,
			Roles:      map[string]contract.ProviderTools{"read": {"recall"}},
			Properties: map[string]any{"remote": true, "undo": "none"}}}}}, nil
}
func (b *fakeProvider) AddonContextFiles(context.Context, string) ([]AddonContextSet, error) {
	installed, running := b.snapshot()
	if !installed {
		return nil, nil
	}
	return []AddonContextSet{{AddonID: fakeProviderID, Running: running, Files: []contract.ContextFile{{Scope: contract.ScopeUser, Path: "Memory/fake/index.md"}}}}, nil
}
func (b *fakeProvider) AddonSkills(context.Context, string) ([]AddonSkillSet, error) {
	installed, running := b.snapshot()
	if !installed {
		return nil, nil
	}
	return []AddonSkillSet{{AddonID: fakeProviderID, Title: "Fake notes", Running: running, Skills: []string{"fake-notes"}}}, nil
}

func builtinsChat(user string) builtins.Chat {
	return builtins.Chat{Actor: workspace.Actor{Principal: user}}
}

var _ = Describe("provider slots", func() {
	var (
		ctx    context.Context
		svc    *Service
		f      *files.Service
		br     *fakeProvider
		stored map[string]string
		smu    sync.Mutex
	)
	home := session.HomePath("alice")
	put := func(rel, text string) {
		GinkgoHelper()
		_, err := f.Store().Put(ctx, f.Actor("alice"), home+"/"+rel, strings.NewReader(text), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	turn := func(in CreateInput) session.TurnTools {
		GinkgoHelper()
		rec, err := svc.CreateAs(ctx, "alice", in, Origin{})
		Expect(err).NotTo(HaveOccurred())
		x, err := svc.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		tt, err := svc.chatTools(ctx, x)
		Expect(err).NotTo(HaveOccurred())
		return tt
	}
	toolNames := func(tt session.TurnTools) []string {
		var out []string
		for _, t := range tt.Tools {
			out = append(out, t.Name)
		}
		return out
	}
	skillNames := func() []string {
		GinkgoHelper()
		list, _, err := svc.Skills(ctx, "alice", "")
		Expect(err).NotTo(HaveOccurred())
		var out []string
		for _, sk := range list {
			out = append(out, sk.Name)
		}
		return out
	}
	// shipping lists the add-ons whose skills the chats offer now.
	shipping := func() []string {
		GinkgoHelper()
		ad, err := svc.addonSkillsFor(ctx, "alice", nil, nil, Commands{})
		Expect(err).NotTo(HaveOccurred())
		var out []string
		for _, set := range ad.usable {
			out = append(out, set.AddonID)
		}
		return out
	}
	recall := addontools.ToolName(fakeProviderID, "recall")
	stats := addontools.ToolName(fakeProviderID, "stats")

	BeforeEach(func() {
		ctx = context.Background()
		stored = map[string]string{}
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
		br = &fakeProvider{}
		svc, err = New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, Agents: ag, AddonTools: br,
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
		put("Memory/index.md", "- home: lives in Rome -> home.md\n")
		put("Memory/fake/index.md", "- pet: has a cat named Miso -> pet.md\n")
	})

	Describe("the list", func() {
		It("shows the built-in provider and Off when no add-on declares the slot", func() {
			v, err := svc.ProviderSlot(ctx, "alice", MemorySlot)
			Expect(err).NotTo(HaveOccurred())
			Expect(v.Providers).To(HaveLen(2))
			Expect(v.Providers[0].ID).To(Equal(contract.ProviderBuiltin))
			Expect(v.Providers[0].Selected).To(BeTrue())
			Expect(v.Providers[1].ID).To(Equal(contract.ProviderOff))
			Expect(v.Resolution.Active).To(Equal(contract.ProviderBuiltin))
			Expect(v.OffMeans).NotTo(BeEmpty())
		})

		It("adds an add-on that declares the slot, with what it declared, and no other", func() {
			br.set(true, true)
			v, err := svc.ProviderSlot(ctx, "alice", MemorySlot)
			Expect(err).NotTo(HaveOccurred())
			var ids []string
			for _, p := range v.Providers {
				ids = append(ids, p.ID)
			}
			Expect(ids).To(Equal([]string{contract.ProviderBuiltin, fakeProviderID, contract.ProviderOff}))
			p := v.Providers[1]
			Expect(p.Kind).To(Equal("addon"))
			Expect(p.Title).To(Equal("Fake notes"))
			Expect(p.Available).To(BeTrue())
			Expect(p.Roles).To(Equal([]string{"read"}))
			Expect(p.Properties).To(HaveKeyWithValue("remote", true))
		})

		It("lists every slot of the host", func() {
			all, err := svc.ProviderSlots(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(all).To(HaveLen(len(contract.ProviderSlots())))
			_, err = svc.ProviderSlot(ctx, "alice", "calendar")
			Expect(err).To(MatchError(ErrUnknownSlot))
		})
	})

	Describe("the choice", func() {
		It("refuses what the slot does not list", func() {
			br.set(true, true)
			_, err := svc.SelectProvider(ctx, "alice", "calendar", contract.ProviderBuiltin)
			Expect(err).To(MatchError(ErrUnknownSlot))
			_, err = svc.SelectProvider(ctx, "alice", MemorySlot, fakeOtherID)
			Expect(err).To(MatchError(ErrUnknownProvider), "an add-on that declares no slot is not a provider")
			_, err = svc.SelectProvider(ctx, "alice", MemorySlot, "missing")
			Expect(err).To(MatchError(ErrUnknownProvider))
		})

		It("refuses an add-on that cannot serve now", func() {
			br.set(true, false)
			_, err := svc.SelectProvider(ctx, "alice", MemorySlot, fakeProviderID)
			Expect(err).To(MatchError(ErrProviderUnavailable))
		})

		It("is stored per person and read back", func() {
			br.set(true, true)
			v, err := svc.SelectProvider(ctx, "alice", MemorySlot, fakeProviderID)
			Expect(err).NotTo(HaveOccurred())
			Expect(v.Resolution.Active).To(Equal(fakeProviderID))
			Expect(v.Resolution.Stored).To(BeTrue())
			Expect(svc.ResolveProvider(ctx, "alice", MemorySlot).Active).To(Equal(fakeProviderID))
			Expect(svc.ResolveProvider(ctx, "bob", MemorySlot).Active).To(Equal(contract.ProviderBuiltin), "another person keeps the default")
		})

		It("cannot be stored without a settings store", func() {
			svc.d.SetUserSetting = nil
			_, err := svc.SelectProvider(ctx, "alice", MemorySlot, contract.ProviderOff)
			Expect(err).To(MatchError(ErrProviderSettings))
		})

		It("keeps the On or Off switch of the notes in step", func() {
			br.set(true, true)
			_, err := svc.SelectProvider(ctx, "alice", MemorySlot, contract.ProviderOff)
			Expect(err).NotTo(HaveOccurred())
			m, err := svc.MemorySettings(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Enabled).To(BeFalse())
			Expect(m.Provider).To(Equal(contract.ProviderOff))

			on := true
			m, err = svc.SetMemory(ctx, "alice", MemoryUpdate{Enabled: &on})
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Enabled).To(BeTrue())
			Expect(m.Provider).To(Equal(contract.ProviderBuiltin), "On takes Off back to the built-in provider")

			_, err = svc.SelectProvider(ctx, "alice", MemorySlot, fakeProviderID)
			Expect(err).NotTo(HaveOccurred())
			m, err = svc.SetMemory(ctx, "alice", MemoryUpdate{Enabled: &on})
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Provider).To(Equal(fakeProviderID), "On keeps the add-on that was picked")

			off := false
			m, err = svc.SetMemory(ctx, "alice", MemoryUpdate{Enabled: &off})
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Enabled).To(BeFalse())
			Expect(svc.ResolveProvider(ctx, "alice", MemorySlot).Active).To(Equal(contract.ProviderOff))
		})
	})

	Describe("a chat", func() {
		It("reads the built-in notes by default and brings nothing of a provider that was not picked", func() {
			br.set(true, true)
			tt := turn(CreateInput{Model: "m"})
			Expect(tt.Prompt).To(ContainSubstring("lives in Rome"))
			Expect(tt.Prompt).NotTo(ContainSubstring("Miso"))
			names := toolNames(tt)
			Expect(names).NotTo(ContainElement(recall), "a role tool of a provider that was not picked")
			Expect(names).To(ContainElement(stats), "the other tools of the add-on are not gated")
			Expect(names).To(ContainElement(addontools.ToolName(fakeOtherID, "ping")))
			Expect(shipping()).NotTo(ContainElement(fakeProviderID))
			Expect(skillNames()).To(ContainElement("memory"))
		})

		It("reads the notes of the add-on that was picked, and not the built-in ones", func() {
			br.set(true, true)
			_, err := svc.SelectProvider(ctx, "alice", MemorySlot, fakeProviderID)
			Expect(err).NotTo(HaveOccurred())
			tt := turn(CreateInput{Model: "m"})
			Expect(tt.Prompt).To(ContainSubstring("Miso"))
			Expect(tt.Prompt).NotTo(ContainSubstring("lives in Rome"), "the built-in provider does not serve at the same time")
			Expect(strings.Count(tt.Prompt, contextfiles.Header)).To(Equal(1))
			Expect(toolNames(tt)).To(ContainElement(recall))
			Expect(shipping()).To(ContainElement(fakeProviderID))
			Expect(svc.memoryWritable(ctx, builtinsChat("alice"))).To(BeFalse(), "the memory kind of propose_change is the built-in provider's")
		})

		It("reads nothing when the person picked Off, whatever add-on is installed", func() {
			br.set(true, true)
			_, err := svc.SelectProvider(ctx, "alice", MemorySlot, contract.ProviderOff)
			Expect(err).NotTo(HaveOccurred())
			tt := turn(CreateInput{Model: "m"})
			Expect(tt.Prompt).NotTo(ContainSubstring("Miso"))
			Expect(tt.Prompt).NotTo(ContainSubstring("lives in Rome"))
			Expect(toolNames(tt)).NotTo(ContainElement(recall))
			Expect(shipping()).NotTo(ContainElement(fakeProviderID))
		})

		It("keeps the provider out of a private chat", func() {
			br.set(true, true)
			_, err := svc.SelectProvider(ctx, "alice", MemorySlot, fakeProviderID)
			Expect(err).NotTo(HaveOccurred())
			tt := turn(CreateInput{Model: "m", Switches: Switches{BuiltinsOff: []string{"memory"}}})
			Expect(tt.Prompt).NotTo(ContainSubstring("Miso"))
			Expect(toolNames(tt)).NotTo(ContainElement(recall))
			Expect(toolNames(tt)).To(ContainElement(stats), "only the role tools follow the slot")
		})
	})

	Describe("a provider that fails", func() {
		BeforeEach(func() {
			br.set(true, true)
			_, err := svc.SelectProvider(ctx, "alice", MemorySlot, fakeProviderID)
			Expect(err).NotTo(HaveOccurred())
		})

		failsClosed := func() {
			GinkgoHelper()
			res := svc.ResolveProvider(ctx, "alice", MemorySlot)
			Expect(res.Off).To(BeTrue())
			Expect(res.Active).To(Equal(contract.ProviderOff))
			Expect(res.Selected).To(Equal(fakeProviderID), "the choice stays, so the person sees what they picked")
			Expect(res.Code).To(Equal(ProviderUnavailable))
			Expect(res.AddonID).To(Equal(fakeProviderID))

			tt := turn(CreateInput{Model: "m"})
			Expect(tt.Prompt).NotTo(ContainSubstring("lives in Rome"), "never the built-in notes: the person did not choose them")
			Expect(tt.Prompt).NotTo(ContainSubstring("Miso"))
			Expect(tt.Prompt).To(ContainSubstring("Memory is off"))
			Expect(toolNames(tt)).NotTo(ContainElement(recall))
			Expect(shipping()).NotTo(ContainElement(fakeProviderID))
			Expect(skillNames()).To(ContainElement("memory"), "the skill stays listed; it is switched off for the chat, not removed")

			rec, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m"}, Origin{})
			Expect(err).NotTo(HaveOccurred())
			state := svc.ChatMemory(ctx, rec)
			Expect(state.On).To(BeFalse())
			Expect(state.Code).To(Equal(MemoryOffProvider))
			Expect(state.By).To(Equal("provider"))
			Expect(state.Provider).To(Equal(fakeProviderID))
			Expect(state.Fix).NotTo(BeNil())
			Expect(state.Fix.Kind).To(Equal(FixOpenMemorySettings))
			Expect(svc.memoryWritable(ctx, builtinsChat("alice"))).To(BeFalse())

			v, err := svc.ProviderSlot(ctx, "alice", MemorySlot)
			Expect(err).NotTo(HaveOccurred())
			var picked *ProviderView
			for i := range v.Providers {
				if v.Providers[i].Selected {
					picked = &v.Providers[i]
				}
			}
			Expect(picked).NotTo(BeNil())
			Expect(picked.ID).To(Equal(fakeProviderID))
			Expect(picked.Available).To(BeFalse())
		}

		It("turns the slot off when the add-on stops, and serves again when it runs", func() {
			br.set(true, false)
			failsClosed()
			br.set(true, true)
			Expect(svc.ResolveProvider(ctx, "alice", MemorySlot).Active).To(Equal(fakeProviderID))
			Expect(turn(CreateInput{Model: "m"}).Prompt).To(ContainSubstring("Miso"))
		})

		It("turns the slot off when the add-on is removed, and lets the person go back to the built-in provider", func() {
			br.set(false, false)
			failsClosed()
			_, err := svc.SelectProvider(ctx, "alice", MemorySlot, contract.ProviderBuiltin)
			Expect(err).NotTo(HaveOccurred())
			Expect(turn(CreateInput{Model: "m"}).Prompt).To(ContainSubstring("lives in Rome"))
		})

		It("turns the slot off when the add-ons cannot be listed", func() {
			br.mu.Lock()
			br.unreadable = true
			br.mu.Unlock()
			res := svc.ResolveProvider(ctx, "alice", MemorySlot)
			Expect(res.Off).To(BeTrue())
			Expect(res.Code).To(Equal(ProviderUnavailable))
			Expect(turn(CreateInput{Model: "m"}).Prompt).NotTo(ContainSubstring("lives in Rome"))
		})

		It("turns the slot off when the choice cannot be read", func() {
			svc.d.UserSetting = func(context.Context, string, string, string) (string, bool, error) {
				return "", false, context.DeadlineExceeded
			}
			res := svc.ResolveProvider(ctx, "alice", MemorySlot)
			Expect(res.Off).To(BeTrue())
			Expect(res.Code).To(Equal(ProviderUnreadable))
		})
	})

	It("keeps the answer of a person who switched memory off before there were providers", func() {
		smu.Lock()
		stored["alice|"+HostSettingsID+"/"+MemoryEnabledKey] = MemoryOff
		smu.Unlock()
		res := svc.ResolveProvider(ctx, "alice", MemorySlot)
		Expect(res.Off).To(BeTrue())
		Expect(res.Code).To(Equal(ProviderChosenOff))
		Expect(res.Stored).To(BeFalse())
		Expect(turn(CreateInput{Model: "m"}).Prompt).NotTo(ContainSubstring("lives in Rome"))
	})
})
