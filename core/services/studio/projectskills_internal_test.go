package studio

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

var _ = Describe("the project skills layer in chats and agents", func() {
	var (
		ctx   context.Context
		svc   *Service
		f     *files.Service
		ps    *projects.Service
		atlas projects.Project
		beta  projects.Project
	)
	admin := workspace.Actor{Principal: files.AdminPrincipal}
	home := session.HomePath("alice")
	put := func(rel, name, desc string) {
		GinkgoHelper()
		_, err := f.Store().Put(ctx, f.Actor("alice"), home+"/"+rel+"/SKILL.md", strings.NewReader(string(skillMD(name, desc, desc+" body."))), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	chatIn := func(p projects.Project) session.ChatRecord {
		GinkgoHelper()
		rec, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m", ProjectID: p.ID}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		got, err := svc.d.Store.GetChat(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		return got
	}
	plainChat := func() session.ChatRecord {
		GinkgoHelper()
		rec, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m"}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		return rec
	}
	// on returns the skills the chat gets: valid, not shadowed, not off.
	on := func(rec session.ChatRecord) map[string]skills.Skill {
		GinkgoHelper()
		sc := svc.scopeOf(ctx, rec)
		list, _, _, err := svc.chatSkillSet(ctx, "alice", rec, svc.skillsOffOf(ctx, rec, sc), rec.AddonsOff, sc, Commands{})
		Expect(err).NotTo(HaveOccurred())
		out := map[string]skills.Skill{}
		for _, sk := range list {
			if sk.Invalid == "" && sk.Shadowed == "" && !sk.Off {
				out[sk.Name] = sk
			}
		}
		return out
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
		st, _ := store.NewJSON("")
		ag, _ := agents.NewJSON("")
		pst, err := projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ps = projects.NewService(projects.Options{Store: pst, Files: f})
		svc, err = New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, Agents: ag, Projects: ps,
			ProposalGate: proposals.GateFunc(func(context.Context, proposals.Request) error { return nil })})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = svc.Close(context.Background())
			if raw, err := db.DB(); err == nil {
				_ = raw.Close()
			}
		})
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		Expect(f.EnsureHome(ctx, "bob")).To(Succeed())
		atlas, err = ps.Create(ctx, "alice", projects.CreateInput{Name: "Atlas"})
		Expect(err).NotTo(HaveOccurred())
		beta, err = ps.Create(ctx, "alice", projects.CreateInput{Name: "Beta"})
		Expect(err).NotTo(HaveOccurred())
		put("Projects/atlas/skills/plan", "plan", "Atlas plan.")
		put("Projects/beta/skills/other", "other", "Beta only.")
		put(".skills/review", "review", "Personal review.")
		put(".skills/notes", "notes", "Personal notes.")
	})

	It("gives a project's skills to the chats of that project only", func() {
		got := on(chatIn(atlas))
		Expect(got).To(HaveKey("plan"))
		Expect(got["plan"].Layer).To(Equal(skills.LayerProject))
		Expect(got).NotTo(HaveKey("other"), "another project's skill")
		Expect(got).To(HaveKey("review"))
		Expect(on(chatIn(beta))).NotTo(HaveKey("plan"))
		Expect(on(plainChat())).NotTo(HaveKey("plan"))
	})

	It("does not load a project's old .skills folder for a chat outside the project", func() {
		put("Projects/atlas/.skills/legacy", "legacy", "Old place.")
		Expect(on(plainChat())).NotTo(HaveKey("legacy"))
		Expect(on(chatIn(beta))).NotTo(HaveKey("legacy"))
		Expect(on(chatIn(atlas))).NotTo(HaveKey("legacy"))
	})

	It("lets the project's version win over the personal one", func() {
		put("Projects/atlas/skills/review", "review", "Atlas review.")
		Expect(on(chatIn(atlas))["review"].Layer).To(Equal(skills.LayerProject))
		Expect(on(plainChat())["review"].Layer).To(Equal(skills.LayerPersonal))
	})

	It("reads the project live, so a chat that already exists gets a skill added later", func() {
		rec := chatIn(atlas)
		Expect(on(rec)).NotTo(HaveKey("later"))
		put("Projects/atlas/skills/later", "later", "Added after the chat started.")
		Expect(on(rec)).To(HaveKey("later"))
	})

	It("is exempt from the project's skills limit, and the limit does not switch off a same-named skill", func() {
		put("Projects/atlas/skills/notes", "notes", "Atlas notes.")
		for _, p := range []projects.Project{atlas, beta} {
			_, err := ps.Update(ctx, "alice", p.ID, projects.UpdateInput{Defaults: &projects.Defaults{Skills: []string{"review"}}})
			Expect(err).NotTo(HaveOccurred())
		}
		// The limit switches off the personal skill outside it, in a project
		// with no skill of that name...
		Expect(chatIn(beta).SkillsOff).To(ContainElement("notes"))
		Expect(on(chatIn(beta))).NotTo(HaveKey("notes"))
		Expect(on(chatIn(beta))).To(HaveKey("review"))
		// ...and does not take the project's own skill of that name, nor the
		// skills it has that no list names.
		rec := chatIn(atlas)
		Expect(rec.SkillsOff).NotTo(ContainElement("notes"))
		got := on(rec)
		Expect(got["notes"].Layer).To(Equal(skills.LayerProject))
		Expect(got).To(HaveKey("plan"))
	})

	It("ignores a switch of the request for a project skill: the project holds it", func() {
		rec, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m", ProjectID: atlas.ID, Switches: Switches{SkillsOff: []string{"plan"}}}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		got, err := svc.d.Store.GetChat(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(on(got)).To(HaveKey("plan"))
	})

	It("is off while another person may write the project folder", func() {
		rec := chatIn(atlas)
		Expect(on(rec)).To(HaveKey("plan"))
		Expect(f.Store().Grant(ctx, admin, "bob", home+"/Projects/atlas", workspace.ModeRead)).To(Succeed())
		Expect(on(rec)).To(HaveKey("plan"))
		Expect(f.Store().Grant(ctx, admin, "bob", home+"/Projects/atlas", workspace.ModeWrite)).To(Succeed())
		Expect(on(rec)).NotTo(HaveKey("plan"))
		Expect(on(rec)).To(HaveKey("review"), "the owner's other skills are not affected")
		Expect(svc.ProjectSkillsBlocked(ctx, "alice", atlas.ID)).To(BeTrue())
		Expect(svc.ProjectSkillsBlocked(ctx, "alice", beta.ID)).To(BeFalse())
		_, blocked, err := svc.ProjectSkills(ctx, "alice", atlas.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(blocked).To(BeTrue())
	})

	It("takes a switch with the layer id, which still cannot turn off a project skill", func() {
		rec := chatIn(atlas)
		Expect(svc.SetSkillsOff(ctx, "alice", rec.ID, []string{"project:plan"})).To(Succeed())
		got, err := svc.d.Store.GetChat(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(on(got)).To(HaveKey("plan"))
		Expect(svc.SetSkillsOff(ctx, "alice", rec.ID, []string{"agent:plan"})).To(MatchError(ErrBadSkillName))
	})

	It("grants the project's skills to a project agent without listing them, and nothing else it was not given", func() {
		a, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Helper", Model: "m", ProjectID: atlas.ID, Skills: []string{"notes"}})
		Expect(err).NotTo(HaveOccurred())
		rec, err := svc.CreateAs(ctx, "alice", CreateInput{AgentID: a.ID}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		got := on(rec)
		Expect(got).To(HaveKey("plan"))
		Expect(got).To(HaveKey("notes"))
		Expect(got).NotTo(HaveKey("review"), "a personal skill the agent was not given")
		Expect(got).NotTo(HaveKey("other"))

		// A limit on the project that leaves the agent's own list alone does
		// not take the project's skills away from it.
		_, err = ps.Update(ctx, "alice", atlas.ID, projects.UpdateInput{Defaults: &projects.Defaults{Skills: []string{"review"}}})
		Expect(err).NotTo(HaveOccurred())
		got = on(rec)
		Expect(got).To(HaveKey("plan"))
		Expect(got).NotTo(HaveKey("notes"), "the limit still caps the agent's own list")
	})

	It("is the owner's: a project that is not the user's has no skills to list or write", func() {
		theirs, err := ps.Create(ctx, "bob", projects.CreateInput{Name: "Theirs"})
		Expect(err).NotTo(HaveOccurred())
		_, _, err = svc.ProjectSkills(ctx, "alice", theirs.ID)
		Expect(errors.Is(err, projects.ErrNotFound)).To(BeTrue(), "%v", err)
		_, err = svc.ProjectSkillDir(ctx, "alice", "s:12", true)
		Expect(errors.Is(err, projects.ErrNotFound)).To(BeTrue(), "%v", err)
		list, blocked, err := svc.ProjectSkills(ctx, "alice", atlas.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(blocked).To(BeFalse())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Name).To(Equal("plan"))
		Expect(list[0].Project).To(Equal("atlas"))
	})

	It("refuses to write the skills of an archived project, and still reads them", func() {
		_, err := ps.Archive(ctx, "alice", atlas.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.ProjectSkillDir(ctx, "alice", atlas.ID, true)
		Expect(errors.Is(err, session.ErrInvalid)).To(BeTrue(), "%v", err)
		dir, err := svc.ProjectSkillDir(ctx, "alice", atlas.ID, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(dir).To(Equal("atlas"))
	})
})
