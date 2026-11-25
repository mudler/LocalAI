package store_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/privacy"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

var _ = Describe("Incognito", func() {
	var (
		ctx        context.Context
		persistent store.Store
		st         store.Store
		incog      *store.Incognito
	)
	BeforeEach(func() {
		ctx = context.Background()
		persistent, _ = store.NewJSON("")
		st, incog = store.NewIncognito(persistent)
	})
	chat := func(id, parent string) session.ChatRecord {
		now := time.Now()
		rec := session.ChatRecord{ID: id, Principal: "alice", ParentID: parent, Created: now, Updated: now}
		if parent != "" {
			rec.Kind = session.KindSubAgent
		}
		return rec
	}

	It("keeps a chat created with a level in memory only, and every other chat in the server's store", func() {
		Expect(st.CreateChat(privacy.WithLevel(ctx, privacy.ZeroRetention), chat("secret", ""))).To(Succeed())
		Expect(st.CreateChat(ctx, chat("kept", ""))).To(Succeed())
		_, err := persistent.GetChat(ctx, "alice", "secret")
		Expect(errors.Is(err, session.ErrNotFound)).To(BeTrue())
		_, err = persistent.GetChat(ctx, "alice", "kept")
		Expect(err).NotTo(HaveOccurred())
		_, err = st.GetChat(ctx, "alice", "secret")
		Expect(err).NotTo(HaveOccurred())
		_, err = st.AppendEntry(ctx, "secret", session.Entry{Role: session.RoleUser, Text: "SENTINEL"})
		Expect(err).NotTo(HaveOccurred())
		entries, err := persistent.Entries(ctx, "secret", 0, 10)
		Expect(err == nil && len(entries) == 0 || errors.Is(err, session.ErrNotFound)).To(BeTrue())
		list, err := st.ListChats(ctx, "alice", session.ChatCursor{}, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].ID).To(Equal("kept"))
		Expect(incog.Level("secret")).To(Equal(privacy.ZeroRetention))
		Expect(incog.Level("kept")).To(Equal(privacy.Normal))
	})

	It("makes the sub-agent of an incognito chat incognito and deletes it with its parent", func() {
		Expect(st.CreateChat(privacy.WithLevel(ctx, privacy.MemoryOff), chat("parent", ""))).To(Succeed())
		Expect(st.CreateChat(ctx, chat("child", "parent"))).To(Succeed())
		Expect(incog.Level("child")).To(Equal(privacy.MemoryOff))
		kids, err := st.ListChildren(ctx, "alice", "parent")
		Expect(err).NotTo(HaveOccurred())
		Expect(kids).To(HaveLen(1))
		Expect(st.DeleteChat(ctx, "alice", "parent")).To(Succeed())
		Expect(incog.Level("child")).To(Equal(privacy.Normal))
		_, err = st.GetChat(ctx, "alice", "child")
		Expect(errors.Is(err, session.ErrNotFound)).To(BeTrue())
	})

	It("lists the idle incognito chats, never a sub-agent", func() {
		Expect(st.CreateChat(privacy.WithLevel(ctx, privacy.MemoryOff), chat("p", ""))).To(Succeed())
		Expect(st.CreateChat(ctx, chat("c", "p"))).To(Succeed())
		Expect(incog.Idle(time.Now().Add(-time.Minute))).To(BeEmpty())
		Expect(incog.Idle(time.Now().Add(time.Minute))).To(ConsistOf(store.IdleChat{Principal: "alice", ID: "p"}))
	})

	It("never resumes an incognito chat by itself", func() {
		Expect(st.CreateChat(privacy.WithLevel(ctx, privacy.MemoryOff), chat("p", ""))).To(Succeed())
		Expect(st.ClaimTurn(ctx, "alice", "p", "run1")).To(Succeed())
		Expect(st.MarkInterrupted(ctx, "alice", "p", "run1", 0)).To(Succeed())
		due, err := st.ListResumable(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(due).To(BeEmpty())
	})

	It("answers Normal on a nil value", func() {
		var none *store.Incognito
		Expect(none.Level("x")).To(Equal(privacy.Normal))
		Expect(none.Idle(time.Now())).To(BeNil())
	})
})
