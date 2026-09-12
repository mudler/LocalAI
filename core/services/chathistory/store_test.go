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

var _ = Describe("Store", func() {
	var (
		db    *gorm.DB
		store *chathistory.Store
	)

	BeforeEach(func() {
		db = testutil.SetupTestDB()
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
			Expect(err).To(MatchError(chathistory.ErrNotFound))
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

			Expect(store.ReplaceAll(userID, []schema.Conversation{newConv("z", "z")})).To(Succeed())

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
})
