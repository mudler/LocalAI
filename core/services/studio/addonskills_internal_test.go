package studio

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing/fstest"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// skillBroker stands in for the add-on broker: the add-ons that ship skills
// and run now, and the ones that cannot work for the user.
type skillBroker struct {
	mu      sync.Mutex
	sets    []AddonSkillSet
	reasons map[string]string
}

func (b *skillBroker) Tools(context.Context, string) ([]AddonTool, error) { return nil, nil }
func (b *skillBroker) Call(context.Context, string, string, string, json.RawMessage) (string, error) {
	return "", nil
}
func (b *skillBroker) AddonSkills(context.Context, string) ([]AddonSkillSet, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]AddonSkillSet(nil), b.sets...), nil
}
func (b *skillBroker) Unavailable(_ context.Context, _, addon string, _ bool) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reasons[addon]
}
func (b *skillBroker) set(sets ...AddonSkillSet) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sets = sets
}

func md(name, desc string) []byte {
	return []byte("---\nname: " + name + "\ndescription: " + desc + "\n---\nBody.\n")
}

// newSkillService is a chats service on a real workspace with a broker that
// ships add-on skills.
func newSkillService(b *skillBroker, pins ...map[string]string) (*Service, *files.Service) {
	dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := sqlitedb.Open(dsn, &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	blobs, _ := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	f := files.New(ws)
	st, _ := store.NewJSON("")
	d := Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, AddonTools: b}
	if len(pins) > 0 {
		d.ToolPins = func() map[string]string { return pins[0] }
	}
	svc, err := New(d)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		_ = svc.Close(context.Background())
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	Expect(f.EnsureHome(context.Background(), "alice")).To(Succeed())
	return svc, f
}

var _ = Describe("add-on skills in a chat", func() {
	var (
		ctx context.Context
		b   *skillBroker
		svc *Service
		f   *files.Service
	)
	BeforeEach(func() {
		ctx = context.Background()
		b = &skillBroker{}
		svc, f = newSkillService(b)
		Expect(f.SyncAddonSkills(ctx, "notes", fstest.MapFS{
			"notes/SKILL.md":       {Data: md("notes", "Use for notes.")},
			"notes-quick/SKILL.md": {Data: md("notes-quick", "Quick notes.")},
		})).To(Succeed())
		b.set(AddonSkillSet{AddonID: "notes", Title: "Notes", Skills: []string{"notes", "notes-quick"}, Running: true})
	})

	It("offers the skills of a usable add-on, with their guest path", func() {
		prompt, _, key, _ := svc.skillTurn(ctx, "alice", nil, nil, nil, nil)
		Expect(prompt).To(ContainSubstring("- notes: Use for notes. (read /workspace/shared-addon-skills/notes/notes/SKILL.md)"))
		Expect(prompt).To(ContainSubstring("notes-quick"))
		Expect(key).NotTo(BeEmpty())
	})

	It("offers nothing for an add-on the chat switched off, that stopped or that cannot work", func() {
		prompt, _, _, _ := svc.skillTurn(ctx, "alice", nil, []string{"notes"}, nil, nil)
		Expect(prompt).NotTo(ContainSubstring("shared-addon-skills"), "addons_off removes the add-on's root")
		b.reasons = map[string]string{"notes": "the Files workspace is off"}
		prompt, _, _, _ = svc.skillTurn(ctx, "alice", nil, nil, nil, nil)
		Expect(prompt).NotTo(ContainSubstring("shared-addon-skills"), "an unavailable add-on gives no skills")
		b.reasons = nil
		b.set(AddonSkillSet{AddonID: "notes", Title: "Notes", Skills: []string{"notes", "notes-quick"}}) // stopped
		prompt, _, _, _ = svc.skillTurn(ctx, "alice", nil, nil, nil, nil)
		Expect(prompt).NotTo(ContainSubstring("shared-addon-skills"), "a stopped add-on gives no skills; the files stay")
	})

	It("moves the refresh key when the add-on starts or stops, not on a switch", func() {
		_, _, running, _ := svc.skillTurn(ctx, "alice", nil, nil, nil, nil)
		_, _, switched, _ := svc.skillTurn(ctx, "alice", []string{"addon:notes"}, nil, nil, nil)
		Expect(switched).To(Equal(running), "a switch changed the key")
		b.set()
		_, _, stopped, _ := svc.skillTurn(ctx, "alice", nil, nil, nil, nil)
		Expect(stopped).NotTo(Equal(running), "a stop left the key as it was")
	})

	It("keeps an agent's grant: an add-on the agent does not grant gives no skills", func() {
		sc := &scope{addons: map[string]bool{}, skills: map[string]bool{"notes": true}}
		prompt, _, _, _ := svc.skillTurn(ctx, "alice", nil, nil, sc, nil)
		Expect(prompt).NotTo(ContainSubstring("shared-addon-skills"))
		sc.addons["notes"] = true
		prompt, _, _, _ = svc.skillTurn(ctx, "alice", nil, nil, sc, nil)
		Expect(prompt).To(ContainSubstring("- notes: Use for notes."))
		Expect(prompt).NotTo(ContainSubstring("notes-quick"), "the agent grants the skill notes only")
	})

	It("lists add-on skills with their add-on, and accepts the switch addon:<name>", func() {
		conv, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		list, _, err := svc.Skills(ctx, "alice", conv.ID)
		Expect(err).NotTo(HaveOccurred())
		var got *skills.Skill
		for i := range list {
			if list[i].Name == "notes" {
				got = &list[i]
			}
		}
		Expect(got).NotTo(BeNil())
		Expect(got.Layer).To(Equal(skills.LayerAddon))
		Expect(got.AddonTitle).To(Equal("Notes"))
		Expect(SkillID(*got)).To(Equal("addon:notes"))
		Expect(validSwitch("addon:notes")).To(BeTrue())
		Expect(svc.SetSkillsOff(ctx, "alice", conv.ID, []string{"addon:notes"})).To(Succeed())
		Expect(svc.SetAddonsOff(ctx, "alice", conv.ID, []string{"notes"})).To(Succeed())
		list, _, err = svc.Skills(ctx, "alice", conv.ID)
		Expect(err).NotTo(HaveOccurred())
		for _, sk := range list {
			Expect(sk.Layer).NotTo(Equal(skills.LayerAddon), "the chat switched the add-on off")
		}
	})

	It("lists the add-on layer as read-only in the guest", func() {
		Expect(strings.HasPrefix(files.SharedGuestPath(session.GuestRoot+"/shared-addon-skills/notes/notes/SKILL.md"), files.AddonSkillsPath)).To(BeTrue())
	})
})

