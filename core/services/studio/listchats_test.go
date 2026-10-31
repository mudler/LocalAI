package studio_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// A newer run of a sub-agent must not push older chats out of a list of chats.
var _ = Describe("listchats", func() {
	It("list chats skips sub agent runs before applying the limit", func() {
		ctx := context.Background()
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		base := time.Now().Add(-time.Hour)
		add := func(id, kind string, age int) {
			rec := session.ChatRecord{ID: id, Principal: studio.AnonymousPrincipal, Kind: kind, Created: base, Updated: base.Add(time.Duration(age) * time.Minute)}
			Expect(st.CreateChat(ctx, rec)).To(Succeed())
		}
		add("chat-old", session.KindChat, 1)
		add("chat-empty-kind", "", 2)
		for i := range 5 {
			add(fmt.Sprintf("run-%d", i), session.KindSubAgent, 10+i)
		}
		svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })

		got, err := svc.ListChats(ctx, "", 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(2), "want the two chats newest first, got %+v", got)
		Expect(got[0].ID).To(Equal("chat-empty-kind"), "want the two chats newest first, got %+v", got)
		Expect(got[1].ID).To(Equal("chat-old"), "want the two chats newest first, got %+v", got)
	})
})
