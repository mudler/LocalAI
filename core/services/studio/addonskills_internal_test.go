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
func newSkillService(b *skillBroker) (*Service, *files.Service) {
	dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := sqlitedb.Open(dsn, &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	blobs, _ := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	f := files.New(ws)
	st, _ := store.NewJSON("")
	svc, err := New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, AddonTools: b})
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
		b.set(AddonSkillSet{AddonID: "notes", Title: "Notes", Skills: []string{"notes", "notes-quick"}})
	})

	It("offers the skills of a usable add-on, with their guest path", func() {
		prompt, _, key := svc.skillTurn(ctx, "alice", nil, nil, nil, nil)
		Expect(prompt).To(ContainSubstring("- notes: Use for notes. (read /workspace/shared-addon-skills/notes/notes/SKILL.md)"))
		Expect(prompt).To(ContainSubstring("notes-quick"))
		Expect(key).NotTo(BeEmpty())
	})

	It("offers nothing for an add-on the chat switched off, that stopped or that cannot work", func() {
		prompt, _, _ := svc.skillTurn(ctx, "alice", nil, []string{"notes"}, nil, nil)
		Expect(prompt).NotTo(ContainSubstring("shared-addon-skills"), "addons_off removes the add-on's root")
		b.reasons = map[string]string{"notes": "the Files workspace is off"}
		prompt, _, _ = svc.skillTurn(ctx, "alice", nil, nil, nil, nil)
		Expect(prompt).NotTo(ContainSubstring("shared-addon-skills"), "an unavailable add-on gives no skills")
		b.reasons = nil
		b.set() // stopped: the broker no longer lists it
		prompt, _, _ = svc.skillTurn(ctx, "alice", nil, nil, nil, nil)
		Expect(prompt).NotTo(ContainSubstring("shared-addon-skills"), "a stopped add-on gives no skills; the files stay")
	})

	It("moves the refresh key when the add-on starts or stops, not on a switch", func() {
		_, _, running := svc.skillTurn(ctx, "alice", nil, nil, nil, nil)
		_, _, switched := svc.skillTurn(ctx, "alice", []string{"addon:notes"}, nil, nil, nil)
		Expect(switched).To(Equal(running), "a switch changed the key")
		b.set()
		_, _, stopped := svc.skillTurn(ctx, "alice", nil, nil, nil, nil)
		Expect(stopped).NotTo(Equal(running), "a stop left the key as it was")
	})

	It("keeps an agent's grant: an add-on the agent does not grant gives no skills", func() {
		sc := &scope{addons: map[string]bool{}, skills: map[string]bool{"notes": true}}
		prompt, _, _ := svc.skillTurn(ctx, "alice", nil, nil, sc, nil)
		Expect(prompt).NotTo(ContainSubstring("shared-addon-skills"))
		sc.addons["notes"] = true
		prompt, _, _ = svc.skillTurn(ctx, "alice", nil, nil, sc, nil)
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
