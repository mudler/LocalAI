package studio

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// getFailStore fails the read of a chat once failing is set.
type getFailStore struct {
	store.Store
	failing bool
}

func (f *getFailStore) GetChat(ctx context.Context, principal, id string) (session.ChatRecord, error) {
	if f.failing {
		return session.ChatRecord{}, errors.New("disk on fire")
	}
	return f.Store.GetChat(ctx, principal, id)
}

var _ = Describe("the collection restriction of a chat for a knowledge write", func() {
	ctx := context.Background()

	It("fails closed when the chat cannot be read, and fails open only for a chat with no record", func() {
		inner, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		fs := &getFailStore{Store: inner}
		svc, err := New(Deps{Store: fs, Provider: provider.New(), StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(ctx) })
		rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m", Collections: []string{"wiki"}})
		Expect(err).NotTo(HaveOccurred())

		Expect(svc.collectionRefused(ctx, "alice", rec.ID, "wiki")).To(Succeed())
		Expect(svc.collectionRefused(ctx, "alice", rec.ID, "manuals")).To(MatchError(`there is no collection "manuals"`))
		fs.failing = true
		Expect(svc.collectionRefused(ctx, "alice", rec.ID, "wiki")).To(MatchError(`there is no collection "wiki"`), "an unreadable chat is refused")
		fs.failing = false
		Expect(svc.collectionRefused(ctx, "alice", "no-such-chat", "wiki")).To(Succeed(), "a chat with no record has no restriction")
	})
})
