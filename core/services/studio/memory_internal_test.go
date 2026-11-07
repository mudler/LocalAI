package studio

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
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

// memBroker stands in for the add-on broker with the Memory add-on in the
// state a spec sets: absent, running or disabled.
type memBroker struct {
	mu        sync.Mutex
	installed bool
	running   bool
}

func (b *memBroker) Tools(context.Context, string) ([]AddonTool, error) { return nil, nil }
func (b *memBroker) Call(context.Context, string, string, string, json.RawMessage) (string, error) {
	return "", nil
}
func (b *memBroker) Unavailable(context.Context, string, string, bool) string { return "" }
func (b *memBroker) state(installed, running bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.installed, b.running = installed, running
}
func (b *memBroker) AddonContextFiles(context.Context, string) ([]AddonContextSet, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.installed {
		return nil, nil
	}
	return []AddonContextSet{{AddonID: "memory", Running: b.running, Files: []contract.ContextFile{
		{Scope: contract.ScopeUser, Path: "Memory/index.md"},
		{Scope: contract.ScopeAgent, Path: "Memory/agents/{agent}/index.md"},
		{Scope: contract.ScopeChannel, Path: "Memory/channels/{channel}/index.md"},
	}}}, nil
}
func (b *memBroker) AddonSkills(context.Context, string) ([]AddonSkillSet, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.installed {
		return nil, nil
	}
	return []AddonSkillSet{{AddonID: "memory", Title: "Short memory", Running: b.running, Skills: []string{"memory", "memory-check-own-files"}}}, nil
}
func (b *memBroker) AddonUserSettings(context.Context, string) ([]AddonUserSettingSet, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.installed {
		return nil, nil
	}
	return []AddonUserSettingSet{{AddonID: "memory", Running: b.running, Settings: []contract.UserSetting{
		{Key: "save-mode", Values: []string{"ask", "auto", "never"}, Default: "ask", Role: contract.UserSettingRoleMemorySave}}}}, nil
}

