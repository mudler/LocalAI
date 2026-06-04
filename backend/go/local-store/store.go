package main

// LocalAI's in-process vector store, exposed as a gRPC backend. Keep
// the implementation here — NOT in a pkg/ library imported by the main
// LocalAI process. The whole point of the gRPC surface is that vector
// storage is a backend like any other (local-store, qdrant, pinecone,
// ...) and can be swapped without changing the routing/recognition
// code that consumes it.
//
// Storage is a sorted parallel-slice (keys [][]float32, values
// [][]byte). Set/Delete preserve the sort so Get can binary-search.
// Find scans linearly and uses a heap to keep the top-K — fine for
// the tens-to-thousands range. The "normalized fast path" (Find when
// every stored key has unit magnitude AND the query is normalized)
// skips the per-item magnitude calculation.
//
// Concurrency: base.SingleThread serialises gRPC calls so the
// non-thread-safe slice/heap manipulation here is sound.

import (
	"container/heap"
	"fmt"
	"math"
	"slices"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/store"
)

type Store struct {
	base.SingleThread

	keys   [][]float32
	values [][]byte

	// keysAreNormalized stays true until any non-unit-magnitude key
	// is added; once false, the magnitude-aware fallback path is
	// used by Find. Re-evaluated only at Set time, never again on
	// its own — a deletion of the offending key does NOT flip it
	// back to true (the bookkeeping cost would dominate the gain).
	keysAreNormalized bool

	// keyLen is the dimension of every stored key. -1 means "no
	// keys yet, dimension is open". Dimension mismatch on Set is
	// rejected so cosine similarity (which requires equal-length
	// vectors) doesn't silently mis-match.
	keyLen int
}

func NewStore() *Store {
	return &Store{
		keys:              make([][]float32, 0),
		values:            make([][]byte, 0),
		keysAreNormalized: true,
		keyLen:            -1,
	}
}

// Load is a no-op — local-store has no on-disk artefact. opts.Model is
// just a namespace identifier; isolation is already handled upstream
// (ModelLoader spawns a fresh local-store process per (backend,
// model) tuple, so each namespace is its own Store{} instance).
func (s *Store) Load(opts *pb.ModelOptions) error {
	_ = opts
	return nil
}

func (s *Store) StoresSet(opts *pb.StoresSetOptions) error {
	keys := store.UnwrapKeys(opts.Keys)
	values := store.UnwrapValues(opts.Values)
	if len(keys) == 0 {
		return fmt.Errorf("local-store: Set: no keys to add")
	}
	if len(keys) != len(values) {
		return fmt.Errorf("local-store: Set: len(keys) = %d, len(values) = %d", len(keys), len(values))
	}

	if s.keyLen == -1 {
		s.keyLen = len(keys[0])
	} else if len(keys[0]) != s.keyLen {
		return fmt.Errorf("local-store: Set: key length %d does not match existing %d", len(keys[0]), s.keyLen)
	}

	kvs := make([]incomingPair, len(keys))
	for i, k := range keys {
		if len(k) != s.keyLen {
			return fmt.Errorf("local-store: Set: key %d length %d does not match existing %d", i, len(k), s.keyLen)
		}
		if s.keysAreNormalized && !isNormalized(k) {
			s.keysAreNormalized = false
		}
		kvs[i] = incomingPair{key: k, value: values[i]}
	}

	slices.SortFunc(kvs, func(a, b incomingPair) int { return slices.Compare(a.key, b.key) })

	merged := mergeSortedPairs(s.keys, s.values, kvs)
	s.keys = merged.keys
	s.values = merged.values
	assert(slices.IsSortedFunc(s.keys, slices.Compare[[]float32]), "Set: s.keys not sorted post-merge")
	assert(len(s.keys) == len(s.values), "Set: keys/values length skew")
	return nil
}

