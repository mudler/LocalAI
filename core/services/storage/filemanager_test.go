package storage

import (
	"bytes"
	"context"
	"io"
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

	Describe("different keys", func() {
		It("does not serialize downloads for different keys", func() {
			store := newMockObjectStore(50 * time.Millisecond)
			store.data["key-a"] = []byte("content-a")
			store.data["key-b"] = []byte("content-b")

			cacheDir := GinkgoT().TempDir()
			fm, err := NewFileManager(store, cacheDir)
			Expect(err).ToNot(HaveOccurred())

			var wg sync.WaitGroup
			wg.Add(2)

			var errA, errB error
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				_, errA = fm.Download(context.Background(), "key-a")
			}()
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				_, errB = fm.Download(context.Background(), "key-b")
			}()
			wg.Wait()

			Expect(errA).ToNot(HaveOccurred())
			Expect(errB).ToNot(HaveOccurred())
			Expect(store.getCalls.Load()).To(BeNumerically("==", 2))
		})
	})
})
