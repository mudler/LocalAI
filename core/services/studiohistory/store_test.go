package studiohistory_test

import (
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studiohistory"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// newEntry builds an entry shaped like the Studio's (prompt, model, params,
// results); createdAt orders the list.
func newEntry(id, mediaType string, createdAt int64) schema.StudioHistoryEntry {
	body, _ := json.Marshal(map[string]any{
		"id":        id,
		"prompt":    "a grey robot in a kitchen",
		"model":     "test-model",
		"params":    map[string]any{"size": "768x512"},
		"results":   []map[string]any{{"url": "/generated-videos/" + id + ".mp4"}},
		"createdAt": createdAt,
	})
	return schema.StudioHistoryEntry{ID: id, Type: mediaType, Entry: body, CreatedAt: createdAt}
}

func ids(entries []schema.StudioHistoryEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

// The store runs on whichever database backs auth: SQLite by default,
// PostgreSQL in distributed setups. Every spec runs against both.
var _ = Describe("Store on PostgreSQL", func() {
	describeStore(testutil.SetupTestDB)
})

func describeStore(openDB func() *gorm.DB) {
	var store *studiohistory.Store

	BeforeEach(func() {
		var err error
		store, err = studiohistory.New(openDB())
		Expect(err).NotTo(HaveOccurred())
	})

	Context("basic CRUD", func() {
		const userID = "alice"

		It("saves, lists newest first, gets and deletes entries", func() {
			for i, id := range []string{"v1", "v2", "v3"} {
				saved, err := store.Save(userID, newEntry(id, "video", int64(1000+i)))
				Expect(err).NotTo(HaveOccurred())
				Expect(saved.Version).To(Equal(int64(1)))
			}
			list, err := store.List(userID, "video")
			Expect(err).NotTo(HaveOccurred())
			Expect(ids(list)).To(Equal([]string{"v3", "v2", "v1"}))

			got, err := store.Get(userID, "v2")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Type).To(Equal("video"))
			Expect(got.CreatedAt).To(Equal(int64(1001)))
			var body map[string]any
			Expect(json.Unmarshal(got.Entry, &body)).To(Succeed())
			Expect(body["prompt"]).To(Equal("a grey robot in a kitchen"))

			Expect(store.Delete(userID, "v2", "")).To(Succeed())
			list, err = store.List(userID, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ids(list)).To(Equal([]string{"v3", "v1"}))
			_, err = store.Get(userID, "v2")
			Expect(err).To(MatchError(studiohistory.ErrDeleted))
			_, err = store.Get(userID, "never")
			Expect(err).To(MatchError(studiohistory.ErrNotFound))
		})

		It("filters by workspace", func() {
			_, err := store.Save(userID, newEntry("img", "image", 1))
			Expect(err).NotTo(HaveOccurred())
			_, err = store.Save(userID, newEntry("vid", "video", 2))
			Expect(err).NotTo(HaveOccurred())
			images, err := store.List(userID, "image")
			Expect(err).NotTo(HaveOccurred())
			Expect(ids(images)).To(Equal([]string{"img"}))
			all, err := store.List(userID, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ids(all)).To(Equal([]string{"vid", "img"}))
		})

		It("never leaks one user's entries to another", func() {
			_, err := store.Save("alice", newEntry("same-id", "image", 1))
			Expect(err).NotTo(HaveOccurred())
			_, err = store.Save("bob", newEntry("same-id", "image", 2))
			Expect(err).NotTo(HaveOccurred())
			Expect(store.Delete("bob", "same-id", "")).To(Succeed())

			got, err := store.Get("alice", "same-id")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.CreatedAt).To(Equal(int64(1)))
			bobs, err := store.List("bob", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(bobs).To(BeEmpty())
		})

		It("gives each account its own stable, opaque cache key", func() {
			Expect(studiohistory.CacheKey("alice")).To(Equal(studiohistory.CacheKey("alice")))
			Expect(studiohistory.CacheKey("alice")).NotTo(Equal(studiohistory.CacheKey("bob")))
			Expect(studiohistory.CacheKey("alice")).NotTo(ContainSubstring("alice"))
		})

		DescribeTable("rejects",
			func(mutate func(*schema.StudioHistoryEntry), want error) {
				e := newEntry("ok", "image", 1)
				mutate(&e)
				_, err := store.Save(userID, e)
				Expect(err).To(MatchError(want))
			},
			Entry("an id with a space", func(e *schema.StudioHistoryEntry) { e.ID = "a b" }, studiohistory.ErrInvalidID),
			Entry("an empty id", func(e *schema.StudioHistoryEntry) { e.ID = "" }, studiohistory.ErrInvalidID),
			Entry("an unknown workspace", func(e *schema.StudioHistoryEntry) { e.Type = "music-video" }, studiohistory.ErrInvalidType),
			Entry("an entry that is not an object", func(e *schema.StudioHistoryEntry) { e.Entry = json.RawMessage(`[1,2]`) }, studiohistory.ErrInvalidEntry),
			Entry("a missing entry", func(e *schema.StudioHistoryEntry) { e.Entry = nil }, studiohistory.ErrInvalidEntry),
			Entry("an oversized entry", func(e *schema.StudioHistoryEntry) {
				e.Entry = json.RawMessage(fmt.Sprintf(`{"prompt":%q}`, make([]byte, studiohistory.MaxEntryBytes)))
			}, studiohistory.ErrInvalidEntry),
		)

		It("keeps an entry in the workspace of its first save", func() {
			_, err := store.Save(userID, newEntry("x", "image", 1))
			Expect(err).NotTo(HaveOccurred())
			_, err = store.Save(userID, newEntry("x", "video", 1))
			Expect(err).To(MatchError(studiohistory.ErrInvalidType))
		})
	})

	Context("tombstones", func() {
		const userID = "alice"

		It("rejects a save after delete instead of resurrecting the entry", func() {
			_, err := store.Save(userID, newEntry("gone", "image", 1))
			Expect(err).NotTo(HaveOccurred())
			Expect(store.Delete(userID, "gone", "")).To(Succeed())
			_, err = store.Save(userID, newEntry("gone", "image", 1))
			Expect(err).To(MatchError(studiohistory.ErrDeleted))

			dead, err := store.Tombstones(userID, "image")
			Expect(err).NotTo(HaveOccurred())
			Expect(dead).To(HaveLen(1))
			Expect(dead[0].ID).To(Equal("gone"))
			Expect(dead[0].Type).To(Equal("image"))
			Expect(dead[0].Version).To(Equal(int64(2)))
			Expect(dead[0].DeletedAt).To(BeNumerically(">", 0))
		})

		It("records a delete that arrives before the entry's first save", func() {
			Expect(store.Delete(userID, "early", "video")).To(Succeed())
			_, err := store.Save(userID, newEntry("early", "video", 1))
			Expect(err).To(MatchError(studiohistory.ErrDeleted))
			dead, err := store.Tombstones(userID, "video")
			Expect(err).NotTo(HaveOccurred())
			Expect(ids2(dead)).To(Equal([]string{"early"}))
		})

		It("treats a repeated delete as success", func() {
			_, err := store.Save(userID, newEntry("twice", "tts", 1))
			Expect(err).NotTo(HaveOccurred())
			Expect(store.Delete(userID, "twice", "")).To(Succeed())
			Expect(store.Delete(userID, "twice", "")).To(Succeed())
		})

		It("clears one workspace without touching the others or other users", func() {
			for i, t := range []string{"image", "image", "video"} {
				_, err := store.Save(userID, newEntry(fmt.Sprintf("e%d", i), t, int64(i)))
				Expect(err).NotTo(HaveOccurred())
			}
			_, err := store.Save("bob", newEntry("b", "image", 1))
			Expect(err).NotTo(HaveOccurred())

			Expect(store.DeleteAll(userID, "image")).To(Succeed())
			left, err := store.List(userID, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ids(left)).To(Equal([]string{"e2"}))
			bobs, err := store.List("bob", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(bobs).To(HaveLen(1))

			Expect(store.DeleteAll(userID, "")).To(Succeed())
			left, err = store.List(userID, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(left).To(BeEmpty())
		})
	})

	Context("cap per workspace", func() {
		const userID = "alice"

		It("turns the oldest entries beyond the cap into tombstones", func() {
			store.SetMaxPerType(3)
			for i := 1; i <= 5; i++ {
				_, err := store.Save(userID, newEntry(fmt.Sprintf("v%d", i), "video", int64(i)))
				Expect(err).NotTo(HaveOccurred())
			}
			_, err := store.Save(userID, newEntry("img", "image", 1))
			Expect(err).NotTo(HaveOccurred())

			videos, err := store.List(userID, "video")
			Expect(err).NotTo(HaveOccurred())
			Expect(ids(videos)).To(Equal([]string{"v5", "v4", "v3"}))
			dead, err := store.Tombstones(userID, "video")
			Expect(err).NotTo(HaveOccurred())
			Expect(ids2(dead)).To(ConsistOf("v1", "v2"))
			images, err := store.List(userID, "image")
			Expect(err).NotTo(HaveOccurred())
			Expect(images).To(HaveLen(1))
		})
	})

	Context("import", func() {
		const userID = "alice"

		It("adds and updates without deleting, and skips deleted ids", func() {
			_, err := store.Save(userID, newEntry("kept", "image", 1))
			Expect(err).NotTo(HaveOccurred())
			_, err = store.Save(userID, newEntry("dead", "image", 2))
			Expect(err).NotTo(HaveOccurred())
			Expect(store.Delete(userID, "dead", "")).To(Succeed())

			stored, skipped, err := store.Import(userID, []schema.StudioHistoryEntry{
				newEntry("new1", "image", 3),
				newEntry("dead", "image", 2),
				newEntry("new2", "video", 4),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(stored).To(Equal(2))
			Expect(skipped).To(Equal([]string{"dead"}))

			all, err := store.List(userID, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(ids(all)).To(ConsistOf("kept", "new1", "new2"))
		})

		It("stores nothing when one entry is invalid", func() {
			bad := newEntry("bad id", "image", 2)
			_, _, err := store.Import(userID, []schema.StudioHistoryEntry{newEntry("good", "image", 1), bad})
			Expect(err).To(MatchError(studiohistory.ErrInvalidID))
			all, err := store.List(userID, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(all).To(BeEmpty())
		})
	})

	Context("versions and timestamps", func() {
		const userID = "alice"

		It("starts at 1, bumps on every write and ignores a client version", func() {
			e := newEntry("e", "sound", 1)
			e.Version = 41
			saved, err := store.Save(userID, e)
			Expect(err).NotTo(HaveOccurred())
			Expect(saved.Version).To(Equal(int64(1)))
			saved, err = store.Save(userID, e)
			Expect(err).NotTo(HaveOccurred())
			Expect(saved.Version).To(Equal(int64(2)))
		})

		It("keeps the client's createdAt, and the stored one when an update omits it", func() {
			e := newEntry("e", "image", 1234)
			e.UpdatedAt = 5678
			_, err := store.Save(userID, e)
			Expect(err).NotTo(HaveOccurred())
			e.CreatedAt = 0
			saved, err := store.Save(userID, e)
			Expect(err).NotTo(HaveOccurred())
			Expect(saved.CreatedAt).To(Equal(int64(1234)))
			got, err := store.Get(userID, "e")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.UpdatedAt).To(Equal(int64(5678)))
		})

		It("counts every one of many concurrent writes", func() {
			_, err := store.Save(userID, newEntry("hot", "image", 1))
			Expect(err).NotTo(HaveOccurred())
			const writers = 8
			errs := make(chan error, writers)
			for i := 0; i < writers; i++ {
				go func() {
					defer GinkgoRecover()
					_, err := store.Save(userID, newEntry("hot", "image", 1))
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
}

func ids2(t []schema.StudioHistoryTombstone) []string {
	out := make([]string, 0, len(t))
	for _, x := range t {
		out = append(out, x.ID)
	}
	return out
}
