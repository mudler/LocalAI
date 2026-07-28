package distributed_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("GalleryStore terminal operations", func() {
	var store *distributed.GalleryStore

	BeforeEach(func() {
		db := testutil.SetupTestDB()
		var err error
		store, err = distributed.NewGalleryStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	// finish drives a row through to a terminal status the same way the gallery
	// service does, so updated_at reflects when it actually finished.
	finish := func(id, status string) {
		Expect(store.UpdateStatus(id, status, "")).To(Succeed())
		// Postgres timestamps are microsecond-precision, but two updates issued
		// back to back can still land close enough to make the assertion on
		// ordering a coin toss.
		time.Sleep(5 * time.Millisecond)
	}

	Describe("ListTerminal", func() {
		It("returns only finished operations", func() {
			Expect(store.Create(&distributed.GalleryOperationRecord{
				ID: "running", GalleryElementName: "busy", OpType: "model_install", Status: "downloading",
			})).To(Succeed())
			Expect(store.Create(&distributed.GalleryOperationRecord{
				ID: "queued", GalleryElementName: "waiting", OpType: "model_install", Status: "pending",
			})).To(Succeed())
			Expect(store.Create(&distributed.GalleryOperationRecord{
				ID: "done", GalleryElementName: "installed", OpType: "model_install", Status: "pending",
			})).To(Succeed())
			Expect(store.Create(&distributed.GalleryOperationRecord{
				ID: "broke", GalleryElementName: "failed-one", OpType: "backend_install", Status: "pending",
			})).To(Succeed())
			Expect(store.Create(&distributed.GalleryOperationRecord{
				ID: "stopped", GalleryElementName: "cancelled-one", OpType: "model_install", Status: "pending",
			})).To(Succeed())

			finish("done", "completed")
			finish("broke", "failed")
			finish("stopped", "cancelled")

			ops, err := store.ListTerminal(0)
			Expect(err).ToNot(HaveOccurred())

			ids := []string{}
			for _, op := range ops {
				ids = append(ids, op.ID)
			}
			Expect(ids).To(ConsistOf("done", "broke", "stopped"))
		})

		It("orders by when the operation finished, newest first", func() {
			for _, id := range []string{"first", "second", "third"} {
				Expect(store.Create(&distributed.GalleryOperationRecord{
					ID: id, GalleryElementName: id, OpType: "model_install", Status: "pending",
				})).To(Succeed())
			}

			// Finish them out of creation order: the record reports what
			// finished when, not what was queued when.
			finish("second", "completed")
			finish("first", "completed")
			finish("third", "completed")

			ops, err := store.ListTerminal(0)
			Expect(err).ToNot(HaveOccurred())
			Expect(ops).To(HaveLen(3))
			Expect(ops[0].ID).To(Equal("third"))
			Expect(ops[1].ID).To(Equal("first"))
			Expect(ops[2].ID).To(Equal("second"))
		})

		It("caps the result at the limit, keeping the newest", func() {
			for _, id := range []string{"a", "b", "c"} {
				Expect(store.Create(&distributed.GalleryOperationRecord{
					ID: id, GalleryElementName: id, OpType: "model_install", Status: "pending",
				})).To(Succeed())
				finish(id, "completed")
			}

			ops, err := store.ListTerminal(2)
			Expect(err).ToNot(HaveOccurred())
			Expect(ops).To(HaveLen(2))
			Expect(ops[0].ID).To(Equal("c"))
			Expect(ops[1].ID).To(Equal("b"))
		})

		It("returns every row when the limit is zero or negative", func() {
			for _, id := range []string{"a", "b", "c"} {
				Expect(store.Create(&distributed.GalleryOperationRecord{
					ID: id, GalleryElementName: id, OpType: "model_install", Status: "pending",
				})).To(Succeed())
				finish(id, "completed")
			}

			ops, err := store.ListTerminal(-1)
			Expect(err).ToNot(HaveOccurred())
			Expect(ops).To(HaveLen(3))
		})

		It("carries the columns the Activity record is built from", func() {
			Expect(store.Create(&distributed.GalleryOperationRecord{
				ID: "op-1", GalleryElementName: "localai@qwen3-4b", OpType: "model_delete", Status: "pending",
			})).To(Succeed())
			Expect(store.UpsertCacheKey("op-1", "localai@qwen3-4b", false)).To(Succeed())
			Expect(store.UpdateStatus("op-1", "failed", "no space left on device")).To(Succeed())

			ops, err := store.ListTerminal(0)
			Expect(err).ToNot(HaveOccurred())
			Expect(ops).To(HaveLen(1))
			Expect(ops[0].CacheKey).To(Equal("localai@qwen3-4b"))
			Expect(ops[0].OpType).To(Equal("model_delete"))
			Expect(ops[0].Error).To(Equal("no space left on device"))
			Expect(ops[0].CreatedAt).ToNot(BeZero())
			Expect(ops[0].UpdatedAt).ToNot(BeZero())
		})
	})

	Describe("ClearTerminal", func() {
		It("removes finished operations and leaves in-flight ones alone", func() {
			Expect(store.Create(&distributed.GalleryOperationRecord{
				ID: "running", GalleryElementName: "busy", OpType: "model_install", Status: "downloading",
			})).To(Succeed())
			Expect(store.Create(&distributed.GalleryOperationRecord{
				ID: "queued", GalleryElementName: "waiting", OpType: "model_install", Status: "pending",
			})).To(Succeed())
			Expect(store.Create(&distributed.GalleryOperationRecord{
				ID: "done", GalleryElementName: "installed", OpType: "model_install", Status: "pending",
			})).To(Succeed())
			finish("done", "completed")

			Expect(store.ClearTerminal()).To(Succeed())

			terminal, err := store.ListTerminal(0)
			Expect(err).ToNot(HaveOccurred())
			Expect(terminal).To(BeEmpty())

			active, err := store.ListActive()
			Expect(err).ToNot(HaveOccurred())
			ids := []string{}
			for _, op := range active {
				ids = append(ids, op.ID)
			}
			Expect(ids).To(ConsistOf("running", "queued"),
				"clearing the record must never drop an operation still in flight")
		})
	})
})