func (s *Store) StoresDelete(opts *pb.StoresDeleteOptions) error {
	keys := store.UnwrapKeys(opts.Keys)
	if len(keys) == 0 {
		return fmt.Errorf("local-store: Delete: no keys to delete")
	}
	if s.keyLen != -1 {
		for i, k := range keys {
			if len(k) != s.keyLen {
				return fmt.Errorf("local-store: Delete: key %d length %d does not match existing %d", i, len(k), s.keyLen)
			}
		}
	}
	sortedKeys := append([][]float32(nil), keys...)
	slices.SortFunc(sortedKeys, slices.Compare[[]float32])

	mergedK := make([][]float32, 0, len(s.keys))
	mergedV := make([][]byte, 0, len(s.keys))
	tailK := s.keys
	tailV := s.values
	for _, k := range sortedKeys {
		j, ok := slices.BinarySearchFunc(tailK, k, slices.Compare[[]float32])
		if ok {
			mergedK = append(mergedK, tailK[:j]...)
			mergedV = append(mergedV, tailV[:j]...)
			tailK = tailK[j+1:]
			tailV = tailV[j+1:]
		}
	}
	mergedK = append(mergedK, tailK...)
	mergedV = append(mergedV, tailV...)
	s.keys = mergedK
	s.values = mergedV
	assert(slices.IsSortedFunc(s.keys, slices.Compare[[]float32]), "Delete: s.keys not sorted post-merge")
	assert(len(s.keys) == len(s.values), "Delete: keys/values length skew")
	return nil
}

// StoresGet fetches values for the given keys. Missing keys are
// omitted from the result rather than reported as an error — callers
// compare returned-key length against requested-key length to detect
// them. Returned slices are aligned.
func (s *Store) StoresGet(opts *pb.StoresGetOptions) (pb.StoresGetResult, error) {
	keys := store.UnwrapKeys(opts.Keys)
	if len(s.keys) == 0 {
		return pb.StoresGetResult{}, nil
	}
	if s.keyLen != -1 {
		for i, k := range keys {
			if len(k) != s.keyLen {
				return pb.StoresGetResult{}, fmt.Errorf("local-store: Get: key %d length %d does not match existing %d", i, len(k), s.keyLen)
			}
		}
	}
	sortedKeys := append([][]float32(nil), keys...)
	slices.SortFunc(sortedKeys, slices.Compare[[]float32])

	var foundKeys [][]float32
	var foundValues [][]byte
	tailK := s.keys
	tailV := s.values
	for _, k := range sortedKeys {
		j, ok := slices.BinarySearchFunc(tailK, k, slices.Compare[[]float32])
		if !ok {
			continue
		}
		foundKeys = append(foundKeys, tailK[j])
		foundValues = append(foundValues, tailV[j])
		tailK = tailK[j+1:]
		tailV = tailV[j+1:]
	}
	return pb.StoresGetResult{
		Keys:   store.WrapKeys(foundKeys),
		Values: store.WrapValues(foundValues),
	}, nil
}

// StoresFind returns the topK nearest stored entries by cosine
// similarity, ordered most-similar first. An empty store returns
// empty slices and no error.
func (s *Store) StoresFind(opts *pb.StoresFindOptions) (pb.StoresFindResult, error) {
	query := opts.Key.Floats
	topK := int(opts.TopK)
	if topK < 1 {
		return pb.StoresFindResult{}, fmt.Errorf("local-store: Find: topK = %d, must be >= 1", topK)
	}
	if len(s.keys) == 0 {
		return pb.StoresFindResult{}, nil
	}
	if len(query) != s.keyLen {
		return pb.StoresFindResult{}, fmt.Errorf("local-store: Find: query length %d does not match existing %d", len(query), s.keyLen)
	}

	var keys [][]float32
	var values [][]byte
	var sims []float32
	if s.keysAreNormalized && isNormalized(query) {
		keys, values, sims = s.findNormalized(query, topK)
	} else {
		keys, values, sims = s.findFallback(query, topK)
	}
	return pb.StoresFindResult{
		Keys:         store.WrapKeys(keys),
		Values:       store.WrapValues(values),
		Similarities: sims,
	}, nil
}

func (s *Store) findNormalized(query []float32, topK int) (keys [][]float32, values [][]byte, similarities []float32) {
	assert(s.keysAreNormalized, "findNormalized: s.keysAreNormalized is false")
	assert(isNormalized(query), "findNormalized: query is not unit-length")
	pq := make(priorityQueue, 0, topK)
	heap.Init(&pq)
	for i, k := range s.keys {
		var dot float32
		for j := range k {
			dot += query[j] * k[j]
		}
		assert(dot >= -1.01 && dot <= 1.01, fmt.Sprintf("findNormalized: dot %f out of [-1, 1] — keysAreNormalized invariant violated", dot))
		heap.Push(&pq, &priorityItem{similarity: dot, key: k, value: s.values[i]})
		if pq.Len() > topK {
			heap.Pop(&pq)
		}
	}
	return drainPQ(&pq)
}

