package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mockObjectStore is a test double that counts Get calls and adds a small
// delay to simulate real I/O. This lets us verify singleflight deduplication.
type mockObjectStore struct {
	getCalls atomic.Int64
	delay    time.Duration
	data     map[string][]byte
}

func newMockObjectStore(delay time.Duration) *mockObjectStore {
	return &mockObjectStore{
		delay: delay,
		data:  make(map[string][]byte),
	}
}

func (m *mockObjectStore) Put(_ context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.data[key] = b
	return nil
}

func (m *mockObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.getCalls.Add(1)
	time.Sleep(m.delay)
	b, ok := m.data[key]
	if !ok {
		return nil, io.EOF
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *mockObjectStore) Head(_ context.Context, key string) (*ObjectMeta, error) {
	b, ok := m.data[key]
	if !ok {
		return nil, io.EOF
	}
	return &ObjectMeta{
		Key:  key,
		Size: int64(len(b)),
	}, nil
}

func (m *mockObjectStore) Exists(_ context.Context, key string) (bool, error) {
	_, ok := m.data[key]
	return ok, nil
}

func (m *mockObjectStore) Delete(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func (m *mockObjectStore) List(_ context.Context, prefix string) ([]string, error) {
	var keys []string
	for k := range m.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

var _ = Describe("FileManager", func() {
	Describe("Upload", func() {
		It("sends file data to the object store", func() {
			store := newMockObjectStore(0)
			cacheDir := GinkgoT().TempDir()
			fm, err := NewFileManager(store, cacheDir)
			Expect(err).ToNot(HaveOccurred())

			// Write a temp file to upload
			tmpFile := filepath.Join(GinkgoT().TempDir(), "upload-test.txt")
			Expect(os.WriteFile(tmpFile, []byte("upload-content"), 0644)).To(Succeed())

			Expect(fm.Upload(context.Background(), "my/key", tmpFile)).To(Succeed())

			// Verify the mock store received the data
			Expect(store.data).To(HaveKey("my/key"))
			Expect(store.data["my/key"]).To(Equal([]byte("upload-content")))
		})

		It("returns error when local file does not exist", func() {
			store := newMockObjectStore(0)
			cacheDir := GinkgoT().TempDir()
			fm, err := NewFileManager(store, cacheDir)
			Expect(err).ToNot(HaveOccurred())

			err = fm.Upload(context.Background(), "key", "/nonexistent/path")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("Download cache hit", func() {
		It("serves from cache on second download without calling store again", func() {
			store := newMockObjectStore(0)
			store.data["cached-key"] = []byte("cached-content")

			cacheDir := GinkgoT().TempDir()
			fm, err := NewFileManager(store, cacheDir)
			Expect(err).ToNot(HaveOccurred())

			// First download populates cache
			path1, err := fm.Download(context.Background(), "cached-key")
			Expect(err).ToNot(HaveOccurred())
			Expect(path1).ToNot(BeEmpty())

			content, err := os.ReadFile(path1)
			Expect(err).ToNot(HaveOccurred())
			Expect(content).To(Equal([]byte("cached-content")))

			Expect(store.getCalls.Load()).To(BeNumerically("==", 1))

			// Second download should hit cache
			path2, err := fm.Download(context.Background(), "cached-key")
			Expect(err).ToNot(HaveOccurred())
			Expect(path2).To(Equal(path1))

			// store.Get should NOT have been called again
			Expect(store.getCalls.Load()).To(BeNumerically("==", 1))
		})
	})

	Describe("Delete", func() {
		It("removes from both local cache and remote store", func() {
			store := newMockObjectStore(0)
			store.data["del-key"] = []byte("delete-me")

			cacheDir := GinkgoT().TempDir()
			fm, err := NewFileManager(store, cacheDir)
			Expect(err).ToNot(HaveOccurred())

			// Download to populate cache
			localPath, err := fm.Download(context.Background(), "del-key")
			Expect(err).ToNot(HaveOccurred())
			Expect(localPath).ToNot(BeEmpty())

			// Verify cache file exists
			_, err = os.Stat(localPath)
			Expect(err).ToNot(HaveOccurred())

			// Delete
			Expect(fm.Delete(context.Background(), "del-key")).To(Succeed())

			// Local cache should be gone
			_, err = os.Stat(localPath)
			Expect(os.IsNotExist(err)).To(BeTrue())

			// Remote store should be gone
			Expect(store.data).ToNot(HaveKey("del-key"))
		})
	})

	Describe("nil store (single-node mode)", func() {
		var fm *FileManager

		BeforeEach(func() {
			cacheDir := GinkgoT().TempDir()
			var err error
			fm, err = NewFileManager(nil, cacheDir)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Upload is a no-op", func() {
			tmpFile := filepath.Join(GinkgoT().TempDir(), "nil-upload.txt")
			Expect(os.WriteFile(tmpFile, []byte("data"), 0644)).To(Succeed())

			err := fm.Upload(context.Background(), "key", tmpFile)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Download returns an error", func() {
			_, err := fm.Download(context.Background(), "key")
			Expect(err).To(HaveOccurred())
		})

		It("Delete is a no-op", func() {
			err := fm.Delete(context.Background(), "key")
			Expect(err).ToNot(HaveOccurred())
		})

		It("IsConfigured returns false", func() {
			Expect(fm.IsConfigured()).To(BeFalse())
		})
	})

	Describe("singleflight deduplication", func() {
		It("deduplicates concurrent downloads for the same key", func() {
			store := newMockObjectStore(50 * time.Millisecond)
			store.data["same-key"] = []byte("test-content")

			cacheDir := GinkgoT().TempDir()
			fm, err := NewFileManager(store, cacheDir)
			Expect(err).ToNot(HaveOccurred())

			const numGoroutines = 10
			var wg sync.WaitGroup
			errs := make([]error, numGoroutines)
			paths := make([]string, numGoroutines)

			for i := range numGoroutines {
				wg.Go(func() {
					defer GinkgoRecover()
					p, e := fm.Download(context.Background(), "same-key")
					paths[i] = p
					errs[i] = e
				})
			}
			wg.Wait()

			for i, e := range errs {
				Expect(e).ToNot(HaveOccurred(), "goroutine %d", i)
			}
			for i, p := range paths {
				Expect(p).To(Equal(paths[0]), "goroutine %d path differs", i)
			}
			Expect(store.getCalls.Load()).To(BeNumerically("==", 1))
		})
	})

	Describe("cachePath directory traversal prevention", func() {
		var fm *FileManager
		var cacheDir string

		BeforeEach(func() {
			cacheDir = GinkgoT().TempDir()
			var err error
			fm, err = NewFileManager(nil, cacheDir)
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects keys with .. that escape the cache directory", func() {
			_, err := fm.CachePath("../escape")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("escapes cache directory"))
		})

		It("rejects keys with nested .. traversal", func() {
			_, err := fm.CachePath("foo/../../bar")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("escapes cache directory"))
		})

		It("allows normal keys", func() {
			p, err := fm.CachePath("models/llama/weights.bin")
			Expect(err).ToNot(HaveOccurred())
			Expect(p).To(HavePrefix(cacheDir))
		})

		It("allows simple keys without slashes", func() {
			p, err := fm.CachePath("myfile.txt")
			Expect(err).ToNot(HaveOccurred())
			Expect(p).To(Equal(filepath.Join(cacheDir, "myfile.txt")))
		})

		It("CacheExists returns false for traversal keys", func() {
			Expect(fm.CacheExists("../etc/passwd")).To(BeFalse())
		})

		It("EvictCache returns error for traversal keys", func() {
			err := fm.EvictCache("../../etc/shadow")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("escapes cache directory"))
		})
	})

	Describe("different keys", func() {
		It("does not serialize downloads for different keys", func() {
			store := newMockObjectStore(50 * time.Millisecond)
			store.data["key-a"] = []byte("content-a")
			store.data["key-b"] = []byte("content-b")

			cacheDir := GinkgoT().TempDir()
			fm, err := NewFileManager(store, cacheDir)
			Expect(err).ToNot(HaveOccurred())

			var wg sync.WaitGroup
			var errA, errB error
			wg.Go(func() {
				defer GinkgoRecover()
				_, errA = fm.Download(context.Background(), "key-a")
			})
			wg.Go(func() {
				defer GinkgoRecover()
				_, errB = fm.Download(context.Background(), "key-b")
			})
			wg.Wait()

			Expect(errA).ToNot(HaveOccurred())
			Expect(errB).ToNot(HaveOccurred())
			Expect(store.getCalls.Load()).To(BeNumerically("==", 2))
		})
	})
})

var _ = Describe("A cached file with an expected sha256", func() {
	sum := func(s string) string {
		h := sha256.Sum256([]byte(s))
		return hex.EncodeToString(h[:])
	}

	It("downloads again a cached copy of an older version of the file", func() {
		store := newMockObjectStore(0)
		fm, err := NewFileManager(store, GinkgoT().TempDir())
		Expect(err).ToNot(HaveOccurred())
		key := ModelKey("tiny/tiny.gguf")
		store.data[key] = []byte("v1")
		_, err = fm.Download(context.Background(), key)
		Expect(err).ToNot(HaveOccurred())

		By("the frontend replacing the object, as an upgrade does")
		store.data[key] = []byte("v2")
		stale, err := fm.Download(context.Background(), key)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.ReadFile(stale)).To(Equal([]byte("v1")), "without a sha256 the cached copy is used as it is")

		p, err := fm.DownloadVerified(context.Background(), key, sum("v2"))
		Expect(err).ToNot(HaveOccurred())
		Expect(os.ReadFile(p)).To(Equal([]byte("v2")))
		Expect(store.getCalls.Load()).To(Equal(int64(2)))

		By("trusting the record of a copy that did not change")
		p, err = fm.DownloadVerified(context.Background(), key, sum("v2"))
		Expect(err).ToNot(HaveOccurred())
		Expect(os.ReadFile(p)).To(Equal([]byte("v2")))
		Expect(store.getCalls.Load()).To(Equal(int64(2)))
	})

	It("fails when the object itself has another sha256, and keeps no copy", func() {
		store := newMockObjectStore(0)
		dir := GinkgoT().TempDir()
		fm, err := NewFileManager(store, dir)
		Expect(err).ToNot(HaveOccurred())
		key := ModelKey("tiny/tiny.gguf")
		store.data[key] = []byte("v1")
		_, err = fm.DownloadVerified(context.Background(), key, sum("v2"))
		Expect(err).To(MatchError(ContainSubstring("expected sha256")))
		Expect(fm.CacheExists(key)).To(BeFalse())
	})

	It("uses a copy a worker verified itself without hashing it", func() {
		store := newMockObjectStore(0)
		dir := GinkgoT().TempDir()
		fm, err := NewFileManager(store, dir)
		Expect(err).ToNot(HaveOccurred())
		key := ModelKey("tiny/tiny.gguf")
		p, err := fm.CachePath(key)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.MkdirAll(filepath.Dir(p), 0750)).To(Succeed())
		Expect(os.WriteFile(p, []byte("v2"), 0600)).To(Succeed())
		Expect(RecordCacheSHA256(dir, key, sum("v2"))).To(Succeed())
		got, err := fm.DownloadVerified(context.Background(), key, sum("v2"))
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(p))
		Expect(store.getCalls.Load()).To(BeZero())
	})
})

var _ = Describe("RecordCacheSHA256", func() {
	It("records concurrent writers of one key without a collision or a leftover", func() {
		dir := GinkgoT().TempDir()
		key := "ephemeral/m/file.bin"
		path := filepath.Join(dir, filepath.FromSlash(key))
		Expect(os.MkdirAll(filepath.Dir(path), 0o750)).To(Succeed())
		Expect(os.WriteFile(path, []byte("data"), 0o600)).To(Succeed())

		var wg sync.WaitGroup
		errs := make(chan error, 32)
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sum := sha256.Sum256([]byte{byte(i)})
				errs <- RecordCacheSHA256(dir, key, hex.EncodeToString(sum[:]))
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			Expect(err).ToNot(HaveOccurred())
		}

		entries, err := os.ReadDir(filepath.Join(dir, verifiedDir, "ephemeral", "m"))
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).To(HaveLen(1), "only the record stays, no temporary file")
	})
})
