package session_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/workfiles"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// workStores are the two workspace stores, each with alice's home.
var workStores = map[string]func() workspace.Store{
	"json": func() workspace.Store {
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err := jsonstore.New(filepath.Join(GinkgoT().TempDir(), "ws"), blobs, jsonstore.Options{Admin: filesAdmin})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = ws.Close() })
		return ws
	},
	"gorm": func() workspace.Store {
		dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
		db, err := sqlitedb.Open(dsn, &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			if raw, err := db.DB(); err == nil {
				_ = raw.Close()
			}
		})
		blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		Expect(err).NotTo(HaveOccurred())
		ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: filesAdmin})
		Expect(err).NotTo(HaveOccurred())
		return ws
	},
}

func withHome(ws workspace.Store, p string) workspace.Store {
	admin := workspace.Actor{Principal: filesAdmin}
	Expect(ws.Mkdir(context.Background(), admin, session.ChatsPath(p))).Error().NotTo(HaveOccurred())
	Expect(ws.Grant(context.Background(), admin, p, session.HomePath(p), workspace.ModeWrite)).To(Succeed())
	return ws
}

// workRig is a manager whose ChatTools asks for the work folder (work) and
// whose harness records the prompt block of each start.
type workRig struct {
	m     *session.Manager
	ws    workspace.Store
	store session.Store
	h     *sessiontest.FakeHarness
	work  bool
}

func newWorkRig(ws workspace.Store) *workRig {
	r := &workRig{ws: ws, store: sessiontest.NewMemoryStore(), work: true}
	turns := make([][]sessiontest.Step, 20)
	for i := range turns {
		turns[i] = []sessiontest.Step{{Say: "ok"}}
	}
	r.h = &sessiontest.FakeHarness{Turns: turns}
	o := session.Options{Store: r.store, Workspace: ws, Harness: r.h, StateDir: GinkgoT().TempDir()}
	o.ChatTools = func(context.Context, workspace.Actor) (session.TurnTools, error) {
		return session.TurnTools{Prompt: "Base block.", WorkFolder: r.work}, nil
	}
	m, err := session.New(o)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = m.Close(context.Background()) })
	r.m = m
	return r
}

func (r *workRig) open(p, id string) *session.Chat {
	c, err := r.m.Open(context.Background(), workspace.Actor{Principal: p, ChatID: id}, id)
	Expect(err).NotTo(HaveOccurred())
	return c
}

// lastPrompt is the prompt block of the last harness start.
func (r *workRig) lastPrompt() string {
	starts := r.h.Starts()
	Expect(starts).NotTo(BeEmpty())
	return starts[len(starts)-1].TurnPrompt
}