var _ = Describe("memory served by the host", func() {
	var (
		ctx    context.Context
		svc    *Service
		f      *files.Service
		br     *memBroker
		stored map[string]string
		smu    sync.Mutex
	)
	home := session.HomePath("alice")
	put := func(rel, text string) {
		GinkgoHelper()
		_, err := f.Store().Put(ctx, f.Actor("alice"), home+"/"+rel, strings.NewReader(text), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	setting := func(addon, key, value string) {
		smu.Lock()
		defer smu.Unlock()
		stored[addon+"/"+key] = value
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
	hasContextRead := func(tt session.TurnTools) bool {
		for _, t := range tt.Tools {
			if t.Name == contextfiles.ToolName {
				return true
			}
		}
		return false
	}
	skillState := func() map[string]skills.Skill {
		GinkgoHelper()
		list, _, err := svc.Skills(ctx, "alice", "")
		Expect(err).NotTo(HaveOccurred())
		out := map[string]skills.Skill{}
		for _, sk := range list {
			if sk.Name == "memory" || sk.Name == "memory-check-own-files" {
				
				out[sk.Name+"/"+string(sk.Layer)] = sk
			}
		}
		return out
	}

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
		br = &memBroker{}
		svc, err = New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, Agents: ag, AddonTools: br,
			SetUserSetting: func(_ context.Context, _, addon, key, value string) error {
				smu.Lock()
				defer smu.Unlock()
				stored[addon+"/"+key] = value
				return nil
			},
			UserSetting: func(_ context.Context, _, addon, key string) (string, bool, error) {
				smu.Lock()
				defer smu.Unlock()
				v, ok := stored[addon+"/"+key]
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
		put("Memory/home.md", "user detail")
	})

	It("loads the notes without the Memory add-on", func() {
		tt := turn(CreateInput{Model: "m"})
		Expect(tt.Prompt).To(ContainSubstring(contextfiles.Header))
		Expect(tt.Prompt).To(ContainSubstring("[user notes: memory]\n- home: lives in Rome -> home.md"))
		Expect(hasContextRead(tt)).To(BeTrue())
	})

	It("leaves the prompt alone while no note exists and no add-on is installed", func() {
		Expect(f.Store().Delete(ctx, f.Actor("alice"), home+"/Memory/index.md")).To(Succeed())
		tt := turn(CreateInput{Model: "m"})
		Expect(tt.Prompt).NotTo(ContainSubstring(contextfiles.Header))
	})

	It("keeps the same block for a person whose Memory add-on runs, and loads nothing twice", func() {
		absent := turn(CreateInput{Model: "m"})
		br.state(true, true)
		present := turn(CreateInput{Model: "m"})
		block := func(p string) string {
			i := strings.Index(p, contextfiles.Header)
			Expect(i).To(BeNumerically(">=", 0))
			return p[i:]
		}
		Expect(block(present.Prompt)).To(Equal(block(absent.Prompt)), "the add-on changes nothing the person sees")
		Expect(strings.Count(present.Prompt, "lives in Rome")).To(Equal(1))
		Expect(strings.Count(present.Prompt, contextfiles.Header)).To(Equal(1))
		Expect(present.RefreshKey).To(Equal(absent.RefreshKey))
	})

	It("keeps the header for a person whose add-on runs even before a note exists", func() {
		Expect(f.Store().Delete(ctx, f.Actor("alice"), home+"/Memory/index.md")).To(Succeed())
		br.state(true, true)
		Expect(turn(CreateInput{Model: "m"}).Prompt).To(ContainSubstring(contextfiles.Header))
	})

	It("reads nothing for a person who disabled the Memory add-on and never chose", func() {
		br.state(true, false)
		tt := turn(CreateInput{Model: "m"})
		Expect(tt.Prompt).NotTo(ContainSubstring("lives in Rome"))
		Expect(hasContextRead(tt)).To(BeFalse())
		for _, sk := range skillState() {
			if sk.Layer == skills.LayerBuiltin {
				Expect(sk.Invalid).To(BeEmpty())
			}
		}
		// The person's own answer wins over the disabled add-on.
		setting(HostSettingsID, MemoryEnabledKey, MemoryOn)
		Expect(turn(CreateInput{Model: "m"}).Prompt).To(ContainSubstring("lives in Rome"))
	})

	It("ships the two skills built in and ignores the copies of the add-on", func() {
		br.state(true, true)
		got := skillState()
		for _, n := range []string{"memory", "memory-check-own-files"} {
			Expect(got).To(HaveKey(n + "/builtin"))
			Expect(got[n+"/builtin"].Invalid).To(BeEmpty())
			Expect(got[n+"/builtin"].Shadowed).To(BeEmpty())
			Expect(got).NotTo(HaveKey(n + "/addon"))
		}
	})

	It("does not read for a saved agent that does not name the Memory add-on, as before", func() {
		plain, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Plain", Model: "m"}, nil)
		Expect(err).NotTo(HaveOccurred())
		granted, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Granted", Model: "m", Addons: []string{"memory"}}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn(CreateInput{Model: "m", AgentID: plain.ID}).Prompt).NotTo(ContainSubstring("lives in Rome"))
		Expect(turn(CreateInput{Model: "m", AgentID: granted.ID}).Prompt).To(ContainSubstring("lives in Rome"))
	})

	It("does not read when the chat switched the Memory add-on off", func() {
		Expect(turn(CreateInput{Model: "m", Switches: Switches{AddonsOff: []string{"memory"}}}).Prompt).NotTo(ContainSubstring("lives in Rome"))
	})

	Describe("the save level", func() {
		mode := func() string { return svc.Changes().storedSaveMode(ctx, "alice") }

		It("asks when nothing was chosen", func() { Expect(mode()).To(Equal(SaveAsk)) })

		It("keeps the value chosen on the page of the add-on", func() {
			br.state(true, true)
			setting("memory", "save-mode", "auto")
			Expect(mode()).To(Equal(SaveAuto))
			setting("memory", "save-mode", "never")
			Expect(mode()).To(Equal(SaveNever))
		})

		It("counts auto only while the add-on runs", func() {
			br.state(true, false)
			setting("memory", "save-mode", "auto")
			Expect(mode()).To(Equal(SaveAsk))
		})

		It("keeps never after the add-on is removed, and never brings auto back", func() {
			setting("memory", "save-mode", "never")
			Expect(mode()).To(Equal(SaveNever))
			setting("memory", "save-mode", "auto")
			Expect(mode()).To(Equal(SaveAsk))
		})

		It("reads the host's own setting first", func() {
			br.state(true, true)
			setting("memory", "save-mode", "never")
			setting(HostSettingsID, MemorySaveKey, "auto")
			Expect(mode()).To(Equal(SaveAuto))
			setting(HostSettingsID, MemorySaveKey, "bogus")
			Expect(mode()).To(Equal(SaveNever), "an unknown value is not a choice")
		})
	})

	Describe("Off and private chats", func() {
		hasTool := func(tt session.TurnTools, name string) bool {
			for _, t := range tt.Tools {
				if t.Name == name {
					return true
				}
			}
			return false
		}
		offLine := "Memory is off"
		expectOff := func(tt session.TurnTools, why string) {
			GinkgoHelper()
			Expect(tt.Prompt).NotTo(ContainSubstring("lives in Rome"), why)
			Expect(tt.Prompt).NotTo(ContainSubstring(contextfiles.Header), why)
			Expect(tt.Prompt).To(ContainSubstring(offLine), why)
			Expect(strings.Count(tt.Prompt, offLine)).To(Equal(1), why)
			Expect(hasTool(tt, contextfiles.ToolName)).To(BeFalse(), why)
			for _, t := range tt.Tools {
				if t.Name == "propose_change" {
					// The other kinds of change stay; the memory kind goes.
					Expect(string(t.Schema)).NotTo(ContainSubstring(`"memory"`), why+": no note is proposed")
				}
			}
		}

		It("offers notes and the save tool while memory is on", func() {
			tt := turn(CreateInput{Model: "m"})
			Expect(tt.Prompt).To(ContainSubstring("lives in Rome"))
			Expect(tt.Prompt).NotTo(ContainSubstring(offLine))
			var schema string
			for _, t := range tt.Tools {
				if t.Name == "propose_change" {
					schema = string(t.Schema)
				}
			}
			Expect(schema).To(ContainSubstring(`"memory"`))
		})

		It("turns everything off for a private chat, and only that chat", func() {
			expectOff(turn(CreateInput{Model: "m", Switches: Switches{BuiltinsOff: []string{"memory"}}}), "private chat")
			Expect(turn(CreateInput{Model: "m"}).Prompt).To(ContainSubstring("lives in Rome"))
		})

		It("turns everything off for a person who switched memory off, whatever else says", func() {
			setting(HostSettingsID, MemoryEnabledKey, MemoryOff)
			expectOff(turn(CreateInput{Model: "m"}), "user off")
			agent, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Granted", Model: "m", Addons: []string{"memory"},
				Builtins: []string{contract.CapMemoryWrite}}, nil)
			Expect(err).NotTo(HaveOccurred())
			expectOff(turn(CreateInput{Model: "m", AgentID: agent.ID}), "a saved agent cannot widen the choice of the person")
			// Nothing in the chat can widen it either.
			expectOff(turn(CreateInput{Model: "m", Switches: Switches{AddonsOff: nil, BuiltinsOff: nil}}), "chat switches only narrow")
		})

		It("honours it in a run that started by itself", func() {
			setting(HostSettingsID, MemoryEnabledKey, MemoryOff)
			rec, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m"}, Origin{Kind: session.OriginTrigger, ID: "t1"})
			Expect(err).NotTo(HaveOccurred())
			x, err := svc.ChatAccess(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			tt, err := svc.chatTools(ctx, x)
			Expect(err).NotTo(HaveOccurred())
			Expect(tt.Prompt).NotTo(ContainSubstring("lives in Rome"))
			Expect(tt.Prompt).To(ContainSubstring(offLine))
		})

		It("honours the memory switch of the agent", func() {
			agent, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Quiet", Model: "m", Addons: []string{"memory"},
				Builtins: []string{contract.CapMemoryWrite}, BuiltinsOff: []string{"memory"}}, nil)
			Expect(err).NotTo(HaveOccurred())
			tt := turn(CreateInput{Model: "m", AgentID: agent.ID})
			Expect(tt.Prompt).NotTo(ContainSubstring("lives in Rome"))
			Expect(tt.Prompt).To(ContainSubstring("for this agent"))
		})

		It("takes the two memory skills away while memory is off", func() {
			rec, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m", Switches: Switches{BuiltinsOff: []string{"memory"}}}, Origin{})
			Expect(err).NotTo(HaveOccurred())
			list, _, err := svc.Skills(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			seen := 0
			for _, sk := range list {
				if sk.Name == "memory" || sk.Name == "memory-check-own-files" {
					seen++
					Expect(sk.Off).To(BeTrue(), sk.Name)
				}
			}
			Expect(seen).To(Equal(2))
		})

		It("says which level is off, and where to turn it on", func() {
			rec, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m", Switches: Switches{BuiltinsOff: []string{"memory"}}}, Origin{})
			Expect(err).NotTo(HaveOccurred())
			r := svc.ChatMemory(ctx, rec)
			Expect(r.On).To(BeFalse())
			Expect(r.Code).To(Equal(MemoryOffChat))
			Expect(r.By).To(Equal("chat"))
			Expect(r.Fix).To(Equal(&Fix{Kind: FixOpenToolsMenu, Target: "memory"}))
			setting(HostSettingsID, MemoryEnabledKey, MemoryOff)
			plain, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m"}, Origin{})
			Expect(err).NotTo(HaveOccurred())
			r = svc.ChatMemory(ctx, plain)
			Expect(r.Code).To(Equal(MemoryOffUser))
			Expect(r.Fix.Kind).To(Equal(FixOpenMemorySettings))
			Expect(svc.ChatMemory(ctx, rec).Code).To(Equal(MemoryOffChat), "the narrowest level is the one named")
		})

		It("lists the memory group in the Tools menu, off for a private chat and unusable when the person switched it off", func() {
			panel, err := svc.DraftTools(ctx, "alice", "m", "")
			Expect(err).NotTo(HaveOccurred())
			var got *BuiltinGroup
			for i := range panel.Builtins {
				if panel.Builtins[i].Group == "memory" {
					got = &panel.Builtins[i]
				}
			}
			Expect(got).NotTo(BeNil())
			Expect(got.Off).To(BeFalse())
			Expect(got.Usable).To(BeTrue())
			Expect(got.Title).To(Equal("Memory"))
			setting(HostSettingsID, MemoryEnabledKey, MemoryOff)
			panel, err = svc.DraftTools(ctx, "alice", "m", "")
			Expect(err).NotTo(HaveOccurred())
			for _, g := range panel.Builtins {
				if g.Group == "memory" {
					Expect(g.Usable).To(BeFalse())
					Expect(g.Reason).To(ContainSubstring("off in your settings"))
				}
			}
			Expect(panel.Memory.Code).To(Equal(MemoryOffUser))
		})

		It("keeps the notes viewable and exportable while memory is off", func() {
			setting(HostSettingsID, MemoryEnabledKey, MemoryOff)
			list, err := svc.ListContextFiles(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(list.Groups).NotTo(BeEmpty())
			var buf bytes.Buffer
			n, err := svc.ExportMemory(ctx, "alice", &buf, "", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(2))
			zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			Expect(err).NotTo(HaveOccurred())
			var names []string
			for _, f := range zr.File {
				names = append(names, f.Name)
			}
			Expect(names).To(ConsistOf("Memory/index.md", "Memory/home.md"))
			_, err = svc.ExportMemory(ctx, "alice", &bytes.Buffer{}, "agent", "nobody")
			Expect(err).To(MatchError(ErrNoNotes))
		})

		It("stores the switch and the save level with the person's settings", func() {
			m, err := svc.MemorySettings(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(m).To(Equal(MemorySettings{Enabled: true, SaveMode: SaveAsk, MessagingSave: MessagingAsk, Provider: "builtin", Available: true}))
			off, auto := false, SaveAuto
			m, err = svc.SetMemory(ctx, "alice", MemoryUpdate{Enabled: &off, SaveMode: &auto})
			Expect(err).NotTo(HaveOccurred())
			Expect(m.Enabled).To(BeFalse())
			Expect(m.EnabledSet).To(BeTrue())
			Expect(m.SaveMode).To(Equal(SaveAuto))
			Expect(m.SaveModeSet).To(BeTrue())
			bad := "everything"
			_, err = svc.SetMemory(ctx, "alice", MemoryUpdate{SaveMode: &bad})
			Expect(err).To(MatchError(ErrInvalidSettings))
		})

		It("stores Always confirm and the messaging row, and refuses a value that is not one", func() {
			always, row := SaveAlways, MessagingPairedPrivate
			m, err := svc.SetMemory(ctx, "alice", MemoryUpdate{SaveMode: &always, MessagingSave: &row})
			Expect(err).NotTo(HaveOccurred())
			Expect(m.SaveMode).To(Equal(SaveAlways))
			Expect(m.MessagingSave).To(Equal(MessagingPairedPrivate))
			// A field left out stays as it is.
			never := SaveNever
			m, err = svc.SetMemory(ctx, "alice", MemoryUpdate{SaveMode: &never})
			Expect(err).NotTo(HaveOccurred())
			Expect(m.MessagingSave).To(Equal(MessagingPairedPrivate))
			for _, bad := range []string{"", "auto", "anyone", "paired"} {
				_, err = svc.SetMemory(ctx, "alice", MemoryUpdate{MessagingSave: &bad})
				Expect(err).To(MatchError(ErrInvalidSettings), bad)
			}
			m, err = svc.MemorySettings(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(m.MessagingSave).To(Equal(MessagingPairedPrivate), "a refused value changes nothing")
		})
	})
})
