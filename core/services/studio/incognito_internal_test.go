package studio

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/privacy"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

var _ = Describe("incognito chats", func() {
	var (
		ctx  context.Context
		svc  *Service
		st   store.Store
		f    *files.Service
		home = session.HomePath("alice")
	)

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
		st, _ = store.NewJSON("")
		ag, _ := agents.NewJSON("")
		svc, err = New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, Agents: ag})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = svc.Close(context.Background())
			if raw, err := db.DB(); err == nil {
				_ = raw.Close()
			}
		})
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		_, err = f.Store().Put(ctx, f.Actor("alice"), home+"/Memory/index.md", strings.NewReader("- home: lives in Rome -> home.md\n"), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	})

	create := func(level privacy.Level) session.ChatRecord {
		GinkgoHelper()
		rec, err := svc.CreateAs(privacy.WithLevel(ctx, level), "alice", CreateInput{Model: "m"}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		return rec
	}
	tools := func(rec session.ChatRecord) session.TurnTools {
		GinkgoHelper()
		x, err := svc.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		tt, err := svc.chatTools(ctx, x)
		Expect(err).NotTo(HaveOccurred())
		return tt
	}
	names := func(tt session.TurnTools) []string {
		var out []string
		for _, t := range tt.Tools {
			out = append(out, t.Name)
		}
		return out
	}

	It("reads the notes in a normal chat, the control of the cases below", func() {
		tt := tools(create(privacy.Normal))
		Expect(tt.Prompt).To(ContainSubstring("lives in Rome"))
		Expect(names(tt)).To(ContainElement(spawnAgentName))
	})

	DescribeTable("detaches memory",
		func(level privacy.Level) {
			rec := create(level)
			tt := tools(rec)
			Expect(tt.Prompt).NotTo(ContainSubstring("lives in Rome"))
			Expect(tt.Prompt + tt.Note).To(ContainSubstring("because this chat is incognito"))
			Expect(svc.ChatMemory(ctx, rec).Code).To(Equal(MemoryOffIncognito))
			Expect(svc.knowledgeOffered(ctx, svc.chatFor(ctx, rec))).To(BeFalse())
		},
		Entry("memory-off", privacy.MemoryOff),
		Entry("zero-retention", privacy.ZeroRetention),
	)

	It("gives a zero-retention chat only the tools that keep nothing", func() {
		tt := tools(create(privacy.ZeroRetention))
		for _, n := range names(tt) {
			Expect(zeroRetentionTools).To(ContainElement(n))
		}
		Expect(names(tt)).NotTo(ContainElement(spawnAgentName))
		Expect(tt.Note).To(ContainSubstring("zero retention"))
		Expect(tt.WorkFolder).To(BeFalse())
	})

	It("keeps the tools of a memory-off chat, but memory", func() {
		tt := tools(create(privacy.MemoryOff))
		Expect(names(tt)).To(ContainElement(spawnAgentName))
		Expect(tt.Note).NotTo(ContainSubstring("zero retention"))
	})

	It("keeps an incognito chat out of the server's store and out of the list", func() {
		normal := create(privacy.Normal)
		rec := create(privacy.ZeroRetention)
		_, err := st.GetChat(ctx, "alice", rec.ID)
		Expect(errors.Is(err, session.ErrNotFound)).To(BeTrue(), "the server's store never sees the chat")
		_, err = st.GetChat(ctx, "alice", normal.ID)
		Expect(err).NotTo(HaveOccurred())
		list, err := svc.List(ctx, "alice", 50)
		Expect(err).NotTo(HaveOccurred())
		ids := []string{}
		for _, r := range list {
			ids = append(ids, r.ID)
		}
		Expect(ids).To(ContainElement(normal.ID))
		Expect(ids).NotTo(ContainElement(rec.ID))
		got, err := svc.Get(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID).To(Equal(rec.ID))
		Expect(svc.ChatPrivacy(rec.ID)).To(Equal(privacy.ZeroRetention))
		Expect(svc.ChatPrivacy(normal.ID)).To(Equal(privacy.Normal))
	})

	It("refuses what would keep a copy of an incognito chat", func() {
		rec := create(privacy.ZeroRetention)
		_, err := svc.Fork(ctx, "alice", rec.ID, 0, "")
		Expect(errors.Is(err, ErrIncognito)).To(BeTrue())
		Expect(errors.Is(svc.SetChatProject(ctx, "alice", rec.ID, "p1"), ErrIncognito)).To(BeTrue())
		_, err = svc.SendWithFiles(ctx, "alice", rec.ID, "hi", []session.Upload{{Name: "a.txt", Body: strings.NewReader("secret")}})
		Expect(errors.Is(err, ErrIncognito)).To(BeTrue())
		_, err = svc.CreateAs(privacy.WithLevel(ctx, privacy.MemoryOff), "alice", CreateInput{Model: "m"}, Origin{Kind: session.OriginTrigger, ID: "t"})
		Expect(errors.Is(err, ErrIncognito)).To(BeTrue())
	})

	It("forgets a discarded chat and an idle one", func() {
		rec := create(privacy.MemoryOff)
		Expect(svc.Delete(ctx, "alice", rec.ID)).To(Succeed())
		Expect(svc.ChatPrivacy(rec.ID)).To(Equal(privacy.Normal))
		_, err := svc.Get(ctx, "alice", rec.ID)
		Expect(errors.Is(err, session.ErrNotFound)).To(BeTrue())

		idle := create(privacy.ZeroRetention)
		svc.discardIncognito(time.Now().Add(-time.Minute))
		Expect(svc.ChatPrivacy(idle.ID)).To(Equal(privacy.ZeroRetention), "a chat used a moment ago stays")
		svc.discardIncognito(time.Now().Add(time.Minute))
		Expect(svc.ChatPrivacy(idle.ID)).To(Equal(privacy.Normal))
		_, err = svc.Get(ctx, "alice", idle.ID)
		Expect(errors.Is(err, session.ErrNotFound)).To(BeTrue())
	})
})
