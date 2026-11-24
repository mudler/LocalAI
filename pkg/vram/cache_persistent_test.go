package vram

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type countingSizeResolver struct {
	size  int64
	err   error
	calls int
}

type countingGGUFReader struct {
	meta  *GGUFMeta
	err   error
	calls int
}

type blockingSizeResolver struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingSizeResolver) ContentLength(context.Context, string) (int64, error) {
	close(r.started)
	<-r.release
	return 42, nil
}

func (r *countingGGUFReader) ReadMetadata(context.Context, string) (*GGUFMeta, error) {
	r.calls++
	return r.meta, r.err
}

func (r *countingSizeResolver) ContentLength(context.Context, string) (int64, error) {
	r.calls++
	return r.size, r.err
}

var _ = Describe("persistent VRAM metadata cache", func() {
	AfterEach(func() {
		ConfigurePersistentCache("", 0)
		SetGalleryGenerationFunc(nil)
	})

	It("reuses a successful size probe after the in-memory cache is replaced", func() {
		cacheDir := filepath.Join(GinkgoT().TempDir(), "vram")
		firstSource := &countingSizeResolver{size: 42}
		first := newCachedSizeResolver(firstSource, cacheDir, time.Hour)

		size, err := first.ContentLength(context.Background(), "https://example.com/model.gguf")
		Expect(err).NotTo(HaveOccurred())
		Expect(size).To(Equal(int64(42)))
		Expect(firstSource.calls).To(Equal(1))

		secondSource := &countingSizeResolver{err: errors.New("unexpected remote probe")}
		second := newCachedSizeResolver(secondSource, cacheDir, time.Hour)
		size, err = second.ContentLength(context.Background(), "https://example.com/model.gguf")

		Expect(err).NotTo(HaveOccurred())
		Expect(size).To(Equal(int64(42)))
		Expect(secondSource.calls).To(BeZero())
	})

	It("reuses successful GGUF metadata after the in-memory cache is replaced", func() {
		cacheDir := filepath.Join(GinkgoT().TempDir(), "vram")
		want := &GGUFMeta{BlockCount: 32, EmbeddingLength: 4096, HeadCount: 32, HeadCountKV: 8, MaximumContextLength: 131072}
		firstSource := &countingGGUFReader{meta: want}
		first := newCachedGGUFReader(firstSource, cacheDir, time.Hour)

		meta, err := first.ReadMetadata(context.Background(), "https://example.com/model.gguf")
		Expect(err).NotTo(HaveOccurred())
		Expect(meta).To(Equal(want))
		Expect(firstSource.calls).To(Equal(1))

		secondSource := &countingGGUFReader{err: errors.New("unexpected remote probe")}
		second := newCachedGGUFReader(secondSource, cacheDir, time.Hour)
		meta, err = second.ReadMetadata(context.Background(), "https://example.com/model.gguf")

		Expect(err).NotTo(HaveOccurred())
		Expect(meta).To(Equal(want))
		Expect(secondSource.calls).To(BeZero())
	})

	It("configures the default caches used by model estimates", func() {
		cacheDir := filepath.Join(GinkgoT().TempDir(), "vram")
		ConfigurePersistentCache(cacheDir, time.Hour)

		Expect(defaultCachedSizeResolver.diskDir).To(Equal(cacheDir))
		Expect(defaultCachedGGUFReader.diskDir).To(Equal(cacheDir))
		Expect(defaultCachedSizeResolver.diskTTL).To(Equal(time.Hour))
		Expect(defaultCachedGGUFReader.diskTTL).To(Equal(time.Hour))
		Expect(defaultCachedSizeResolver.diskGuard).To(BeIdenticalTo(defaultCachedGGUFReader.diskGuard))
	})

	It("removes expired VRAM entries when the persistent cache is configured", func() {
		cacheDir := GinkgoT().TempDir()
		stale := filepath.Join(cacheDir, "size-stale.json")
		abandoned := filepath.Join(cacheDir, ".vram-abandoned.tmp")
		unrelated := filepath.Join(cacheDir, "keep.txt")
		Expect(os.WriteFile(stale, []byte("{}"), 0o600)).To(Succeed())
		Expect(os.WriteFile(abandoned, []byte("partial"), 0o600)).To(Succeed())
		Expect(os.WriteFile(unrelated, []byte("keep"), 0o600)).To(Succeed())
		old := time.Now().Add(-2 * time.Hour)
		Expect(os.Chtimes(stale, old, old)).To(Succeed())

		ConfigurePersistentCache(cacheDir, time.Hour)

		Expect(stale).NotTo(BeAnExistingFile())
		Expect(abandoned).NotTo(BeAnExistingFile())
		Expect(unrelated).To(BeAnExistingFile())
	})

	It("does not reuse a persistent entry after the gallery generation changes", func() {
		var generation uint64 = 1
		SetGalleryGenerationFunc(func() uint64 { return generation })
		cacheDir := GinkgoT().TempDir()
		first := newCachedSizeResolver(&countingSizeResolver{size: 42}, cacheDir, time.Hour)
		_, err := first.ContentLength(context.Background(), "https://example.com/model.gguf")
		Expect(err).NotTo(HaveOccurred())

		freshSource := &countingSizeResolver{size: 84}
		second := newCachedSizeResolver(freshSource, cacheDir, time.Hour)
		size, err := second.ContentLength(context.Background(), "https://example.com/model.gguf")
		Expect(err).NotTo(HaveOccurred())
		Expect(size).To(Equal(int64(42)))

		generation = 2
		size, err = second.ContentLength(context.Background(), "https://example.com/model.gguf")

		Expect(err).NotTo(HaveOccurred())
		Expect(size).To(Equal(int64(84)))
		Expect(freshSource.calls).To(Equal(1))
	})

	It("does not persist probes for local model files", func() {
		cacheDir := GinkgoT().TempDir()
		first := newCachedSizeResolver(&countingSizeResolver{size: 42}, cacheDir, time.Hour)
		_, err := first.ContentLength(context.Background(), "file:///models/model.gguf")
		Expect(err).NotTo(HaveOccurred())

		freshSource := &countingSizeResolver{size: 84}
		second := newCachedSizeResolver(freshSource, cacheDir, time.Hour)
		size, err := second.ContentLength(context.Background(), "file:///models/model.gguf")

		Expect(err).NotTo(HaveOccurred())
		Expect(size).To(Equal(int64(84)))
		Expect(freshSource.calls).To(Equal(1))
	})

	It("falls back to the remote probe for an invalid persisted size", func() {
		cacheDir := GinkgoT().TempDir()
		resolver := newCachedSizeResolver(&countingSizeResolver{size: 42}, cacheDir, time.Hour)
		Expect(os.WriteFile(resolver.persistentPath("https://example.com/model.gguf"), []byte(`{"size":-1}`), 0o600)).To(Succeed())

		size, err := resolver.ContentLength(context.Background(), "https://example.com/model.gguf")

		Expect(err).NotTo(HaveOccurred())
		Expect(size).To(Equal(int64(42)))
	})

	It("falls back to the remote probe for empty persisted GGUF metadata", func() {
		cacheDir := GinkgoT().TempDir()
		want := &GGUFMeta{BlockCount: 32, EmbeddingLength: 4096, HeadCount: 32, HeadCountKV: 8}
		reader := newCachedGGUFReader(&countingGGUFReader{meta: want}, cacheDir, time.Hour)
		Expect(os.WriteFile(reader.persistentPath("https://example.com/model.gguf"), []byte(`{"meta":{}}`), 0o600)).To(Succeed())

		meta, err := reader.ReadMetadata(context.Background(), "https://example.com/model.gguf")

		Expect(err).NotTo(HaveOccurred())
		Expect(meta).To(Equal(want))
	})

	It("keeps the persistent cache within its entry limit", func() {
		cacheDir := GinkgoT().TempDir()
		for _, name := range []string{"size-a.json", "size-b.json", "gguf-c.json"} {
			Expect(os.WriteFile(filepath.Join(cacheDir, name), []byte("{}"), 0o600)).To(Succeed())
			time.Sleep(time.Millisecond)
		}

		prunePersistentEntries(cacheDir, time.Hour, 2)

		entries, err := os.ReadDir(cacheDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(2))
		Expect(filepath.Join(cacheDir, "size-a.json")).NotTo(BeAnExistingFile())
	})

	It("removes persistent entries when gallery data is invalidated", func() {
		cacheDir := GinkgoT().TempDir()
		ConfigurePersistentCache(cacheDir, time.Hour)
		stale := filepath.Join(cacheDir, "size-stale.json")
		Expect(os.WriteFile(stale, []byte(`{"version":1,"size":42}`), 0o600)).To(Succeed())

		InvalidatePersistentCache()

		Expect(stale).NotTo(BeAnExistingFile())
	})

	It("does not persist a probe that finishes after invalidation", func() {
		var generation uint64 = 1
		SetGalleryGenerationFunc(func() uint64 { return generation })
		cacheDir := GinkgoT().TempDir()
		guard := &persistentGenerationGuard{dir: cacheDir}
		source := &blockingSizeResolver{started: make(chan struct{}), release: make(chan struct{})}
		resolver := newCachedSizeResolverWithGuard(source, cacheDir, time.Hour, guard)
		done := make(chan error, 1)
		go func() {
			_, err := resolver.ContentLength(context.Background(), "https://example.com/model.gguf")
			done <- err
		}()
		Eventually(source.started).Should(BeClosed())

		generation = 2
		guard.invalidate(generation)
		close(source.release)

		Eventually(done).Should(Receive(BeNil()))
		Expect(resolver.persistentPath("https://example.com/model.gguf")).NotTo(BeAnExistingFile())
	})
})
