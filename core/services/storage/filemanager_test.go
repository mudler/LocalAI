package storage

import (
	"bytes"
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestFileManagerSingleflightDedup(t *testing.T) {
	store := newMockObjectStore(50 * time.Millisecond)
	store.data["same-key"] = []byte("test-content")

	cacheDir := t.TempDir()
	fm, err := NewFileManager(store, cacheDir)
	if err != nil {
		t.Fatal(err)
	}

	const numGoroutines = 10
	var wg sync.WaitGroup
	errs := make([]error, numGoroutines)
	paths := make([]string, numGoroutines)

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			p, e := fm.Download(context.Background(), "same-key")
			paths[idx] = p
			errs[idx] = e
		}(i)
	}
	wg.Wait()

	// All goroutines should succeed
	for i, e := range errs {
		if e != nil {
			t.Fatalf("goroutine %d: unexpected error: %v", i, e)
		}
	}

	// All goroutines should get the same path
	for i, p := range paths {
		if p != paths[0] {
			t.Fatalf("goroutine %d: path %q differs from goroutine 0 path %q", i, p, paths[0])
		}
	}

	// singleflight should have deduplicated to exactly 1 Get call
	got := store.getCalls.Load()
	if got != 1 {
		t.Fatalf("expected exactly 1 Get call, got %d", got)
	}
}

func TestFileManagerDifferentKeysNotSerialized(t *testing.T) {
	store := newMockObjectStore(50 * time.Millisecond)
	store.data["key-a"] = []byte("content-a")
	store.data["key-b"] = []byte("content-b")

	cacheDir := t.TempDir()
	fm, err := NewFileManager(store, cacheDir)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	var errA, errB error
	go func() {
		defer wg.Done()
		_, errA = fm.Download(context.Background(), "key-a")
	}()
	go func() {
		defer wg.Done()
		_, errB = fm.Download(context.Background(), "key-b")
	}()
	wg.Wait()

	if errA != nil {
		t.Fatalf("key-a: unexpected error: %v", errA)
	}
	if errB != nil {
		t.Fatalf("key-b: unexpected error: %v", errB)
	}

	// Both keys should have triggered a separate Get call
	got := store.getCalls.Load()
	if got != 2 {
		t.Fatalf("expected 2 Get calls for different keys, got %d", got)
	}
}
