package session_test

import (
	"context"
	"log/slog"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("the files of a chat whose agent has a folder", func() {
	It("names its files by the place and leaves out files outside its folders", func() {
		r := newFilesRig()
		ctx := context.Background()
		admin := workspace.Actor{Principal: filesAdmin}
		chatActor := workspace.Actor{Principal: "alice", ChatID: "c1"}
		home := session.HomePath("alice")

		c := r.open("alice", "c1")
		turn(c, session.Message{Text: "read this", Files: upload("q1.csv")})
		// Changes the chat made before its agent got a folder.
		put(r.ws, chatActor, home+"/Other/old-secret.txt", "x")
		put(r.ws, chatActor, home+"/Projects/Acme/report.md", "# report")

		cf, err := r.m.Files(ctx, "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(byName(cf)).To(HaveKey("old-secret.txt"), "unscoped, the list is today's")
		chatFolder := strings.TrimPrefix(strings.TrimSuffix(cf.Folder, "/"), session.GuestRoot+"/"+session.GuestHome)

		acme, err := r.ws.Stat(ctx, admin, home+"/Projects/Acme")
		Expect(err).NotTo(HaveOccurred())
		chat, err := r.ws.Stat(ctx, admin, home+chatFolder)
		Expect(err).NotTo(HaveOccurred())
		fenced := chatActor.Fenced(
			workspace.FenceRoot{Node: acme.ID, Mode: workspace.ModeRead},
			workspace.FenceRoot{Node: chat.ID, Mode: workspace.ModeWrite},
		)
		v, err := session.BuildView(ctx, r.ws, fenced, acme.Path, chat.Path, nil, slog.New(slog.DiscardHandler))
		Expect(err).NotTo(HaveOccurred())
		x, err := session.NewAccess(fenced, v)
		Expect(err).NotTo(HaveOccurred())

		cf, err = r.m.FilesIn(ctx, "alice", "c1", x.Place())
		Expect(err).NotTo(HaveOccurred())
		got := byName(cf)
		Expect(got).NotTo(HaveKey("old-secret.txt"))
		Expect(got).To(HaveKey("q1.csv"))
		Expect(got["q1.csv"].GuestPath).To(Equal("/workspace/chat/q1.csv"))
		Expect(got["report.md"].GuestPath).To(Equal("/workspace/home/report.md"))
		Expect(cf.Folder).To(Equal("/workspace/chat/"))
		p := session.FilesPrompt(cf)
		Expect(p).NotTo(ContainSubstring("Other"))
		Expect(p).NotTo(ContainSubstring("old-secret"))
	})
})
