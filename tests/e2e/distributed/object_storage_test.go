package distributed_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/storage"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Phase 5: Object Storage & File Manager", Label("Distributed"), func() {
	var (
		ctx      context.Context
		store    *storage.FilesystemStore
		fileMgr  *storage.FileManager
		tmpDir   string
		cacheDir string
	)

	BeforeEach(func() {
		ctx = context.Background()

		var err error
		tmpDir = GinkgoT().TempDir()
		storeDir := filepath.Join(tmpDir, "objectstore")
		cacheDir = filepath.Join(tmpDir, "cache")

		store, err = storage.NewFilesystemStore(storeDir)
		Expect(err).ToNot(HaveOccurred())

		fileMgr, err = storage.NewFileManager(store, cacheDir)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("FileManager upload and download", func() {
		It("should upload a local file to object storage", func() {
			// Create a local file
			localFile := filepath.Join(tmpDir, "test-model.gguf")
			Expect(os.WriteFile(localFile, []byte("fake model data"), 0644)).To(Succeed())

			// Upload
			key := storage.ModelKey("test-model.gguf")
			Expect(fileMgr.Upload(ctx, key, localFile)).To(Succeed())

			// Verify it exists in object storage
			exists, err := fileMgr.Exists(ctx, key)
			Expect(err).ToNot(HaveOccurred())
			Expect(exists).To(BeTrue())
		})

		It("should download a file from object storage with local caching", func() {
			// Put a file directly in the store
			key := storage.ModelKey("cached-model.gguf")
			Expect(store.Put(ctx, key, bytes.NewReader([]byte("model bytes")))).To(Succeed())

			// Download via file manager
			localPath, err := fileMgr.Download(ctx, key)
			Expect(err).ToNot(HaveOccurred())
			Expect(localPath).ToNot(BeEmpty())

			// Verify file exists locally
			data, err := os.ReadFile(localPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(data)).To(Equal("model bytes"))

			// Verify it's cached
			Expect(fileMgr.CacheExists(key)).To(BeTrue())
		})

		It("should serve from cache on second download", func() {
			key := storage.ModelKey("cache-hit.gguf")
			Expect(store.Put(ctx, key, bytes.NewReader([]byte("original")))).To(Succeed())

			// First download
			path1, err := fileMgr.Download(ctx, key)
			Expect(err).ToNot(HaveOccurred())

			// Modify the object store (simulate update) — cache should still return old
			Expect(store.Put(ctx, key, bytes.NewReader([]byte("updated")))).To(Succeed())

			// Second download — should return cached version
			path2, err := fileMgr.Download(ctx, key)
			Expect(err).ToNot(HaveOccurred())
			Expect(path2).To(Equal(path1))

			data, _ := os.ReadFile(path2)
			Expect(string(data)).To(Equal("original")) // cached, not updated
		})

		It("should evict from cache", func() {
			key := storage.ModelKey("evictable.gguf")
			Expect(store.Put(ctx, key, bytes.NewReader([]byte("data")))).To(Succeed())

			fileMgr.Download(ctx, key)
			Expect(fileMgr.CacheExists(key)).To(BeTrue())

			Expect(fileMgr.EvictCache(key)).To(Succeed())
			Expect(fileMgr.CacheExists(key)).To(BeFalse())

			// Still exists in object store
			exists, _ := fileMgr.Exists(ctx, key)
			Expect(exists).To(BeTrue())
		})

		It("should delete from both cache and object store", func() {
			key := storage.ModelKey("deletable.gguf")
			Expect(store.Put(ctx, key, bytes.NewReader([]byte("data")))).To(Succeed())
			fileMgr.Download(ctx, key)

			Expect(fileMgr.Delete(ctx, key)).To(Succeed())

			Expect(fileMgr.CacheExists(key)).To(BeFalse())
			exists, _ := fileMgr.Exists(ctx, key)
			Expect(exists).To(BeFalse())
		})
	})

	Context("Namespace helpers", func() {
		It("should generate correct model keys", func() {
			Expect(storage.ModelKey("llama3.gguf")).To(Equal("models/llama3.gguf"))
		})

		It("should generate correct user asset keys", func() {
			Expect(storage.UserAssetKey("user1", "image.png")).To(Equal("users/user1/assets/image.png"))
		})

		It("should generate correct user output keys", func() {
			Expect(storage.UserOutputKey("user1", "result.txt")).To(Equal("users/user1/outputs/result.txt"))
		})

		It("should generate correct fine-tune dataset keys", func() {
			Expect(storage.FineTuneDatasetKey("job1", "train.json")).To(Equal("finetune/datasets/job1/train.json"))
		})

		It("should generate correct fine-tune checkpoint keys", func() {
			Expect(storage.FineTuneCheckpointKey("job1", "checkpoint-100")).To(Equal("finetune/job1/checkpoints/checkpoint-100"))
		})

		It("should generate correct skill keys", func() {
			Expect(storage.SkillKey("user1", "search", "SKILL.md")).To(Equal("skills/user1/search/SKILL.md"))
			Expect(storage.SkillKey("", "search", "SKILL.md")).To(Equal("skills/global/search/SKILL.md"))
		})
	})

	Context("Store user assets in object storage", func() {
		It("should store and retrieve user assets", func() {
			key := storage.UserAssetKey("user1", "document.pdf")
			content := []byte("PDF content here")

			Expect(store.Put(ctx, key, bytes.NewReader(content))).To(Succeed())

			r, err := store.Get(ctx, key)
			Expect(err).ToNot(HaveOccurred())
			data, _ := io.ReadAll(r)
			r.Close()
			Expect(data).To(Equal(content))
		})

		It("should list user assets", func() {
			store.Put(ctx, storage.UserAssetKey("user1", "a.png"), bytes.NewReader([]byte("a")))
			store.Put(ctx, storage.UserAssetKey("user1", "b.pdf"), bytes.NewReader([]byte("b")))
			store.Put(ctx, storage.UserAssetKey("user2", "c.txt"), bytes.NewReader([]byte("c")))

			u1Assets, err := store.List(ctx, "users/user1/assets")
			Expect(err).ToNot(HaveOccurred())
			Expect(u1Assets).To(HaveLen(2))
		})
	})

	Context("Store fine-tune data in object storage", func() {
		It("should store and retrieve fine-tune datasets", func() {
			key := storage.FineTuneDatasetKey("job-123", "train.jsonl")
			content := []byte(`{"text": "training data"}`)

			Expect(store.Put(ctx, key, bytes.NewReader(content))).To(Succeed())

			exists, _ := store.Exists(ctx, key)
			Expect(exists).To(BeTrue())
		})

		It("should store fine-tune checkpoints", func() {
			key := storage.FineTuneCheckpointKey("job-123", "checkpoint-500")
			Expect(store.Put(ctx, key, bytes.NewReader([]byte("checkpoint data")))).To(Succeed())

			keys, err := store.List(ctx, "finetune/job-123/checkpoints")
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(1))
		})
	})

	Context("FileManager without store (single-node mode)", func() {
		It("should be a no-op when store is nil", func() {
			fm, err := storage.NewFileManager(nil, "")
			Expect(err).ToNot(HaveOccurred())

			Expect(fm.IsConfigured()).To(BeFalse())
			Expect(fm.Upload(ctx, "key", "/nonexistent")).To(Succeed()) // no-op
			Expect(fm.Delete(ctx, "key")).To(Succeed())                 // no-op

			exists, _ := fm.Exists(ctx, "key")
			Expect(exists).To(BeFalse())
		})
	})
})
