package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
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

// The accounts are opaque ids that differ from every display name, so a spec
// can tell a name from an account in any text the owner reads.
const (
	sOwner = "u-7f3a-owner"
	sBob   = "u-91c2-bob"
	sCarol = "u-44d0-carol"
	sDave  = "u-5e17-dave"
	sErin  = "u-0b8c-erin"
)

var _ = Describe("a note for a project shared with the chat", func() {
	var (
		ctx    context.Context
		svc    *Service
		f      *files.Service
		ps     *projects.Service
		st     store.Store
		atlas  projects.Project
		mu     sync.Mutex
		saving map[string]string
	)
	names := map[string]string{sOwner: "Olivia Owner", sBob: "Bobby Grantee", sCarol: "Carol Grantee", sDave: "Dave Grantee", sErin: "Erin Grantee"}
	admin := workspace.Actor{Principal: files.AdminPrincipal}

	mk := func(owner, name string) projects.Project {
		GinkgoHelper()
		p, err := ps.Create(ctx, owner, projects.CreateInput{Name: name})
		Expect(err).NotTo(HaveOccurred())
		return p
	}
	abs := func(owner string, p projects.Project) string {
		GinkgoHelper()
		a, err := f.Abs(owner, "Projects/"+p.Slug)
		Expect(err).NotTo(HaveOccurred())
		return a
	}
	grant := func(who string, owner string, p projects.Project, m workspace.Mode) {
		GinkgoHelper()
		Expect(f.Store().Grant(ctx, admin, who, abs(owner, p), m)).To(Succeed())
	}
	revoke := func(who, owner string, p projects.Project) {
		GinkgoHelper()
		Expect(f.Store().Revoke(ctx, admin, who, abs(owner, p))).To(Succeed())
	}
	ref := func(p projects.Project) string { return projects.SharedRef(p.Node) }
	// chatOf makes a plain chat of user that pulls the given references.
	chatOf := func(user string, pulled ...string) (session.Tool, session.ChatRecord) {
		GinkgoHelper()
		Expect(f.EnsureHome(ctx, user)).To(Succeed())
		rec, err := svc.CreateAs(ctx, user, CreateInput{Model: "m"}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		if len(pulled) > 0 {
			Expect(svc.SetChatPulled(ctx, user, rec.ID, pulled)).To(Succeed())
		}
		return toolOf(ctx, svc, user, rec.ID), rec
	}
	note := func(target, content string, extra ...string) map[string]string {
		m := map[string]string{"kind": "memory", "scope": "project", "target": target, "content": content, "summary": "Project note"}
		if len(extra) == 2 {
			m[extra[0]] = extra[1]
		}
		return m
	}
	run := func(pc session.Tool, args map[string]string) (string, error) {
		raw, _ := json.Marshal(args)
		return pc.Run(ctx, raw)
	}
	pendingOf := func(owner string) []contract.ChangeProposal {
		GinkgoHelper()
		l, err := svc.Changes().List(ctx, owner, contract.ProposalPending)
		Expect(err).NotTo(HaveOccurred())
		return l
	}
	read := func(owner, rel string) (string, bool) {
		GinkgoHelper()
		text, ok, err := f.ReadHome(ctx, owner, rel, 1<<20)
		Expect(err).NotTo(HaveOccurred())
		return text, ok
	}

	BeforeEach(func() {
		ctx = context.Background()
		saving = map[string]string{}
		dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
		db, err := sqlitedb.Open(dsn, &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		blobs, _ := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		f = files.New(ws)
		st, _ = store.NewJSON("")
		ag, _ := agents.NewJSON("")
		pst, err := projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ps = projects.NewService(projects.Options{Store: pst, Files: f, OwnerName: func(_ context.Context, u string) string { return names[u] }})
		svc, err = New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, Agents: ag, Projects: ps,
			ProposalGate: proposals.GateFunc(func(context.Context, proposals.Request) error { return nil }),
			UserSetting: func(_ context.Context, user, addon, key string) (string, bool, error) {
				mu.Lock()
				defer mu.Unlock()
				v, ok := saving[user+"|"+addon+"|"+key]
				return v, ok, nil
			}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = svc.Close(context.Background())
			if raw, err := db.DB(); err == nil {
				_ = raw.Close()
			}
		})
		Expect(f.EnsureHome(ctx, sOwner)).To(Succeed())
		atlas = mk(sOwner, "Atlas")
	})
	setSave := func(user, mode string) {
		mu.Lock()
		defer mu.Unlock()
		saving[user+"|"+HostSettingsID+"|"+MemorySaveKey] = mode
	}

	Describe("proposing", func() {
		It("is stored for the owner with the grantee as proposer, and writes nothing", func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			t, _ := chatOf(sBob, ref(atlas))
			out, err := run(t, note("decisions.md", "- use sqlite\n", "project", ref(atlas)))
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("Nothing is saved yet"))
			Expect(out).NotTo(ContainSubstring(sOwner), "the grantee never reads the owner's account")
			list := pendingOf(sOwner)
			Expect(list).To(HaveLen(1))
			Expect(list[0].Owner).To(Equal(sOwner))
			Expect(list[0].Target).To(Equal("Projects/" + atlas.Slug + "/memory/decisions.md"))
			Expect(list[0].ProjectNode).To(Equal(atlas.Node))
			Expect(pendingOf(sBob)).To(BeEmpty(), "the grantee has no queue of their own for it")
			_, ok := read(sOwner, "Projects/"+atlas.Slug+"/memory/decisions.md")
			Expect(ok).To(BeFalse())
		})

		It("shows the owner a display name and never the account of the proposer", func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			t, _ := chatOf(sBob, ref(atlas))
			_, err := run(t, note("decisions.md", "- a\n", "project", ref(atlas)))
			Expect(err).NotTo(HaveOccurred())
			list := pendingOf(sOwner)
			Expect(list[0].Proposer).To(Equal("Bobby Grantee"))
			raw, _ := json.Marshal(list)
			Expect(string(raw)).NotTo(ContainSubstring(sBob))
			one, err := svc.Changes().Get(ctx, sOwner, list[0].ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(one.Proposer).To(Equal("Bobby Grantee"))
			raw, _ = json.Marshal(one)
			Expect(string(raw)).NotTo(ContainSubstring(sBob))
		})

		It("tells a grantee why their own content was refused, and nothing of the owner's side", func() {
			c := svc.Changes()
			grantee := contract.ChangeProposal{ID: "p1", Proposer: sBob}
			refusal := fmt.Errorf("%w: the content is 9999 bytes and a memory change may have 100", ErrBadChange)
			Expect(c.neutral(grantee, refusal)).To(MatchError(ContainSubstring("the content is 9999 bytes")))
			// A failure to read the owner's project is worded neutrally, wherever it was
			// raised: the snapshot of the target and the check of the kind alike.
			secret := fmt.Errorf("open /owner/path/Projects/atlas/PROJECT.md: no such file")
			for _, cause := range []error{
				fmt.Errorf("%w: cannot read %s: %w", ErrBadChange, "Projects/atlas/memory/index.md", &proposals.SourceError{Err: secret}),
				fmt.Errorf("%w: %w", ErrBadChange, &proposals.SourceError{Err: secret}),
				secret,
			} {
				err := c.neutral(grantee, cause)
				Expect(err).To(MatchError(ErrBadChange))
				Expect(err.Error()).To(ContainSubstring("the project cannot be read now"))
				Expect(err.Error()).NotTo(ContainSubstring("/owner/path"))
			}
			// The owner's own proposal keeps its error.
			Expect(c.neutral(contract.ChangeProposal{ID: "p2"}, secret)).To(Equal(secret))
		})

		It("is refused for a read grant, in words the model can use", func() {
			grant(sBob, sOwner, atlas, workspace.ModeRead)
			t, _ := chatOf(sBob, ref(atlas))
			_, err := run(t, note("decisions.md", "x\n", "project", ref(atlas)))
			Expect(err).To(MatchError(ContainSubstring("shared read-only")))
			Expect(pendingOf(sOwner)).To(BeEmpty())
		})

		It("is refused for a project the chat did not pull, even when it is shared", func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			t, _ := chatOf(sBob)
			_, err := run(t, note("decisions.md", "x\n", "project", ref(atlas)))
			Expect(err).To(HaveOccurred())
			Expect(pendingOf(sOwner)).To(BeEmpty())
		})

		DescribeTable("is refused for a reference that is not an entry of the pulled list as it is",
			func(vary func(string) string) {
				grant(sBob, sOwner, atlas, workspace.ModeWrite)
				t, _ := chatOf(sBob, ref(atlas))
				_, err := run(t, note("decisions.md", "x\n", "project", vary(ref(atlas))))
				Expect(err).To(HaveOccurred())
				Expect(pendingOf(sOwner)).To(BeEmpty())
			},
			Entry("upper case prefix", func(r string) string { return strings.ToUpper(r) }),
			Entry("a space before", func(r string) string { return " " + r }),
			Entry("a space after", func(r string) string { return r + " " }),
			Entry("a leading zero", func(r string) string { return "s:0" + strings.TrimPrefix(r, "s:") }),
			Entry("a bare node id", func(r string) string { return strings.TrimPrefix(r, "s:") }),
			Entry("a new line", func(r string) string { return r + "\n" }),
			Entry("the manifest id of the project", func(string) string { return atlas.ID }),
			Entry("the slug", func(string) string { return atlas.Slug }),
		)

		It("cannot name an own project: the home project is the only own target", func() {
			own := mk(sBob, "Mine")
			Expect(f.EnsureHome(ctx, sBob)).To(Succeed())
			rec, err := svc.CreateAs(ctx, sBob, CreateInput{Model: "m", ProjectID: own.ID}, Origin{})
			Expect(err).NotTo(HaveOccurred())
			t := toolOf(ctx, svc, sBob, rec.ID)
			other := mk(sBob, "Other")
			Expect(svc.SetChatPulled(ctx, sBob, rec.ID, []string{other.ID})).To(Succeed())
			for _, name := range []string{other.ID, other.Slug, projects.SharedRef(other.Node)} {
				_, err = run(t, note("a.md", "x\n", "project", name))
				Expect(err).To(HaveOccurred(), name)
			}
			_, err = run(t, note("a.md", "x\n"))
			Expect(err).NotTo(HaveOccurred(), "the home project stays the default")
			list := pendingOf(sBob)
			Expect(list).To(HaveLen(1))
			Expect(list[0].Proposer).To(BeEmpty())
			Expect(list[0].Target).To(Equal("Projects/" + own.Slug + "/memory/a.md"))
		})

		It("keeps the home project as the default when a project is shared too", func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			own := mk(sBob, "Mine")
			Expect(f.EnsureHome(ctx, sBob)).To(Succeed())
			rec, err := svc.CreateAs(ctx, sBob, CreateInput{Model: "m", ProjectID: own.ID}, Origin{})
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.SetChatPulled(ctx, sBob, rec.ID, []string{ref(atlas)})).To(Succeed())
			t := toolOf(ctx, svc, sBob, rec.ID)
			_, err = run(t, note("a.md", "x\n"))
			Expect(err).NotTo(HaveOccurred())
			Expect(pendingOf(sBob)).To(HaveLen(1))
			Expect(pendingOf(sOwner)).To(BeEmpty())
		})

		It("is refused for an archived project", func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			t, _ := chatOf(sBob, ref(atlas))
			_, err := ps.Archive(ctx, sOwner, atlas.ID)
			Expect(err).NotTo(HaveOccurred())
			_, err = run(t, note("a.md", "x\n", "project", ref(atlas)))
			Expect(err).To(HaveOccurred())
			Expect(pendingOf(sOwner)).To(BeEmpty())
		})

		It("is refused once the grant was revoked, and never reaches the owner's queue", func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			t, _ := chatOf(sBob, ref(atlas))
			revoke(sBob, sOwner, atlas)
			_, err := run(t, note("a.md", "x\n", "project", ref(atlas)))
			Expect(err).To(HaveOccurred())
			Expect(pendingOf(sOwner)).To(BeEmpty())
		})

		It("is never saved at once, whatever the save mode of the grantee", func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			setSave(sBob, SaveAuto)
			t, _ := chatOf(sBob, ref(atlas))
			out, err := run(t, note("a.md", "x\n", "project", ref(atlas)))
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("needs a confirmation"))
			Expect(pendingOf(sOwner)).To(HaveLen(1))
			_, ok := read(sOwner, "Projects/"+atlas.Slug+"/memory/a.md")
			Expect(ok).To(BeFalse())
		})

		It("follows the gates of the proposing chat, not the owner's settings", func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			// The owner turned notes off: it does not stop a note the owner confirms.
			setSave(sOwner, SaveNever)
			t, _ := chatOf(sBob, ref(atlas))
			_, err := run(t, note("a.md", "x\n", "project", ref(atlas)))
			Expect(err).NotTo(HaveOccurred())
			// The grantee turned notes off: the closed case. Nothing is proposed.
			setSave(sBob, SaveNever)
			_, err = run(t, note("b.md", "x\n", "project", ref(atlas)))
			Expect(err).To(HaveOccurred())
			Expect(pendingOf(sOwner)).To(HaveLen(1))
		})

		It("is not offered, and the schema has no project argument, without a write-shared pulled project", func() {
			grant(sBob, sOwner, atlas, workspace.ModeRead)
			t, _ := chatOf(sBob, ref(atlas))
			Expect(string(t.Schema)).NotTo(ContainSubstring(`"project":{`))
			Expect(string(t.Schema)).NotTo(ContainSubstring(`"project"]`))
		})

		It("offers the project argument for a write-shared pulled project", func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			t, _ := chatOf(sBob, ref(atlas))
			Expect(string(t.Schema)).To(ContainSubstring(`"project":{`))
			Expect(string(t.Schema)).To(ContainSubstring(`"project"`))
			Expect(t.Description).To(ContainSubstring(ref(atlas)))
			Expect(t.Description).NotTo(ContainSubstring(sOwner))
			Expect(t.Description).To(ContainSubstring("Olivia Owner"))
		})

		It("caps the grantee's own proposals without touching the owner's queue", func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			t, _ := chatOf(sBob, ref(atlas))
			for i := range contract.MaxChangesPerHour {
				_, err := run(t, note(fmt.Sprintf("n%d.md", i), "x\n", "project", ref(atlas)))
				Expect(err).NotTo(HaveOccurred())
			}
			_, err := run(t, note("over.md", "x\n", "project", ref(atlas)))
			Expect(err).To(MatchError(ContainSubstring("too many proposals")))
			// The owner proposes in the own project: not blocked.
			rec, _ := svc.CreateAs(ctx, sOwner, CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
			ownT := toolOf(ctx, svc, sOwner, rec.ID)
			_, err = run(ownT, note("mine.md", "x\n"))
			Expect(err).NotTo(HaveOccurred())
		})

		It("caps the proposals waiting for the owner for one project, whoever proposes", func() {
			var ts []session.Tool
			for _, u := range []string{sBob, sCarol, sDave} {
				grant(u, sOwner, atlas, workspace.ModeWrite)
				t, _ := chatOf(u, ref(atlas))
				ts = append(ts, t)
			}
			n := 0
			for i := range contract.MaxPendingPerProject {
				_, err := run(ts[i%3], note(fmt.Sprintf("n%d.md", i), "x\n", "project", ref(atlas)))
				Expect(err).NotTo(HaveOccurred(), "proposal %d", i)
				n++
			}
			_, err := run(ts[0], note("over.md", "x\n", "project", ref(atlas)))
			Expect(err).To(MatchError(ContainSubstring("waiting")))
			Expect(pendingOf(sOwner)).To(HaveLen(n))
			// Another project of the owner and the owner's own notes still work.
			other := mk(sOwner, "Other")
			grant(sErin, sOwner, other, workspace.ModeWrite)
			te, _ := chatOf(sErin, ref(other))
			_, err = run(te, note("a.md", "x\n", "project", ref(other)))
			Expect(err).NotTo(HaveOccurred())
			rec, _ := svc.CreateAs(ctx, sOwner, CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
			_, err = run(toolOf(ctx, svc, sOwner, rec.ID), note("mine.md", "x\n"))
			Expect(err).NotTo(HaveOccurred(), "the owner's own queue is not consumed by grantees")
		})
	})

	Describe("what the owner and the audit show of the proposer", func() {
		const (
			agentMark  = "agent-mark"
			bindMark   = "binding-mark-17"
			threadMark = "thread-mark-23"
			senderMark = "sender-mark-31"
		)
		var chatID, agentID, id string
		propose := func() {
			GinkgoHelper()
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			_, rec := chatOf(sBob, ref(atlas))
			chatID = rec.ID
			a, err := svc.SaveAgent(ctx, sBob, agents.Agent{Name: "Helper " + agentMark, Model: "m", Builtins: []string{"memory.write"}})
			Expect(err).NotTo(HaveOccurred())
			agentID = a.ID
			p, err := svc.Changes().Propose(ctx, ProposeInput{User: sBob, Agent: agentID,
				Source: contract.ChangeSource{Kind: session.OriginConnector, ID: bindMark, Chat: chatID, Thread: threadMark, Sender: senderMark},
				Kind:   "memory", Scope: "project", Target: "decisions.md", Content: "- a\n", Summary: "Note", PulledRef: ref(atlas)})
			Expect(err).NotTo(HaveOccurred())
			id = p.ID
		}
		marks := func() []string { return []string{agentID, chatID, bindMark, threadMark, senderMark, sBob} }
		noMarks := func(v any, what string) {
			GinkgoHelper()
			raw, err := json.Marshal(v)
			Expect(err).NotTo(HaveOccurred())
			for _, m := range marks() {
				Expect(string(raw)).NotTo(ContainSubstring(m), what)
			}
		}
		It("hides the agent, chat, binding, thread and sender of the grantee in every owner-facing output", func() {
			propose()
			list, err := svc.Changes().List(ctx, sOwner)
			Expect(err).NotTo(HaveOccurred())
			noMarks(list, "the list")
			Expect(list[0].Source.Kind).To(Equal(session.OriginConnector), "the kind of origin stays")
			one, err := svc.Changes().Get(ctx, sOwner, id)
			Expect(err).NotTo(HaveOccurred())
			noMarks(one, "get")
			done, err := svc.Changes().Confirm(ctx, sOwner, id)
			Expect(err).NotTo(HaveOccurred())
			noMarks(done, "the confirmed proposal")
			evs, err := svc.Changes().Events(ctx, sOwner, 50)
			Expect(err).NotTo(HaveOccurred())
			Expect(evs).NotTo(BeEmpty())
			noMarks(evs, "the audit of the owner")
			rv, err := svc.Changes().Revert(ctx, sOwner, id)
			Expect(err).NotTo(HaveOccurred())
			noMarks(rv, "the reverted proposal")
			evs, _ = svc.Changes().Events(ctx, sOwner, 50)
			noMarks(evs, "the audit after the revert")
		})
		It("keeps the grantee's own fields in their own view", func() {
			propose()
			got, err := svc.Changes().load(ctx, sOwner, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Source.Chat).To(Equal(chatID))
			Expect(got.Agent).To(Equal(agentID))
		})
		It("audits a refusal for a cap under the proposer, never under the owner", func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			t, _ := chatOf(sBob, ref(atlas))
			for i := range contract.MaxChangesPerHour {
				_, err := run(t, note(fmt.Sprintf("n%d.md", i), "x\n", "project", ref(atlas)))
				Expect(err).NotTo(HaveOccurred())
			}
			_, err := run(t, note("over.md", "x\n", "project", ref(atlas)))
			Expect(err).To(HaveOccurred())
			refused := func(user string) int {
				evs, _ := svc.Changes().Events(ctx, user, 100)
				n := 0
				for _, e := range evs {
					if e.Action == proposals.ActionRefuse {
						n++
					}
				}
				return n
			}
			Expect(refused(sOwner)).To(BeZero(), "a grantee cannot fill the owner's audit")
			Expect(refused(sBob)).To(Equal(1))
		})
		It("is not undone by the owner's revert of the last change, only by its id", func() {
			propose()
			_, err := svc.Changes().Confirm(ctx, sOwner, id)
			Expect(err).NotTo(HaveOccurred())
			_, err = svc.Changes().RevertLast(ctx, sOwner, "")
			Expect(err).To(MatchError(ErrNothingToRevert))
			_, err = svc.Changes().RevertLast(ctx, sOwner, agentID)
			Expect(err).To(MatchError(ErrNothingToRevert), "an agent id of the grantee that an owner agent shares matches nothing")
			rv, err := svc.Changes().Revert(ctx, sOwner, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(rv.Status).To(Equal(contract.ProposalReverted))
		})
	})

	Describe("the words of the tool", func() {
		It("does not call a shared project the project of this chat, when the chat has none", func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			t, _ := chatOf(sBob, ref(atlas))
			Expect(t.Description).NotTo(ContainSubstring("the project this chat belongs to"))
			Expect(t.Description).NotTo(ContainSubstring("give no project name"))
			Expect(t.Description).To(ContainSubstring("shares with you"))
			Expect(t.Description).To(ContainSubstring("target is required"))
		})
		It("tells a chat with a home project that project names a shared one", func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			own := mk(sBob, "Mine")
			Expect(f.EnsureHome(ctx, sBob)).To(Succeed())
			rec, err := svc.CreateAs(ctx, sBob, CreateInput{Model: "m", ProjectID: own.ID}, Origin{})
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.SetChatPulled(ctx, sBob, rec.ID, []string{ref(atlas)})).To(Succeed())
			t := toolOf(ctx, svc, sBob, rec.ID)
			Expect(t.Description).NotTo(ContainSubstring("give no project name"))
			Expect(t.Description).To(ContainSubstring("the project this chat belongs to"))
			Expect(t.Description).To(ContainSubstring(ref(atlas)))
		})
		It("tells a grantee who proposes from a messaging chat that the owner answers, with no prompt in that chat", func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			cs, err := connectors.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			c, err := NewConnectors(svc, ConnectorOptions{Store: cs, Transport: titleTransport{}})
			Expect(err).NotTo(HaveOccurred())
			svc.SetConnectors(c)
			Expect(cs.PutBinding(ctx, connectors.Binding{ID: "b-tg", Owner: sBob, Kind: "tg", AddonID: "tg-addon"})).To(Succeed())
			Expect(f.EnsureHome(ctx, sBob)).To(Succeed())
			rec, err := svc.CreateAs(ctx, sBob, CreateInput{Model: "m"}, Origin{Kind: session.OriginConnector, ID: "b-tg"})
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.SetChatPulled(ctx, sBob, rec.ID, []string{ref(atlas)})).To(Succeed())
			svc.access.set(rec.ID, turnGrant{Binding: "b-tg", Kind: "tg", Senders: []string{"s1"}, Direct: true, Paired: true,
				Caps: []string{contract.CapMemoryWrite, contract.CapProposalsConfirm}})
			t := toolOf(ctx, svc, sBob, rec.ID)
			out, err := run(t, note("a.md", "x\n", "project", ref(atlas)))
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("owner of the project"))
			Expect(out).NotTo(ContainSubstring("wrote the proposal in this chat"))
			Expect(out).NotTo(ContainSubstring("yes or no"))
			Expect(pendingOf(sOwner)).To(HaveLen(1))
		})
	})

	Describe("answering", func() {
		var id string
		BeforeEach(func() {
			grant(sBob, sOwner, atlas, workspace.ModeWrite)
			t, _ := chatOf(sBob, ref(atlas))
			_, err := run(t, note("decisions.md", "- use sqlite\n", "project", ref(atlas)))
			Expect(err).NotTo(HaveOccurred())
			id = pendingOf(sOwner)[0].ID
		})
		path := func() string { return "Projects/" + atlas.Slug + "/memory/decisions.md" }

		It("writes as the owner in one change set, which the owner can revert", func() {
			p, err := svc.Changes().Confirm(ctx, sOwner, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Status).To(Equal(contract.ProposalApplied))
			Expect(p.Rev).NotTo(BeZero())
			Expect(p.Proposer).To(Equal("Bobby Grantee"))
			text, ok := read(sOwner, path())
			Expect(ok).To(BeTrue())
			Expect(text).To(Equal("- use sqlite\n"))
			sets, err := f.Store().ChangesSince(ctx, f.Actor(sOwner), p.Rev-1, 50)
			Expect(err).NotTo(HaveOccurred())
			var cs workspace.ChangeSet
			for _, c := range sets {
				if c.Rev == p.Rev {
					cs = c
				}
			}
			Expect(cs.Rev).To(Equal(p.Rev))
			Expect(cs.Title).To(ContainSubstring("Bobby Grantee"))
			Expect(cs.Title).NotTo(ContainSubstring(sBob))
			Expect(cs.Actor).To(Equal(sOwner), "the write is the owner's own")
			rv, err := svc.Changes().Revert(ctx, sOwner, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(rv.Status).To(Equal(contract.ProposalReverted))
			_, ok = read(sOwner, path())
			Expect(ok).To(BeFalse())
		})

		It("is not undone by revert_change of a plain chat of the owner, whatever its id", func() {
			_, err := svc.Changes().Confirm(ctx, sOwner, id)
			Expect(err).NotTo(HaveOccurred())
			_, orec := chatOf(sOwner)
			revert := toolNamed(ctx, svc, sOwner, orec.ID, "revert_change")
			Expect(revert.Name).To(Equal("revert_change"))
			raw, _ := json.Marshal(map[string]string{"id": id})
			_, err = revert.Run(ctx, raw)
			Expect(err).To(MatchError(ContainSubstring("belongs to another")))
			_, ok := read(sOwner, path())
			Expect(ok).To(BeTrue(), "the note is still written")
			got, err := svc.Changes().Get(ctx, sOwner, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(contract.ProposalApplied))
		})

		It("lets the owner edit the text and dismiss", func() {
			p, err := svc.Changes().ConfirmEdited(ctx, sOwner, id, "- edited\n")
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Status).To(Equal(contract.ProposalApplied))
			text, _ := read(sOwner, path())
			Expect(text).To(Equal("- edited\n"))
		})

		It("lets the owner dismiss it", func() {
			p, err := svc.Changes().Dismiss(ctx, sOwner, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Status).To(Equal(contract.ProposalDismissed))
			_, ok := read(sOwner, path())
			Expect(ok).To(BeFalse())
		})

		It("is not found for the proposer, who cannot confirm, edit, dismiss or revert it", func() {
			_, err := svc.Changes().Confirm(ctx, sBob, id)
			Expect(err).To(MatchError(proposals.ErrNotFound))
			_, err = svc.Changes().ConfirmEdited(ctx, sBob, id, "- mine\n")
			Expect(err).To(MatchError(proposals.ErrNotFound))
			_, err = svc.Changes().Dismiss(ctx, sBob, id)
			Expect(err).To(MatchError(proposals.ErrNotFound))
			_, err = svc.Changes().Revert(ctx, sBob, id)
			Expect(err).To(MatchError(proposals.ErrNotFound))
			_, err = svc.Changes().Get(ctx, sBob, id)
			Expect(err).To(MatchError(proposals.ErrNotFound))
			Expect(pendingOf(sOwner)).To(HaveLen(1))
			_, ok := read(sOwner, path())
			Expect(ok).To(BeFalse())
		})

		voided := func() {
			GinkgoHelper()
			_, err := svc.Changes().Confirm(ctx, sOwner, id)
			Expect(err).To(MatchError(ErrProposalVoid))
			Expect(err.Error()).NotTo(ContainSubstring(sBob))
			Expect(err.Error()).To(ContainSubstring("no longer"))
			p, err := svc.Changes().Get(ctx, sOwner, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Status).To(Equal(contract.ProposalStale), "a voided proposal is settled as stale")
			_, ok := read(sOwner, path())
			Expect(ok).To(BeFalse(), "nothing was written")
			_, err = svc.Changes().Confirm(ctx, sOwner, id)
			Expect(err).To(HaveOccurred())
			got, _ := svc.Changes().Get(ctx, sOwner, id)
			Expect(got.Status).To(Equal(contract.ProposalStale))
		}
		It("is voided when the grant is revoked before the owner confirms", func() {
			revoke(sBob, sOwner, atlas)
			voided()
		})
		It("is voided when the grant is lowered to read", func() {
			revoke(sBob, sOwner, atlas)
			grant(sBob, sOwner, atlas, workspace.ModeRead)
			voided()
		})
		It("is voided when the project is deleted", func() {
			Expect(ps.Delete(ctx, sOwner, atlas.ID)).To(Succeed())
			voided()
		})
		It("is voided when the project is archived", func() {
			_, err := ps.Archive(ctx, sOwner, atlas.ID)
			Expect(err).NotTo(HaveOccurred())
			voided()
		})
		It("is voided when the project is moved out of Projects", func() {
			from, _ := f.Abs(sOwner, "Projects/"+atlas.Slug)
			to, _ := f.Abs(sOwner, "Elsewhere/"+atlas.Slug)
			_, err := f.Store().Move(ctx, f.Actor(sOwner), from, to)
			Expect(err).NotTo(HaveOccurred())
			voided()
		})
		It("is voided when the project folder is renamed", func() {
			from, _ := f.Abs(sOwner, "Projects/"+atlas.Slug)
			to, _ := f.Abs(sOwner, "Projects/renamed")
			_, err := f.Store().Move(ctx, f.Actor(sOwner), from, to)
			Expect(err).NotTo(HaveOccurred())
			voided()
		})
		It("is voided when another project replaced the folder", func() {
			Expect(ps.Delete(ctx, sOwner, atlas.ID)).To(Succeed())
			again := mk(sOwner, "Atlas")
			_ = again
			// Whatever the store does with node ids, the grant is gone with the folder.
			voided()
		})
	})
})

// toolOf returns the propose_change tool a chat of user has now, as the model
// would get it at the start of a turn.
func toolOf(ctx context.Context, svc *Service, user, chatID string) session.Tool {
	GinkgoHelper()
	return toolNamed(ctx, svc, user, chatID, "propose_change")
}

// toolNamed returns the tool of that name a chat of user has now.
func toolNamed(ctx context.Context, svc *Service, user, chatID, name string) session.Tool {
	GinkgoHelper()
	x, err := svc.ChatAccess(ctx, user, chatID)
	Expect(err).NotTo(HaveOccurred())
	tt, err := svc.chatTools(ctx, x)
	Expect(err).NotTo(HaveOccurred())
	for _, t := range tt.Tools {
		if t.Name == name {
			return t
		}
	}
	// A chat without the tool: a tool that refuses, so a spec reads the refusal.
	return session.Tool{Name: name, Run: func(context.Context, json.RawMessage) (string, error) {
		return "", fmt.Errorf("%s is not offered to this chat", name)
	}}
}
