package distributed_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("FineTuneStore", func() {
	var store *distributed.FineTuneStore

	BeforeEach(func() {
		db := testutil.SetupTestDB()
		var err error
		store, err = distributed.NewFineTuneStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("ListAll", func() {
		It("returns jobs across all users (unlike per-user List)", func() {
			Expect(store.Create(&distributed.FineTuneJobRecord{ID: "j1", UserID: "u1", Status: "queued"})).To(Succeed())
			Expect(store.Create(&distributed.FineTuneJobRecord{ID: "j2", UserID: "u2", Status: "queued"})).To(Succeed())

			all, err := store.ListAll()
			Expect(err).ToNot(HaveOccurred())
			Expect(all).To(HaveLen(2))

			perUser, err := store.List("u1")
			Expect(err).ToNot(HaveOccurred())
			Expect(perUser).To(HaveLen(1), "List stays per-user")
		})
	})

	Describe("Upsert", func() {
		It("inserts a new row", func() {
			Expect(store.Upsert(&distributed.FineTuneJobRecord{ID: "up-1", UserID: "u1", Status: "queued"})).To(Succeed())

			got, err := store.Get("up-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Status).To(Equal("queued"))
		})

		It("idempotently updates an existing row on a repeated key", func() {
			Expect(store.Upsert(&distributed.FineTuneJobRecord{ID: "up-2", UserID: "u1", Status: "queued"})).To(Succeed())
			// Second Upsert with the same primary key must update, not error on a
			// duplicate-key violation (this is the SyncedMap write-through contract).
			Expect(store.Upsert(&distributed.FineTuneJobRecord{ID: "up-2", UserID: "u1", Status: "completed", Message: "done"})).To(Succeed())

			got, err := store.Get("up-2")
			Expect(err).ToNot(HaveOccurred())
			Expect(got.Status).To(Equal("completed"))
			Expect(got.Message).To(Equal("done"))

			all, err := store.ListAll()
			Expect(err).ToNot(HaveOccurred())
			Expect(all).To(HaveLen(1), "upsert must not create a duplicate")
		})
	})
})
