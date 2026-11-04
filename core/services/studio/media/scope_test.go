package media_test

import (
	"bytes"
	"context"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/extract/extracttest"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// scopedAccess is the access of alice's chat whose agent works in
// /Projects/Acme, read only, with its chat folder.
func (fx *fixture) scopedAccess() session.Access {
	ctx := context.Background()
	user := fx.f.Actor("alice")
	mk := func(rel string) workspace.Node {
		abs, _ := fx.f.Abs("alice", rel)
		n, err := fx.f.Store().Mkdir(ctx, user, abs)
		if err != nil {
			n, err = fx.f.Store().Stat(ctx, user, abs)
		}
		Expect(err).NotTo(HaveOccurred())
		return n
	}
	acme := mk("/Projects/Acme")
	chat := mk("/" + session.ChatsDir + "/2026-11-01 Plan")
	a := workspace.Actor{Principal: "alice", ChatID: "c1", Groups: []string{files.EveryoneGroup}}.Fenced(
		workspace.FenceRoot{Node: acme.ID, Mode: workspace.ModeRead},
		workspace.FenceRoot{Node: chat.ID, Mode: workspace.ModeWrite},
	)
	v, err := session.BuildView(ctx, fx.f.Store(), a, acme.Path, chat.Path, nil, slog.New(slog.DiscardHandler))
	Expect(err).NotTo(HaveOccurred())
	x, err := session.NewAccess(a, v)
	Expect(err).NotTo(HaveOccurred())
	return x
}

func (fx *fixture) putBytes(rel string, data []byte) {
	abs, _ := fx.f.Abs("alice", rel)
	_, err := fx.f.Store().Put(context.Background(), fx.f.Actor("alice"), abs, bytes.NewReader(data), workspace.PutOptions{Overwrite: true})
	Expect(err).ToNot(HaveOccurred())
}

var _ = Describe("media tools of a chat whose agent has a folder", func() {
	It("read_document resolves /Other/x.pdf inside the folder", func() {
		fx := newFixture("vision-model", "whisper-1")
		fx.putBytes("/Other/x.pdf", extracttest.TextPDF("OUTSIDE-7f3a9c secret"))
		x := fx.scopedAccess()
		tl := fx.toolOf("read_document", x, "chat-model")
		_, err := run(tl, map[string]any{"path": "/Other/x.pdf"})
		Expect(err).To(MatchError("not found"))
		fx.putBytes("/Projects/Acme/Other/x.pdf", extracttest.TextPDF("inside text"))
		out, err := run(tl, map[string]any{"path": "/Other/x.pdf"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("inside text"))
		Expect(out).NotTo(ContainSubstring("OUTSIDE-7f3a9c"))
		out, err = run(tl, map[string]any{"path": "/workspace/home/Other/x.pdf"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("inside text"))
	})

	It("never reads outside the folder, whatever the spelling", func() {
		fx := newFixture("vision-model", "whisper-1")
		fx.putBytes("/Other/x.pdf", extracttest.TextPDF("OUTSIDE-7f3a9c secret"))
		x := fx.scopedAccess()
		for _, name := range []string{"read_document", "describe_image"} {
			tl := fx.toolOf(name, x, "chat-model")
			for _, p := range []string{"../Other/x.pdf", "/workspace/home/../Other/x.pdf", "/workspace/chat/../../Other/x.pdf",
				"/users/alice/Other/x.pdf", "..%2fOther%2fx.pdf", "․․/Other/x.pdf", "/workspace/Home/Other/x.pdf", "/Workspace/Home/Other/x.pdf", "Other\x00x.pdf"} {
				out, err := run(tl, map[string]any{"path": p})
				Expect(err).To(HaveOccurred(), "%s %q", name, p)
				Expect(out).NotTo(ContainSubstring("OUTSIDE"))
				Expect(err.Error()).NotTo(ContainSubstring("OUTSIDE"))
			}
		}
	})

	It("reads an attachment in the chat folder", func() {
		fx := newFixture("vision-model", "whisper-1")
		x := fx.scopedAccess()
		_, err := fx.f.Store().Put(context.Background(), x.Actor(), x.Place().Chat()+"/doc.pdf", bytes.NewReader(extracttest.TextPDF("attached words")), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		out, err := run(fx.toolOf("read_document", x, "chat-model"), map[string]any{"path": "/workspace/chat/doc.pdf"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("attached words"))
	})
})
