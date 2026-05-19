package chathistory_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/chathistory"
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
		dir   string
		store *chathistory.Store
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		store = chathistory.New(dir)
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
		// Second Store instance simulates a process restart: no shared
		// in-memory cache, so it must read what the first instance wrote
		// for the round-trip to succeed.
		It("loads conversations written by a previous instance", func() {
			first := chathistory.New(dir)
			_, err := first.Save("bob", newConv("x", "Hi"))
			Expect(err).NotTo(HaveOccurred())

			second := chathistory.New(dir)
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

	Context("unsafe IDs", func() {
		// idRegex must reject anything that could escape the user's
		// directory or be misread by os.WriteFile. These are the
		// classic path-traversal payloads plus a few edge cases.
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
		// Bulk migration scenario: client uploads its entire
		// conversation set in one shot, the store should overwrite
		// anything previously there instead of merging.
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
		// Drift from the anonymous/ layout would silently strand
		// anonymous users' history once they later log in, so the
		// test pins the exact path.
		It("stores conversations under the anonymous/ subdirectory", func() {
			_, err := store.Save("", newConv("solo", "anon chat"))
			Expect(err).NotTo(HaveOccurred())

			expected := filepath.Join(dir, "anonymous", "conversations.json")
			_, err = os.Stat(expected)
			Expect(err).NotTo(HaveOccurred(), "expected anonymous conversations file at %s", expected)

			second := chathistory.New(dir)
			got, err := second.Get("", "solo")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Name).To(Equal("anon chat"))
		})
	})
})