var _ = Describe("add-on skills that replace built-in skills", func() {
	var (
		ctx  context.Context
		b    *skillBroker
		svc  *Service
		f    *files.Service
		pins map[string]string
	)
	office := func(running bool) AddonSkillSet {
		return AddonSkillSet{AddonID: "office", Title: "Office", Skills: []string{"xlsx", "docx"}, Shadows: []string{"xlsx", "docx"}, Running: running}
	}
	sheets := func(running bool) AddonSkillSet {
		return AddonSkillSet{AddonID: "sheets", Title: "Sheets", Skills: []string{"xlsx"}, Shadows: []string{"xlsx"}, Running: running}
	}
	BeforeEach(func() {
		ctx = context.Background()
		b = &skillBroker{}
		pins = map[string]string{}
		svc, f = newSkillService(b, pins)
		Expect(f.SyncBuiltin(ctx, fstest.MapFS{
			"xlsx/SKILL.md":           {Data: md("xlsx", "Built-in sheets.")},
			"docx/SKILL.md":           {Data: md("docx", "Built-in documents.")},
			"writing-skills/SKILL.md": {Data: md("writing-skills", "Built-in writing.")},
		})).To(Succeed())
		Expect(f.SyncAddonSkills(ctx, "office", fstest.MapFS{
			"xlsx/SKILL.md": {Data: md("xlsx", "Office sheets.")},
			"docx/SKILL.md": {Data: md("docx", "Office documents.")},
		})).To(Succeed())
		Expect(f.SyncAddonSkills(ctx, "sheets", fstest.MapFS{
			"xlsx/SKILL.md":           {Data: md("xlsx", "Sheets sheets.")},
			"writing-skills/SKILL.md": {Data: md("writing-skills", "A take-over.")},
		})).To(Succeed())
	})
	turn := func(off, addonsOff []string) (string, string, string) {
		prompt, _, key, fallback := svc.skillTurn(ctx, "alice", off, addonsOff, nil, nil)
		return prompt, key, fallback
	}

	It("lets a running add-on replace the built-in skill, and gives it back when the add-on stops", func() {
		b.set(office(true))
		prompt, _, fallback := turn(nil, nil)
		Expect(prompt).To(ContainSubstring("- xlsx: Office sheets. (read /workspace/shared-addon-skills/office/xlsx/SKILL.md)"))
		Expect(prompt).NotTo(ContainSubstring("Built-in sheets."))
		Expect(fallback).To(BeEmpty())
		b.set(office(false))
		prompt, _, fallback = turn(nil, nil)
		Expect(prompt).To(ContainSubstring("Built-in sheets."))
		Expect(fallback).To(ContainSubstring("xlsx (Office)"), "the turn says why the built-in skill serves")
	})

	It("gives the built-in skill back, with the notice, when the chat switches the add-on or its skill off", func() {
		b.set(office(true))
		prompt, _, fallback := turn(nil, []string{"office"})
		Expect(prompt).To(ContainSubstring("Built-in sheets."))
		Expect(fallback).To(ContainSubstring("docx (Office), xlsx (Office)"))
		prompt, _, fallback = turn([]string{"addon:xlsx"}, nil)
		Expect(prompt).To(ContainSubstring("Built-in sheets."))
		Expect(prompt).To(ContainSubstring("Office documents."))
		Expect(fallback).To(ContainSubstring("xlsx (Office)"))
		Expect(fallback).NotTo(ContainSubstring("docx"))
	})

	It("serves one add-on per name: the first by ID, or the pinned one", func() {
		b.set(office(true), sheets(true))
		prompt, key, _ := turn(nil, nil)
		Expect(prompt).To(ContainSubstring("Office sheets."))
		Expect(prompt).NotTo(ContainSubstring("Sheets sheets."))
		Expect(strings.Count(prompt, "- xlsx:")).To(Equal(1))
		pins["xlsx"] = "sheets"
		prompt, pinned, _ := turn(nil, nil)
		Expect(prompt).To(ContainSubstring("Sheets sheets."))
		Expect(prompt).NotTo(ContainSubstring("Office sheets."))
		Expect(pinned).NotTo(Equal(key), "a new provider moves the refresh key")
		list, _, err := svc.Skills(ctx, "alice", "")
		Expect(err).NotTo(HaveOccurred())
		for _, sk := range list {
			if sk.Layer == skills.LayerAddon && sk.Addon == "office" && sk.Name == "xlsx" {
				Expect(sk.Shadowed).To(Equal(string(skills.LayerAddon)))
				Expect(sk.ShadowedBy).To(Equal("sheets"), "the Skills list says who serves it")
			}
		}
	})

	It("lets the built-in serve, with a note, when the pinned add-on is stopped", func() {
		pins["xlsx"] = "sheets"
		b.set(office(true), sheets(false))
		prompt, _, fallback := turn(nil, nil)
		Expect(prompt).To(ContainSubstring("Built-in sheets."))
		Expect(prompt).NotTo(ContainSubstring("Office sheets."))
		Expect(fallback).To(ContainSubstring("xlsx (Sheets)"))
	})

	It("never lets an add-on take over a built-in skill that is not replaceable", func() {
		b.set(AddonSkillSet{AddonID: "sheets", Title: "Sheets", Skills: []string{"xlsx", "writing-skills"}, Shadows: []string{"xlsx"}, Running: true})
		prompt, _, _ := turn(nil, nil)
		Expect(prompt).To(ContainSubstring("Built-in writing."))
		Expect(prompt).NotTo(ContainSubstring("A take-over."))
	})

	It("lists the replaced skills for Settings > Tools, with who serves each and the candidates", func() {
		b.set(office(true), sheets(false))
		list := svc.AdminBuiltinSkills(ctx, "alice")
		Expect(list).To(HaveLen(2))
		Expect(list[0].ID).To(Equal("xlsx"))
		Expect(list[0].Candidates).To(Equal([]string{"office", "sheets"}))
		Expect(list[0].Titles).To(HaveKeyWithValue("sheets", "Sheets"))
		Expect(list[0].Provider).To(Equal("office"))
		Expect(list[1].ID).To(Equal("docx"))
		pins["xlsx"] = "sheets"
		list = svc.AdminBuiltinSkills(ctx, "alice")
		Expect(list[0].Pinned).To(Equal("sheets"))
		Expect(list[0].Provider).To(Equal(BuiltinProvider), "the pinned add-on is stopped")
		Expect(list[0].Note).NotTo(BeEmpty())
		b.set()
		Expect(svc.AdminBuiltinSkills(ctx, "alice")).To(BeEmpty(), "no add-on replaces a skill: no row")
	})

	It("gives every frontend on one store the same index, and the built-in skills when the owner is down", func() {
		b.set(office(true))
		other, err := New(Deps{Store: svc.d.Store, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, AddonTools: b})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = other.Close(ctx) })
		one, _, k1, _ := svc.skillTurn(ctx, "alice", nil, nil, nil, nil)
		two, _, k2, _ := other.skillTurn(ctx, "alice", nil, nil, nil, nil)
		Expect(two).To(Equal(one))
		Expect(k2).To(Equal(k1))
		down, err := New(Deps{Store: svc.d.Store, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, AddonTools: &skillBroker{}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = down.Close(ctx) })
		prompt, _, _, _ := down.skillTurn(ctx, "alice", nil, nil, nil, nil)
		Expect(prompt).To(ContainSubstring("Built-in sheets."))
	})
})
