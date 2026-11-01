package studio

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// scopedTurnAccess is the access of alice's chat c1 whose agent works in
// /Projects/Acme at mode, with its chat folder.
func scopedTurnAccess(f *files.Service, mode workspace.Mode, chatFolder string) session.Access {
	ctx := context.Background()
	user := f.Actor("alice")
	mk := func(rel string) workspace.Node {
		abs, _ := f.Abs("alice", rel)
		n, err := f.Store().Mkdir(ctx, user, abs)
		if err != nil {
			n, err = f.Store().Stat(ctx, user, abs)
		}
		Expect(err).NotTo(HaveOccurred())
		return n
	}
	acme := mk("/Projects/Acme")
	chat := mk("/" + session.ChatsDir + "/" + chatFolder)
	a := f.Actor("alice")
	a.ChatID = "c1"
	a = a.Fenced(workspace.FenceRoot{Node: acme.ID, Mode: mode}, workspace.FenceRoot{Node: chat.ID, Mode: workspace.ModeWrite})
	v, err := session.BuildView(ctx, f.Store(), a, acme.Path, chat.Path, nil, slog.New(slog.DiscardHandler))
	Expect(err).NotTo(HaveOccurred())
	x, err := session.NewAccess(a, v)
	Expect(err).NotTo(HaveOccurred())
	return x
}

func userPut(f *files.Service, user, rel, content string) {
	abs, err := f.Abs(user, rel)
	Expect(err).NotTo(HaveOccurred())
	_, err = f.Store().Put(context.Background(), f.Actor(user), abs, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("the rules nudge of a chat whose agent has a folder", func() {
	It("starts at the agent's folder and never points above it", func() {
		ctx := context.Background()
		svc, f := newSkillService(&skillBroker{})
		for _, rel := range []string{"", "/Projects", "/Projects/Acme", "/Projects/Acme/sub", "/" + session.ChatsDir + "/2026-11-01 Plan"} {
			userPut(f, "alice", rel+"/"+triggers.RulesFile, "rules")
		}
		x := scopedTurnAccess(f, workspace.ModeRead, "2026-11-01 Plan")
		n := svc.newRulesNudge(ctx, x, session.ChatRecord{Folder: "2026-11-01 Plan"})
		Expect(n).NotTo(BeNil())
		line := n.turnLine(ctx, session.ChatRecord{Folder: "2026-11-01 Plan"})
		Expect(line).To(Equal("Rules for this folder are in /workspace/home/AGENTS.md. Read that file before you start."))

		ok := func(context.Context, json.RawMessage) (string, error) { return "done", nil }
		tools := n.wrap([]session.Tool{{Name: "read_file", Run: ok}, {Name: "list_files", Run: ok}})
		out, err := tools[0].Run(ctx, json.RawMessage(`{"path":"/sub/x.txt"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("/workspace/home/sub/AGENTS.md"))
		for _, p := range []string{"/workspace/chat/x.txt", "/workspace/chat", "../x", "/workspace/home/../x", "/users/alice/x"} {
			out, err := tools[1].Run(ctx, json.RawMessage(`{"path":"`+p+`"}`))
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal("done"), p)
		}
		Expect(strings.Contains(line, "Projects")).To(BeFalse())
	})

	It("keeps today's walk for an unscoped chat", func() {
		ctx := context.Background()
		svc, f := newSkillService(&skillBroker{})
		userPut(f, "alice", "/"+triggers.RulesFile, "rules")
		a := f.Actor("alice")
		a.ChatID = "c1"
		x, err := svc.mgr.UserAccess(ctx, a)
		Expect(err).NotTo(HaveOccurred())
		n := svc.newRulesNudge(ctx, x, session.ChatRecord{Folder: "2026-11-01 Plan"})
		Expect(n.turnLine(ctx, session.ChatRecord{Folder: "2026-11-01 Plan"})).To(Equal("Rules for this folder are in /workspace/home/AGENTS.md. Read that file before you start."))
	})
})

var _ = Describe("the tools of a turn without a resolved access", func() {
	It("gives no tools rather than tools on a guessed view", func() {
		ctx := context.Background()
		svc, f := newSkillService(&skillBroker{})
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		a := f.Actor("alice")
		a.ChatID = rec.ID
		empty, err := session.NewAccess(a, workspace.View{})
		Expect(err).NotTo(HaveOccurred())
		tt, err := svc.chatTools(ctx, empty)
		Expect(err).To(MatchError(errNoTurnAccess))
		Expect(tt.Tools).To(BeEmpty())

		x, err := svc.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		tt, err = svc.chatTools(ctx, x)
		Expect(err).NotTo(HaveOccurred())
		Expect(tt.Tools).NotTo(BeEmpty(), "the resolved access gets its tools")
	})
})
