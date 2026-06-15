package facerecognition_test

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/mudler/LocalAI/core/services/facerecognition"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"

	grpclib "google.golang.org/grpc"
)

const dim = 4 // tiny test-friendly embedding dimension

func TestRegisterIdentifyForget(t *testing.T) {
	t.Parallel()

	reg, fake := newTestRegistry(t)
	ctx := t.Context()

	alice := []float32{1, 0, 0, 0}
	bob := []float32{0, 1, 0, 0}

	aliceMeta, err := reg.Register(ctx, alice, facerecognition.Metadata{Name: "Alice"})
	if err != nil {
		t.Fatalf("Register Alice: %v", err)
	}
	if aliceMeta.ID == "" {
		t.Fatalf("Register returned empty ID")
	}
	if aliceMeta.RegisteredAt.IsZero() {
		t.Fatalf("Register did not populate RegisteredAt")
	}

	bobMeta, err := reg.Register(ctx, bob, facerecognition.Metadata{Name: "Bob"})
	if err != nil {
		t.Fatalf("Register Bob: %v", err)
	}
	if bobMeta.ID == aliceMeta.ID {
		t.Fatalf("IDs should be distinct, got %q twice", bobMeta.ID)
	}
	aliceID := aliceMeta.ID
	if got, want := fake.len(), 2; got != want {
		t.Fatalf("fake store has %d entries, want %d", got, want)
	}

	// Identify an Alice-like probe — she should win.
	matches, err := reg.Identify(ctx, []float32{0.99, 0.01, 0, 0}, 2)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no matches returned")
	}
	if matches[0].Metadata.Name != "Alice" {
		t.Fatalf("top match name = %q, want Alice", matches[0].Metadata.Name)
	}
	if matches[0].ID != aliceID {
		t.Fatalf("top match ID = %q, want %q", matches[0].ID, aliceID)
	}
	// Sorted ascending by distance.
	for i := 1; i < len(matches); i++ {
		if matches[i].Distance < matches[i-1].Distance {
			t.Fatalf("matches not sorted by distance: %v", matches)
		}
	}

	// Forget Alice → she's gone, Bob remains.
	if err := reg.Forget(ctx, aliceID); err != nil {
		t.Fatalf("Forget Alice: %v", err)
	}
	if got, want := fake.len(), 1; got != want {
		t.Fatalf("after Forget, store has %d entries, want %d", got, want)
	}

	// Forget unknown ID → ErrNotFound (checkable via errors.Is).
	if err := reg.Forget(ctx, "nonexistent"); !errors.Is(err, facerecognition.ErrNotFound) {
		t.Fatalf("Forget unknown: err = %v, want ErrNotFound", err)
	}
}

func TestRegisterRejectsBadEmbedding(t *testing.T) {
	t.Parallel()

	reg, _ := newTestRegistry(t)
	ctx := t.Context()

	tests := []struct {
		name    string
		embed   []float32
		wantErr error
	}{
		{"empty", []float32{}, facerecognition.ErrEmptyEmbedding},
		{"wrong_dim", []float32{1, 2}, facerecognition.ErrDimensionMismatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := reg.Register(ctx, tc.embed, facerecognition.Metadata{Name: "x"})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want wrapping %v", err, tc.wantErr)
			}
		})
	}
}

func TestConcurrent(t *testing.T) {
	t.Parallel()

	reg, _ := newTestRegistry(t)
	ctx := t.Context()

	done := make(chan struct{})
	for i := range 32 {
		go func(i int) {
			embed := []float32{float32(i % 4), float32((i + 1) % 4), 0, 1}
			meta, err := reg.Register(ctx, embed, facerecognition.Metadata{Name: "n"})
			if err == nil {
				_, _ = reg.Identify(ctx, embed, 3)
				_ = reg.Forget(ctx, meta.ID)
			}
			done <- struct{}{}
		}(i)
	}
	for range 32 {
		<-done
	}
}

// ─── fake gRPC backend ───────────────────────────────────────────────

func newTestRegistry(t *testing.T) (facerecognition.Registry, *fakeBackend) {
	t.Helper()
	fake := &fakeBackend{}
	resolver := func(_ context.Context, _ string) (grpc.Backend, error) {
		return fake, nil
	}
	return facerecognition.NewStoreRegistry(resolver, "test-store", dim), fake
}

// fakeBackend implements just enough of grpc.Backend for the store
// helpers. All other methods panic so any accidental dependency is
// visible in tests.
type fakeBackend struct {
	grpc.Backend // embed to inherit no-op default method set via panic

	mu   sync.Mutex
	keys [][]float32
	vals [][]byte
}

func (f *fakeBackend) len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.keys)
}

func (f *fakeBackend) StoresSet(_ context.Context, in *pb.StoresSetOptions, _ ...grpclib.CallOption) (*pb.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, k := range in.Keys {
		f.keys = append(f.keys, append([]float32(nil), k.Floats...))
		f.vals = append(f.vals, append([]byte(nil), in.Values[i].Bytes...))
	}
	return &pb.Result{Success: true}, nil
}

func (f *fakeBackend) StoresDelete(_ context.Context, in *pb.StoresDeleteOptions, _ ...grpclib.CallOption) (*pb.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range in.Keys {
		idx := f.findKey(k.Floats)
		if idx < 0 {
			continue
		}
		f.keys = append(f.keys[:idx], f.keys[idx+1:]...)
		f.vals = append(f.vals[:idx], f.vals[idx+1:]...)
	}
	return &pb.Result{Success: true}, nil
}

func (f *fakeBackend) StoresFind(_ context.Context, in *pb.StoresFindOptions, _ ...grpclib.CallOption) (*pb.StoresFindResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	type scored struct {
		key []float32
		val []byte
		sim float32
	}
	results := make([]scored, 0, len(f.keys))
	for i, k := range f.keys {
		results = append(results, scored{k, f.vals[i], cosine(k, in.Key.Floats)})
	}
	// Sort descending by similarity.
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].sim > results[i].sim {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	top := int(in.TopK)
	if top <= 0 || top > len(results) {
		top = len(results)
	}
	out := &pb.StoresFindResult{}
	for _, r := range results[:top] {
		out.Keys = append(out.Keys, &pb.StoresKey{Floats: r.key})
		out.Values = append(out.Values, &pb.StoresValue{Bytes: r.val})
		out.Similarities = append(out.Similarities, r.sim)
	}
	return out, nil
}

func (f *fakeBackend) findKey(target []float32) int {
	for i, k := range f.keys {
		if equalFloats(k, target) {
			return i
		}
	}
	return -1
}

func equalFloats(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cosine(a, b []float32) float32 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}
