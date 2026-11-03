package studio

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

type saver struct{}

func (saver) SaveTool() string { return "propose_change" }

var _ = Describe("context files in the prompt of a chat", func() {
	var (
		ctx context.Context
		svc *Service
		f   *files.Service
		cs  connectors.Store
	)
	home := session.HomePath("alice")
	put := func(rel, text string) {
		GinkgoHelper()
		_, err := f.Store().Put(ctx, f.Actor("alice"), home+"/"+rel, strings.NewReader(text), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	register := func() {
		reg := svc.ContextSources()
		Expect(reg.Register(contextfiles.Source{ID: "notes-user", Scope: contextfiles.ScopeUser, Path: "Memory/index.md"})).To(Succeed())
		Expect(reg.Register(contextfiles.Source{ID: "notes-agent", Scope: contextfiles.ScopeAgent, Path: "Memory/agents/{agent}/index.md"})).To(Succeed())
		Expect(reg.Register(contextfiles.Source{ID: "notes-channel", Scope: contextfiles.ScopeChannel, Path: "Memory/channels/{channel}/index.md"})).To(Succeed())
	}
	bind := func(id, kind string) {
		GinkgoHelper()
		Expect(cs.PutBinding(ctx, connectors.Binding{ID: id, Owner: "alice", Kind: kind, AddonID: kind + "-addon"})).To(Succeed())
	}
	turn := func(in CreateInput, origin Origin) (session.TurnTools, session.Access) {
		GinkgoHelper()
		rec, err := svc.CreateAs(ctx, "alice", in, origin)
		Expect(err).NotTo(HaveOccurred())
		x, err := svc.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		tt, err := svc.chatTools(ctx, x)
		Expect(err).NotTo(HaveOccurred())
		return tt, x
	}
	tool := func(tt session.TurnTools, name string) *session.Tool {
		for i := range tt.Tools {
			if tt.Tools[i].Name == name {
				return &tt.Tools[i]
			}
		}
		return nil
	}
	read := func(tt session.TurnTools, args string) (string, error) {
		GinkgoHelper()
		mr := tool(tt, contextfiles.ToolName)
		Expect(mr).NotTo(BeNil(), "the chat has memory_read")
		return mr.Run(ctx, json.RawMessage(args))
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
		svc, err = New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, Agents: ag})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = svc.Close(context.Background())
			if raw, err := db.DB(); err == nil {
				_ = raw.Close()
			}
		})
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		cs, err = connectors.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		c, err := NewConnectors(svc, ConnectorOptions{Store: cs, Transport: titleTransport{}})
		Expect(err).NotTo(HaveOccurred())
		svc.SetConnectors(c)
	})

	It("adds nothing and no tool while no source is registered", func() {
		put("Memory/index.md", "- home: Rome\n")
		tt, _ := turn(CreateInput{Model: "m"}, Origin{})
		Expect(tt.Prompt).NotTo(ContainSubstring(contextfiles.Header))
		Expect(tool(tt, contextfiles.ToolName)).To(BeNil())
	})

	Describe("a plain chat", func() {
		BeforeEach(register)

		It("lists the files of every source with sizes, the agent and channel folders, and the profiles", func() {
			put("Memory/index.md", "- home: Rome -> home.md\n\n- job: dev\n")
			put("Memory/home.md", "Rome.")
			put("Memory/agents/bot/index.md", "- tone: short\n")
			put("Memory/channels/room-1234abcd/index.md", "- team: blue\n")
			put("Memory/channels/room-1234abcd/notes.txt", "not markdown")
			put(contextfiles.ProfilePath("bot"), "Be brief.")
			put("LocalAI/agents/bot/other.md", "not context")
			out, err := svc.ListContextFiles(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Limits).To(Equal(ContextLimits{IndexLines: 40, IndexBytes: 2048, ProfileBytes: 4096, TurnBytes: 6144}))
			names := func(g ContextGroup) []string {
				var n []string
				for _, f := range g.Files {
					n = append(n, f.Path)
				}
				return n
			}
			byKey := map[string]ContextGroup{}
			for _, g := range out.Groups {
				byKey[g.Source+"/"+g.Key] = g
			}
			Expect(byKey).To(HaveLen(4))
			user := byKey["notes-user/"]
			Expect(names(user)).To(Equal([]string{"Memory/index.md", "Memory/home.md"}))
			Expect(user.Files[0].Index).To(BeTrue())
			Expect(user.Files[0].Lines).To(Equal(2))
			Expect(user.MaxBytes).To(Equal(2048))
			Expect(names(byKey["notes-agent/bot"])).To(Equal([]string{"Memory/agents/bot/index.md"}))
			Expect(names(byKey["notes-channel/room-1234abcd"])).To(Equal([]string{"Memory/channels/room-1234abcd/index.md"}))
			profile := byKey["profile/bot"]
			Expect(names(profile)).To(Equal([]string{"LocalAI/agents/bot/profile.md"}))
			Expect(profile.MaxBytes).To(Equal(4096))
		})

		It("lists nothing for another user", func() {
			put("Memory/index.md", "- home: Rome\n")
			Expect(f.EnsureHome(ctx, "bob")).To(Succeed())
			out, err := svc.ListContextFiles(ctx, "bob")
			Expect(err).NotTo(HaveOccurred())
			Expect(out.Groups).To(BeEmpty())
		})

		It("loads the user index under the header and offers memory_read", func() {
			put("Memory/index.md", "- home: lives in Rome -> home.md\n")
			put("Memory/home.md", "Rome, since 2020.")
			tt, _ := turn(CreateInput{Model: "m"}, Origin{})
			Expect(tt.Prompt).To(ContainSubstring(contextfiles.Header))
			Expect(tt.Prompt).To(ContainSubstring("lives in Rome"))
			out, err := read(tt, `{"topic":"home"}`)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("Rome, since 2020."))
			Expect(tool(tt, contextfiles.ToolName).Description).To(Equal(contextfiles.ToolDescription))
		})

		It("keeps the prompt and the key the same from one turn to the next, and the note free of it", func() {
			put("Memory/index.md", "- home: lives in Rome\n")
			rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			x, _ := svc.ChatAccess(ctx, "alice", rec.ID)
			first, err := svc.chatTools(ctx, x)
			Expect(err).NotTo(HaveOccurred())
			second, err := svc.chatTools(ctx, x)
			Expect(err).NotTo(HaveOccurred())
			Expect(second.Prompt).To(Equal(first.Prompt))
			Expect(second.RefreshKey).To(Equal(first.RefreshKey))
			Expect(first.Note).NotTo(ContainSubstring("Rome"))
			Expect(strings.Count(first.Prompt, contextfiles.Header)).To(Equal(1))
			put("Memory/index.md", "- home: lives in Milan\n")
			third, err := svc.chatTools(ctx, x)
			Expect(err).NotTo(HaveOccurred())
			Expect(third.RefreshKey).NotTo(Equal(first.RefreshKey), "a changed index restarts the harness")
		})

		It("tells the model how to save once a writer is plugged in", func() {
			svc.SetContextWriter(saver{})
			tt, _ := turn(CreateInput{Model: "m"}, Origin{})
			Expect(tt.Prompt).To(ContainSubstring("Save new facts with propose_change."))
		})

		It("adds a bounded amount even when every file is at its cap", func() {
			base, _ := turn(CreateInput{Model: "m"}, Origin{})
			big := strings.Repeat("- t: "+strings.Repeat("w", 90)+"\n", 100)
			put("Memory/index.md", big)
			put("Memory/agents/x/index.md", big)
			tt, _ := turn(CreateInput{Model: "m"}, Origin{})
			Expect(len(tt.Prompt) - len(base.Prompt)).To(BeNumerically("<=", contextfiles.MaxTotalBytes+1024))
		})
	})

	Describe("the same loader for chats of any origin", func() {
		BeforeEach(func() {
			register()
			put("Memory/index.md", "- home: lives in Rome\n")
			bind("b-slack", "slack")
			bind("b-tg", "telegram")
			put("Memory/channels/"+contextfiles.ChannelKey("b-slack")+"/index.md", "- team: SLACKNOTE\n")
			put("Memory/channels/"+contextfiles.ChannelKey("b-tg")+"/index.md", "- team: TGNOTE\n")
			put("Memory/channels/"+contextfiles.ChannelKey("b-slack")+"/team.md", "slack detail")
			put("Memory/channels/"+contextfiles.ChannelKey("b-tg")+"/team.md", "telegram detail")
		})

		It("gives a Slack-origin chat the user index and its own channel", func() {
			tt, _ := turn(CreateInput{Model: "m"}, Origin{Kind: session.OriginConnector, ID: "b-slack"})
			Expect(tt.Prompt).To(ContainSubstring("lives in Rome"))
			Expect(tt.Prompt).To(ContainSubstring("SLACKNOTE"))
			Expect(tt.Prompt).NotTo(ContainSubstring("TGNOTE"))
			out, err := read(tt, `{"topic":"team","scope":"channel"}`)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("slack detail"))
		})

		It("gives a Telegram-origin chat the user index and its own channel", func() {
			tt, _ := turn(CreateInput{Model: "m"}, Origin{Kind: session.OriginConnector, ID: "b-tg"})
			Expect(tt.Prompt).To(ContainSubstring("lives in Rome"))
			Expect(tt.Prompt).To(ContainSubstring("TGNOTE"))
			Expect(tt.Prompt).NotTo(ContainSubstring("SLACKNOTE"))
			out, err := read(tt, `{"topic":"team"}`)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("telegram detail"))
			Expect(out).NotTo(ContainSubstring("slack"))
		})

		It("gives a plain chat the user index and no channel", func() {
			tt, _ := turn(CreateInput{Model: "m"}, Origin{})
			Expect(tt.Prompt).To(ContainSubstring("lives in Rome"))
			Expect(tt.Prompt).NotTo(ContainSubstring("SLACKNOTE"))
			Expect(tt.Prompt).NotTo(ContainSubstring("TGNOTE"))
			_, err := read(tt, `{"topic":"team"}`)
			Expect(err).To(MatchError(contextfiles.ErrNoTopic))
		})

		It("builds the same shape of block for the three", func() {
			shape := func(p string) string {
				i := strings.Index(p, contextfiles.Header)
				Expect(i).To(BeNumerically(">=", 0))
				return p[i : i+len(contextfiles.Header)]
			}
			a, _ := turn(CreateInput{Model: "m"}, Origin{})
			b, _ := turn(CreateInput{Model: "m"}, Origin{Kind: session.OriginConnector, ID: "b-slack"})
			c, _ := turn(CreateInput{Model: "m"}, Origin{Kind: session.OriginConnector, ID: "b-tg"})
			Expect(shape(a.Prompt)).To(Equal(shape(b.Prompt)))
			Expect(shape(b.Prompt)).To(Equal(shape(c.Prompt)))
		})
	})

	Describe("a scoped agent", func() {
		var mine, other agents.Agent
		BeforeEach(func() {
			register()
			_, err := f.Store().Mkdir(ctx, f.Actor("alice"), home+"/Clients/Acme")
			Expect(err).NotTo(HaveOccurred())
			mine, err = svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Mine", Model: "m", Instructions: "INSTR-MINE"},
				&AgentFolderWrite{Path: "/Clients/Acme", Mode: agents.FolderWrite})
			Expect(err).NotTo(HaveOccurred())
			other, err = svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Other", Model: "m"}, nil)
			Expect(err).NotTo(HaveOccurred())
			put("Memory/index.md", "- home: lives in Rome -> home.md\n")
			put("Memory/home.md", "user detail")
			put("Memory/agents/"+mine.ID+"/index.md", "- style: MYSTYLE -> style.md\n")
			put("Memory/agents/"+mine.ID+"/style.md", "my style detail")
			put("Memory/agents/"+other.ID+"/index.md", "- style: OTHERSTYLE\n")
			put("Memory/agents/"+other.ID+"/style.md", "other style detail")
			put(contextfiles.ProfilePath(mine.ID), "PROFILE-MINE: answer in French.")
		})

		It("reads its own index and the user index, and nothing of another agent", func() {
			tt, x := turn(CreateInput{Model: "m", AgentID: mine.ID}, Origin{})
			Expect(x.Scoped()).To(BeTrue())
			Expect(tt.Prompt).To(ContainSubstring("lives in Rome"))
			Expect(tt.Prompt).To(ContainSubstring("MYSTYLE"))
			Expect(tt.Prompt).NotTo(ContainSubstring("OTHERSTYLE"))
			out, err := read(tt, `{"topic":"style","scope":"agent"}`)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("my style detail"))
			Expect(out).NotTo(ContainSubstring("other"))
			out, err = read(tt, `{"topic":"home"}`)
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("user detail"))
		})

		It("gains no access to the files: the fence still refuses the notes and the profile", func() {
			_, x := turn(CreateInput{Model: "m", AgentID: mine.ID}, Origin{})
			for _, p := range []string{home + "/Memory/index.md", home + "/" + contextfiles.ProfilePath(mine.ID)} {
				_, _, err := f.Store().Open(ctx, x.Actor(), p)
				Expect(err).To(HaveOccurred(), p)
				_, err = f.Store().Put(ctx, x.Actor(), p, strings.NewReader("x"), workspace.PutOptions{Overwrite: true})
				Expect(err).To(HaveOccurred(), p)
			}
		})

		It("puts the profile after the instructions and before the index, and bounds it", func() {
			tt, _ := turn(CreateInput{Model: "m", AgentID: mine.ID}, Origin{})
			i, p, h := strings.Index(tt.Prompt, "INSTR-MINE"), strings.Index(tt.Prompt, "PROFILE-MINE"), strings.Index(tt.Prompt, contextfiles.Header)
			Expect(i).To(BeNumerically(">=", 0))
			Expect(p).To(BeNumerically(">", i))
			Expect(h).To(BeNumerically(">", p))
			put(contextfiles.ProfilePath(mine.ID), strings.Repeat("rule ", 5000))
			big, _ := turn(CreateInput{Model: "m", AgentID: mine.ID}, Origin{})
			Expect(strings.Count(big.Prompt, "rule ")).To(BeNumerically("<=", contextfiles.MaxProfileBytes/5))
		})

		It("reads no profile for a chat without an agent", func() {
			tt, _ := turn(CreateInput{Model: "m"}, Origin{})
			Expect(tt.Prompt).NotTo(ContainSubstring("PROFILE-MINE"))
		})

		It("leaves the profile out of the prompt when the file is absent", func() {
			tt, _ := turn(CreateInput{Model: "m", AgentID: other.ID}, Origin{})
			Expect(tt.Prompt).NotTo(ContainSubstring("Agent profile"))
		})
	})
})
