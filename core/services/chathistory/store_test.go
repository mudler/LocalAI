package chathistory_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/chathistory"
	"github.com/mudler/LocalAI/core/services/testutil"
)

func newConv(id, name string) schema.Conversation {
	history, _ := json.Marshal([]map[string]any{
		{"role": "user", "content": "hi"},
		{"role": "assistant", "content": "hello"},
	})
	return schema.Conversation{
		ID:      id,
		Name:    name,
		Model:   "test-model",
		History: history,
	}
}

// The store runs on whichever database backs auth: SQLite by default,
// PostgreSQL in distributed setups. Every spec runs against both so the
// tombstone and version SQL cannot drift into one dialect.
var _ = Describe("Store on PostgreSQL", func() {
	describeStore(testutil.SetupTestDB)
})

func describeStore(openDB func() *gorm.DB) {
	var (
		db    *gorm.DB
		store *chathistory.Store
	)

	BeforeEach(func() {
		db = openDB()
		var err error
		store, err = chathistory.New(db)
		Expect(err).NotTo(HaveOccurred())
	})

	Context("basic CRUD", func() {
		const userID = "alice"

		It("saves, lists, gets, and deletes a conversation", func() {
			_, err := store.Save(userID, newConv("c1", "First"))
			Expect(err).NotTo(HaveOccurred())
			_, err = store.Save(userID, newConv("c2", "Second"))
			Expect(err).NotTo(HaveOccurred())

			list, err := store.List(userID)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(2))

			got, err := store.Get(userID, "c1")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Name).To(Equal("First"))
			Expect(got.CreatedAt).NotTo(BeZero(), "Save should populate CreatedAt")
			Expect(got.UpdatedAt).NotTo(BeZero(), "Save should populate UpdatedAt")

			Expect(store.Delete(userID, "c1")).To(Succeed())
			_, err = store.Get(userID, "c1")
			Expect(err).To(MatchError(chathistory.ErrDeleted))
		})
	})

	Context("persistence across Store instances", func() {
		// Two Stores sharing the same *gorm.DB simulate a process restart:
		// no shared in-memory state, so the second must read what the first
		// wrote for the round-trip to succeed.
		It("loads conversations written by a previous instance", func() {
			first, err := chathistory.New(db)
			Expect(err).NotTo(HaveOccurred())
			_, err = first.Save("bob", newConv("x", "Hi"))
			Expect(err).NotTo(HaveOccurred())

			second, err := chathistory.New(db)
			Expect(err).NotTo(HaveOccurred())
			got, err := second.Get("bob", "x")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Name).To(Equal("Hi"))
		})
	})

	Context("user isolation", func() {
		It("never leaks one user's data to another", func() {
			_, err := store.Save("alice", newConv("a1", "alice's chat"))
			Expect(err).NotTo(HaveOccurred())
			_, err = store.Save("bob", newConv("b1", "bob's chat"))
			Expect(err).NotTo(HaveOccurred())

			bobList, err := store.List("bob")
			Expect(err).NotTo(HaveOccurred())
			Expect(bobList).To(HaveLen(1))
			Expect(bobList[0].ID).To(Equal("b1"))

			_, err = store.Get("bob", "a1")
			Expect(err).To(MatchError(chathistory.ErrNotFound))
		})
	})

	Context("malformed IDs", func() {
		// The DB-backed store no longer needs to defend against path
		// traversal, but idRegex still rejects whitespace / control
		// characters so IDs stay safe in logs and HTTP responses. The
		// same payloads exercise the empty-string and over-length cases.
		DescribeTable("rejects",
			func(badID string) {
				_, err := store.Save("alice", schema.Conversation{ID: badID, Name: "x"})
				Expect(err).To(HaveOccurred())
			},
			Entry("path traversal", "../etc/passwd"),
			Entry("forward slash", "a/b"),
			Entry("back slash", "a\\b"),
			Entry("empty id", ""),
			Entry("contains spaces", "id with spaces"),
		)
	})

	Context("ReplaceAll", func() {
		// Bulk migration scenario: client uploads its entire conversation
		// set in one shot, the store should overwrite anything previously
		// there instead of merging.
		It("overwrites the entire conversation set", func() {
			const userID = "alice"
			for _, id := range []string{"a", "b", "c"} {
				_, err := store.Save(userID, newConv(id, id))
				Expect(err).NotTo(HaveOccurred())
			}

			// Second return value (error) must be nil; the skipped list empty.
			Expect(store.ReplaceAll(userID, []schema.Conversation{newConv("z", "z")})).To(BeEmpty())

			list, err := store.List(userID)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			Expect(list[0].ID).To(Equal("z"))
		})
	})

	Context("anonymous user", func() {
		// UserID == "" maps to the anonymous slice. We can no longer pin a
		// directory layout (the previous file-based store wrote
		// anonymous/conversations.json), so the test checks the round-trip
		// and the per-user isolation guarantee instead.
		It("stores and retrieves conversations for an empty user ID", func() {
			_, err := store.Save("", newConv("solo", "anon chat"))
			Expect(err).NotTo(HaveOccurred())

			got, err := store.Get("", "solo")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Name).To(Equal("anon chat"))

			// And the conversation must NOT leak to a logged-in user.
			_, err = store.Get("alice", "solo")
			Expect(err).To(MatchError(chathistory.ErrNotFound))
		})
	})

	Context("DeleteAll", func() {
		It("wipes the user's entire chat history without touching others", func() {
			_, err := store.Save("alice", newConv("a1", "alice 1"))
			Expect(err).NotTo(HaveOccurred())
			_, err = store.Save("alice", newConv("a2", "alice 2"))
			Expect(err).NotTo(HaveOccurred())
			_, err = store.Save("bob", newConv("b1", "bob 1"))
			Expect(err).NotTo(HaveOccurred())

			Expect(store.DeleteAll("alice")).To(Succeed())

			aliceList, err := store.List("alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(aliceList).To(BeEmpty())

			bobList, err := store.List("bob")
			Expect(err).NotTo(HaveOccurred())
			Expect(bobList).To(HaveLen(1))
		})
	})
	// A conversation deleted on one device must stay deleted when a client
	// with a stale copy writes it again (review on #9902). Deletes leave a
	// tombstone the store refuses to overwrite.
	Context("tombstones", func() {
		const userID = "alice"

		It("rejects a save after delete instead of resurrecting the row", func() {
			_, err := store.Save(userID, newConv("c1", "First"))
			Expect(err).NotTo(HaveOccurred())
			Expect(store.Delete(userID, "c1")).To(Succeed())

			_, err = store.Save(userID, newConv("c1", "stale copy"))
			Expect(err).To(MatchError(chathistory.ErrDeleted))

			list, err := store.List(userID)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(BeEmpty())
			Expect(tombstoneIDs(store, userID)).To(ConsistOf("c1"))
			_, err = store.Get(userID, "c1")
			Expect(err).To(MatchError(chathistory.ErrDeleted))
		})

		It("drops the content of a deleted conversation", func() {
			_, err := store.Save(userID, newConv("c1", "secret"))
			Expect(err).NotTo(HaveOccurred())
			Expect(store.Delete(userID, "c1")).To(Succeed())

			var row chathistory.ConversationRecord
			Expect(db.Unscoped().Where("user_id = ? AND conv_id = ?", userID, "c1").Take(&row).Error).To(Succeed())
			Expect(row.DeletedAt.Valid).To(BeTrue())
			Expect(row.Content).To(BeEmpty())
		})

		It("records a delete that arrives before the conversation's first save", func() {
			// The UI deletes right away while a debounced PUT may still be in
			// flight; whichever lands second must not bring the chat back.
			Expect(store.Delete(userID, "early")).To(Succeed())
			_, err := store.Save(userID, newConv("early", "late write"))
			Expect(err).To(MatchError(chathistory.ErrDeleted))
		})

		It("treats a repeated delete as success", func() {
			_, err := store.Save(userID, newConv("c1", "First"))
			Expect(err).NotTo(HaveOccurred())
			Expect(store.Delete(userID, "c1")).To(Succeed())
			Expect(store.Delete(userID, "c1")).To(Succeed())
			Expect(tombstoneIDs(store, userID)).To(ConsistOf("c1"))
		})

		It("tombstones every conversation on DeleteAll", func() {
			for _, id := range []string{"a", "b"} {
				_, err := store.Save(userID, newConv(id, id))
				Expect(err).NotTo(HaveOccurred())
			}
			_, err := store.Save("bob", newConv("a", "bob's a"))
			Expect(err).NotTo(HaveOccurred())

			Expect(store.DeleteAll(userID)).To(Succeed())
			Expect(tombstoneIDs(store, userID)).To(ConsistOf("a", "b"))
			_, err = store.Save(userID, newConv("a", "stale"))
			Expect(err).To(MatchError(chathistory.ErrDeleted))

			// bob shares the id, not the tombstone.
			_, err = store.Save("bob", newConv("a", "bob again"))
			Expect(err).NotTo(HaveOccurred())
		})

		It("does not let ReplaceAll resurrect deleted ids and tombstones what it replaces", func() {
			for _, id := range []string{"a", "b"} {
				_, err := store.Save(userID, newConv(id, id))
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(store.Delete(userID, "b")).To(Succeed())

			skipped, err := store.ReplaceAll(userID, []schema.Conversation{newConv("b", "stale b"), newConv("c", "c")})
			Expect(err).NotTo(HaveOccurred())
			Expect(skipped).To(ConsistOf("b"))

			list, err := store.List(userID)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			Expect(list[0].ID).To(Equal("c"))
			// "a" was replaced away: other devices must see it as deleted too.
			Expect(tombstoneIDs(store, userID)).To(ConsistOf("a", "b"))
		})
	})

	Context("versions", func() {
		const userID = "alice"

		It("starts at 1 and bumps on every write, including the delete", func() {
			Expect(savedVersion(store, userID, newConv("c1", "v1"))).To(Equal(int64(1)))
			Expect(savedVersion(store, userID, newConv("c1", "v2"))).To(Equal(int64(2)))

			list, err := store.List(userID)
			Expect(err).NotTo(HaveOccurred())
			Expect(list[0].Version).To(Equal(int64(2)))

			Expect(store.Delete(userID, "c1")).To(Succeed())
			tombs, err := store.Tombstones(userID)
			Expect(err).NotTo(HaveOccurred())
			Expect(tombs).To(HaveLen(1))
			Expect(tombs[0].Version).To(Equal(int64(3)))
			Expect(tombs[0].DeletedAt).NotTo(BeZero())
		})

		It("ignores a client-supplied version", func() {
			conv := newConv("c1", "x")
			conv.Version = 41
			Expect(savedVersion(store, userID, conv)).To(Equal(int64(1)))
		})

		It("counts every one of many concurrent writes", func() {
			Expect(savedVersion(store, userID, newConv("hot", "seed"))).To(Equal(int64(1)))
			const writers = 8
			errs := make(chan error, writers)
			for i := 0; i < writers; i++ {
				go func() {
					defer GinkgoRecover()
					_, err := store.Save(userID, newConv("hot", "concurrent"))
					errs <- err
				}()
			}
			for i := 0; i < writers; i++ {
				Expect(<-errs).NotTo(HaveOccurred())
			}
			got, err := store.Get(userID, "hot")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Version).To(Equal(int64(1 + writers)))
		})
	})

	Context("timestamps", func() {
		const userID = "alice"

		// The UI decides which copy is newer by comparing updatedAt across
		// devices, so the server must store the client's value rather than
		// its own clock.
		It("keeps the client's updatedAt and createdAt", func() {
			conv := newConv("c1", "x")
			conv.CreatedAt = 1000
			conv.UpdatedAt = 2000
			_, err := store.Save(userID, conv)
			Expect(err).NotTo(HaveOccurred())
			got, err := store.Get(userID, "c1")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.CreatedAt).To(Equal(int64(1000)))
			Expect(got.UpdatedAt).To(Equal(int64(2000)))
		})

		It("keeps the original createdAt when an update omits it", func() {
			conv := newConv("c1", "x")
			conv.CreatedAt = 1000
			_, err := store.Save(userID, conv)
			Expect(err).NotTo(HaveOccurred())
			_, err = store.Save(userID, newConv("c1", "y"))
			Expect(err).NotTo(HaveOccurred())
			got, err := store.Get(userID, "c1")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.CreatedAt).To(Equal(int64(1000)))
		})
	})
}

func savedVersion(store *chathistory.Store, userID string, conv schema.Conversation) int64 {
	saved, err := store.Save(userID, conv)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return saved.Version
}

func tombstoneIDs(store *chathistory.Store, userID string) []string {
	tombs, err := store.Tombstones(userID)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ids := []string{}
	for _, t := range tombs {
		ids = append(ids, t.ID)
	}
	return ids
}