var _ = Describe("the work folder of a chat", func() {
	for name, mk := range workStores {
		Context("on the "+name+" workspace store", func() {
			It("names the work folder in the prompt and makes it only when it is used", func() {
				ctx := context.Background()
				r := newWorkRig(withHome(mk(), "alice"))
				c := r.open("alice", "c1")
				turn(c, session.Message{Text: "Budget review"})

				rec, err := r.store.GetChat(ctx, "alice", "c1")
				Expect(err).NotTo(HaveOccurred())
				Expect(rec.Folder).NotTo(BeEmpty(), "the chat took no folder name")
				guest := workfiles.GuestFolder(rec.Folder)
				Expect(r.lastPrompt()).To(HavePrefix("Base block."))
				Expect(r.lastPrompt()).To(ContainSubstring(guest))

				// Nothing is made until something is saved there.
				user := workspace.Actor{Principal: "alice"}
				_, err = r.ws.Stat(ctx, user, session.ChatsPath("alice")+"/"+rec.Folder)
				Expect(err).To(MatchError(workspace.ErrNotFound))
				cf, err := r.m.Files(ctx, "alice", "c1")
				Expect(err).NotTo(HaveOccurred())
				Expect(cf.Folder).To(BeEmpty(), "the file list names a folder that is not there")

				// EnsureWorkFolder makes it once; a second call changes nothing.
				work := session.HomePath("alice") + workfiles.Folder(rec.Folder)
				Expect(session.EnsureWorkFolder(ctx, r.ws, user, "alice", rec.Folder)).To(Succeed())
				n, err := r.ws.Stat(ctx, user, work)
				Expect(err).NotTo(HaveOccurred())
				Expect(n.Kind).To(Equal(workspace.KindDir))
				before, err := r.ws.History(ctx, user, workspace.HistoryQuery{Path: session.HomePath("alice"), Limit: 100})
				Expect(err).NotTo(HaveOccurred())
				Expect(session.EnsureWorkFolder(ctx, r.ws, user, "alice", rec.Folder)).To(Succeed())
				after, err := r.ws.History(ctx, user, workspace.HistoryQuery{Path: session.HomePath("alice"), Limit: 100})
				Expect(err).NotTo(HaveOccurred())
				Expect(after).To(HaveLen(len(before)), "the second call made a change")
				Expect(session.EnsureWorkFolder(ctx, r.ws, user, "alice", "")).To(Succeed())

				// An upload uses the same folder.
				Expect(c.Send(ctx, session.Message{Text: "here", Files: upload("q1.csv")})).To(Succeed())
				Eventually(func() error {
					_, err := r.ws.Stat(ctx, user, session.ChatsPath("alice")+"/"+rec.Folder+"/q1.csv")
					return err
				}, "10s").Should(Succeed())
			})

			It("gives two chats with the same title two folders before either is made", func() {
				ctx := context.Background()
				r := newWorkRig(withHome(mk(), "alice"))
				turn(r.open("alice", "c1"), session.Message{Text: "Same title"})
				turn(r.open("alice", "c2"), session.Message{Text: "Same title"})
				a, err := r.store.GetChat(ctx, "alice", "c1")
				Expect(err).NotTo(HaveOccurred())
				b, err := r.store.GetChat(ctx, "alice", "c2")
				Expect(err).NotTo(HaveOccurred())
				Expect(a.Folder).NotTo(BeEmpty())
				Expect(b.Folder).NotTo(BeEmpty())
				Expect(strings.EqualFold(a.Folder, b.Folder)).To(BeFalse(), "both chats hold %q", a.Folder)
			})

			It("renames a folder name that has no folder yet", func() {
				ctx := context.Background()
				r := newWorkRig(withHome(mk(), "alice"))
				c := r.open("alice", "c1")
				turn(c, session.Message{Text: "First title"})
				Expect(c.SetTitle(ctx, "Second title")).To(Succeed())
				rec, err := r.store.GetChat(ctx, "alice", "c1")
				Expect(err).NotTo(HaveOccurred())
				Expect(rec.Folder).To(HaveSuffix("Second title"))
				turn(c, session.Message{Text: "again"})
				Expect(r.lastPrompt()).To(ContainSubstring(workfiles.GuestFolder(rec.Folder)))
			})
		})
	}

	It("names no work folder when the product does not ask", func() {
		ctx := context.Background()
		r := newWorkRig(withHome(workStores["json"](), "alice"))
		r.work = false
		turn(r.open("alice", "c1"), session.Message{Text: "hello"})
		rec, err := r.store.GetChat(ctx, "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Folder).To(BeEmpty())
		for _, s := range r.h.Starts() {
			Expect(s.TurnPrompt).NotTo(ContainSubstring("work folder"))
		}
	})

	It("marks the working files of the chat's file list", func() {
		ctx := context.Background()
		r := newWorkRig(withHome(workStores["json"](), "alice"))
		c := r.open("alice", "c1")
		turn(c, session.Message{Text: "Report"})
		rec, err := r.store.GetChat(ctx, "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		agent := workspace.Actor{Principal: "alice", ChatID: "c1"}
		home := session.HomePath("alice")
		put(r.ws, agent, home+"/report.docx", "doc")
		put(r.ws, agent, home+"/scripts/build_document.py", "print(1)")
		put(r.ws, agent, home+workfiles.Folder(rec.Folder)+"/rows.json", "[]")
		put(r.ws, agent, home+"/pages/tracker/app.js", "x")
		Expect(c.Send(ctx, session.Message{Text: "mine", Files: upload("notes.py")})).To(Succeed())
		Eventually(func() int {
			cf, err := r.m.Files(ctx, "alice", "c1")
			Expect(err).NotTo(HaveOccurred())
			return len(cf.Files)
		}, "10s").Should(Equal(5))
		cf, err := r.m.Files(ctx, "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		kinds := map[string]string{}
		for _, f := range cf.Files {
			kinds[f.Name] = f.Kind
		}
		Expect(kinds).To(Equal(map[string]string{
			"report.docx": workfiles.Deliverable, "build_document.py": workfiles.Working, "rows.json": workfiles.Working,
			"app.js": workfiles.Deliverable, "notes.py": workfiles.Deliverable, // attached by the user
		}))
	})

	It("puts the kind of each file on the card of a call", func() {
		ws := withHome(workStores["json"](), "alice")
		var calls int
		turns := [][]sessiontest.Step{{{Tool: "save", Args: `{}`}, {Tool: "write", Args: `{}`}, {Say: "done"}}}
		h := &sessiontest.FakeHarness{Turns: turns}
		st := sessiontest.NewMemoryStore()
		o := session.Options{Store: st, Workspace: ws, Harness: h, StateDir: GinkgoT().TempDir()}
		run := func(card session.ToolCard, files []session.ToolFile) func(context.Context, json.RawMessage) (string, error) {
			return func(ctx context.Context, _ json.RawMessage) (string, error) {
				calls++
				session.SetToolCard(ctx, card)
				if files != nil {
					session.SetToolFiles(ctx, files)
				}
				return "ok", nil
			}
		}
		o.ChatTools = func(context.Context, workspace.Actor) (session.TurnTools, error) {
			return session.TurnTools{Tools: []session.Tool{
				{Name: "save", Description: "x", Schema: []byte(`{"type":"object"}`), Run: run(session.ToolCard{Kind: "tool_provider", ID: "sandbox"},
					[]session.ToolFile{{Path: "/summary.docx", Rev: 4, Op: session.ToolFileCreated}, {Path: "/scripts/build.py", Rev: 4, Op: session.ToolFileCreated}})},
				{Name: "write", Description: "x", Schema: []byte(`{"type":"object"}`), Run: run(session.ToolCard{Kind: session.ChangeCardKind, ID: "5:/Chats/x/work/a.py"}, nil)},
			}}, nil
		}
		m, err := session.New(o)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close(context.Background()) })
		c, err := m.Open(context.Background(), workspace.Actor{Principal: "alice", ChatID: "c9"}, "c9")
		Expect(err).NotTo(HaveOccurred())
		turn(c, session.Message{Text: "go"})
		Expect(calls).To(Equal(2))
		entries, err := st.Entries(context.Background(), "c9", 0, 100)
		Expect(err).NotTo(HaveOccurred())
		var cards []*session.ToolCard
		for _, e := range entries {
			if e.Role == session.RoleTool && e.Card != nil {
				cards = append(cards, e.Card)
			}
		}
		Expect(cards).To(HaveLen(2))
		Expect(cards[0].Files).To(HaveLen(2))
		Expect(cards[0].Files[0].Kind).To(Equal(workfiles.Deliverable))
		Expect(cards[0].Files[1].Kind).To(Equal(workfiles.Working))
		Expect(cards[1].FileKind).To(Equal(workfiles.Working))
	})
})