func (s *Store) findFallback(query []float32, topK int) (keys [][]float32, values [][]byte, similarities []float32) {
	var qmag float64
	for _, v := range query {
		qmag += float64(v) * float64(v)
	}
	qmag = math.Sqrt(qmag)
	pq := make(priorityQueue, 0, topK)
	heap.Init(&pq)
	for i, k := range s.keys {
		var dot, kmag float64
		for j := range k {
			dot += float64(query[j]) * float64(k[j])
			kmag += float64(k[j]) * float64(k[j])
		}
		denom := qmag * math.Sqrt(kmag)
		var sim float32
		if denom > 0 {
			sim = float32(dot / denom)
		}
		heap.Push(&pq, &priorityItem{similarity: sim, key: k, value: s.values[i]})
		if pq.Len() > topK {
			heap.Pop(&pq)
		}
	}
	return drainPQ(&pq)
}

func isNormalized(k []float32) bool {
	var sum float64
	for _, v := range k {
		sum += float64(v) * float64(v)
	}
	mag := math.Sqrt(sum)
	return mag >= 0.99 && mag <= 1.01
}

type incomingPair struct {
	key   []float32
	value []byte
}

type pairs struct {
	keys   [][]float32
	values [][]byte
}

// mergeSortedPairs merges (existing, incoming) into a fresh sorted
// slice. Equal keys take the incoming value — Set is upsert.
func mergeSortedPairs(existingK [][]float32, existingV [][]byte, incoming []incomingPair) pairs {
	assert(slices.IsSortedFunc(existingK, slices.Compare[[]float32]), "mergeSortedPairs: existing not sorted")
	assert(slices.IsSortedFunc(incoming, func(a, b incomingPair) int { return slices.Compare(a.key, b.key) }), "mergeSortedPairs: incoming not sorted")
	l := len(existingK) + len(incoming)
	mk := make([][]float32, 0, l)
	mv := make([][]byte, 0, l)
	i, j := 0, 0
	for i < len(incoming) || j < len(existingK) {
		switch {
		case j >= len(existingK):
			mk = append(mk, incoming[i].key)
			mv = append(mv, incoming[i].value)
			i++
		case i >= len(incoming):
			mk = append(mk, existingK[j])
			mv = append(mv, existingV[j])
			j++
		default:
			c := slices.Compare(incoming[i].key, existingK[j])
			switch {
			case c < 0:
				mk = append(mk, incoming[i].key)
				mv = append(mv, incoming[i].value)
				i++
			case c > 0:
				mk = append(mk, existingK[j])
				mv = append(mv, existingV[j])
				j++
			default:
				mk = append(mk, incoming[i].key)
				mv = append(mv, incoming[i].value)
				i++
				j++
			}
		}
	}
	return pairs{keys: mk, values: mv}
}

type priorityItem struct {
	similarity float32
	key        []float32
	value      []byte
}

type priorityQueue []*priorityItem

func (pq priorityQueue) Len() int           { return len(pq) }
func (pq priorityQueue) Less(i, j int) bool { return pq[i].similarity < pq[j].similarity }
func (pq priorityQueue) Swap(i, j int)      { pq[i], pq[j] = pq[j], pq[i] }
func (pq *priorityQueue) Push(x any)        { *pq = append(*pq, x.(*priorityItem)) }
func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	*pq = old[0 : n-1]
	return item
}

func drainPQ(pq *priorityQueue) (keys [][]float32, values [][]byte, similarities []float32) {
	n := pq.Len()
	keys = make([][]float32, n)
	values = make([][]byte, n)
	similarities = make([]float32, n)
	for i := n - 1; i >= 0; i-- {
		item := heap.Pop(pq).(*priorityItem)
		keys[i] = item.key
		values[i] = item.value
		similarities[i] = item.similarity
	}
	return keys, values, similarities
}
