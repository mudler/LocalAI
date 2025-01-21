package main

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"container/heap"
	"context"
	"fmt"
	"math"
	"runtime"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	chromem "github.com/philippgille/chromem-go"

	"github.com/rs/zerolog/log"
)

type Store struct {
	base.SingleThread
	*chromem.DB
	*chromem.Collection
}

func NewStore() *Store {
	return &Store{}
}

func (s *Store) Load(opts *pb.ModelOptions) error {
	db := chromem.NewDB()
	collection, err := db.CreateCollection("all-documents", nil, nil)
	if err != nil {
		return err
	}
	s.DB = db
	s.Collection = collection
	return nil
}

// Sort the incoming kvs and merge them with the existing sorted kvs
func (s *Store) StoresSet(opts *pb.StoresSetOptions) error {
	if len(opts.Keys) == 0 {
		return fmt.Errorf("no keys to add")
	}

	if len(opts.Keys) != len(opts.Values) {
		return fmt.Errorf("len(keys) = %d, len(values) = %d", len(opts.Keys), len(opts.Values))
	}
	docs := []chromem.Document{}

	for i, k := range opts.Keys {
		docs = append(docs, chromem.Document{
			ID:      k.String(),
			Content: opts.Values[i].String(),
		})
	}

	return s.Collection.AddDocuments(context.Background(), docs, runtime.NumCPU())
}

func (s *Store) StoresReset(opts *pb.StoresResetOptions) error {
	err := s.DB.DeleteCollection("all-documents")
	if err != nil {
		return err
	}
	s.Collection, err = s.CreateCollection("all-documents", nil, nil)
	return err
}

func (s *Store) StoresGet(opts *pb.StoresGetOptions) (pb.StoresGetResult, error) {
	pbKeys := make([]*pb.StoresKey, 0, len(opts.Keys))
	pbValues := make([]*pb.StoresValue, 0, len(opts.Keys))
	ks := sortIntoKeySlicese(opts.Keys)

	if len(s.keys) == 0 {
		log.Debug().Msgf("Get: No keys in store")
	}

	if s.keyLen == -1 {
		s.keyLen = len(opts.Keys[0].Floats)
	} else {
		if len(opts.Keys[0].Floats) != s.keyLen {
			return pb.StoresGetResult{}, fmt.Errorf("Try to get a key with length %d when existing length is %d", len(opts.Keys[0].Floats), s.keyLen)
		}
	}

	tail_k := s.keys
	tail_v := s.values
	for i, k := range ks {
		j, found := findInSortedSlice(tail_k, k)

		if found {
			pbKeys = append(pbKeys, &pb.StoresKey{
				Floats: k,
			})
			pbValues = append(pbValues, &pb.StoresValue{
				Bytes: tail_v[j],
			})

			tail_k = tail_k[j+1:]
			tail_v = tail_v[j+1:]
		} else {
			assert(!hasKey(s.keys, k), fmt.Sprintf("Key exists, but was not found: i=%d, %v", i, k))
		}
	}

	if len(pbKeys) != len(opts.Keys) {
		log.Debug().Msgf("Get: Some keys not found: len(pbKeys) = %d, len(opts.Keys) = %d, len(s.Keys) = %d", len(pbKeys), len(opts.Keys), len(s.keys))
	}

	return pb.StoresGetResult{
		Keys:   pbKeys,
		Values: pbValues,
	}, nil
}

func isNormalized(k []float32) bool {
	var sum float32
	for _, v := range k {
		sum += v
	}

	return sum == 1.0
}

// TODO: This we could replace with handwritten SIMD code
func normalizedCosineSimilarity(k1, k2 []float32) float32 {
	assert(len(k1) == len(k2), fmt.Sprintf("normalizedCosineSimilarity: len(k1) = %d, len(k2) = %d", len(k1), len(k2)))

	var dot float32
	for i := 0; i < len(k1); i++ {
		dot += k1[i] * k2[i]
	}

	assert(dot >= -1 && dot <= 1, fmt.Sprintf("dot = %f", dot))

	// 2.0 * (1.0 - dot) would be the Euclidean distance
	return dot
}

type PriorityItem struct {
	Similarity float32
	Key        []float32
	Value      []byte
}

type PriorityQueue []*PriorityItem

func (pq PriorityQueue) Len() int { return len(pq) }

func (pq PriorityQueue) Less(i, j int) bool {
	// Inverted because the most similar should be at the top
	return pq[i].Similarity < pq[j].Similarity
}

func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
}

func (pq *PriorityQueue) Push(x any) {
	item := x.(*PriorityItem)
	*pq = append(*pq, item)
}

func (pq *PriorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	*pq = old[0 : n-1]
	return item
}

func (s *Store) StoresFindNormalized(opts *pb.StoresFindOptions) (pb.StoresFindResult, error) {
	tk := opts.Key.Floats
	top_ks := make(PriorityQueue, 0, int(opts.TopK))
	heap.Init(&top_ks)

	for i, k := range s.keys {
		sim := normalizedCosineSimilarity(tk, k)
		heap.Push(&top_ks, &PriorityItem{
			Similarity: sim,
			Key:        k,
			Value:      s.values[i],
		})

		if top_ks.Len() > int(opts.TopK) {
			heap.Pop(&top_ks)
		}
	}

	similarities := make([]float32, top_ks.Len())
	pbKeys := make([]*pb.StoresKey, top_ks.Len())
	pbValues := make([]*pb.StoresValue, top_ks.Len())

	for i := top_ks.Len() - 1; i >= 0; i-- {
		item := heap.Pop(&top_ks).(*PriorityItem)

		similarities[i] = item.Similarity
		pbKeys[i] = &pb.StoresKey{
			Floats: item.Key,
		}
		pbValues[i] = &pb.StoresValue{
			Bytes: item.Value,
		}
	}

	return pb.StoresFindResult{
		Keys:         pbKeys,
		Values:       pbValues,
		Similarities: similarities,
	}, nil
}

func cosineSimilarity(k1, k2 []float32, mag1 float64) float32 {
	assert(len(k1) == len(k2), fmt.Sprintf("cosineSimilarity: len(k1) = %d, len(k2) = %d", len(k1), len(k2)))

	var dot, mag2 float64
	for i := 0; i < len(k1); i++ {
		dot += float64(k1[i] * k2[i])
		mag2 += float64(k2[i] * k2[i])
	}

	sim := float32(dot / (mag1 * math.Sqrt(mag2)))

	assert(sim >= -1 && sim <= 1, fmt.Sprintf("sim = %f", sim))

	return sim
}

func (s *Store) StoresFindFallback(opts *pb.StoresFindOptions) (pb.StoresFindResult, error) {
	tk := opts.Key.Floats
	top_ks := make(PriorityQueue, 0, int(opts.TopK))
	heap.Init(&top_ks)

	var mag1 float64
	for _, v := range tk {
		mag1 += float64(v * v)
	}
	mag1 = math.Sqrt(mag1)

	for i, k := range s.keys {
		dist := cosineSimilarity(tk, k, mag1)
		heap.Push(&top_ks, &PriorityItem{
			Similarity: dist,
			Key:        k,
			Value:      s.values[i],
		})

		if top_ks.Len() > int(opts.TopK) {
			heap.Pop(&top_ks)
		}
	}

	similarities := make([]float32, top_ks.Len())
	pbKeys := make([]*pb.StoresKey, top_ks.Len())
	pbValues := make([]*pb.StoresValue, top_ks.Len())

	for i := top_ks.Len() - 1; i >= 0; i-- {
		item := heap.Pop(&top_ks).(*PriorityItem)

		similarities[i] = item.Similarity
		pbKeys[i] = &pb.StoresKey{
			Floats: item.Key,
		}
		pbValues[i] = &pb.StoresValue{
			Bytes: item.Value,
		}
	}

	return pb.StoresFindResult{
		Keys:         pbKeys,
		Values:       pbValues,
		Similarities: similarities,
	}, nil
}

func (s *Store) StoresFind(opts *pb.StoresFindOptions) (pb.StoresFindResult, error) {
	tk := opts.Key.Floats

	if len(tk) != s.keyLen {
		return pb.StoresFindResult{}, fmt.Errorf("Try to find key with length %d when existing length is %d", len(tk), s.keyLen)
	}

	if opts.TopK < 1 {
		return pb.StoresFindResult{}, fmt.Errorf("opts.TopK = %d, must be >= 1", opts.TopK)
	}

	if s.keyLen == -1 {
		s.keyLen = len(opts.Key.Floats)
	} else {
		if len(opts.Key.Floats) != s.keyLen {
			return pb.StoresFindResult{}, fmt.Errorf("Try to add key with length %d when existing length is %d", len(opts.Key.Floats), s.keyLen)
		}
	}

	if s.keysAreNormalized && isNormalized(tk) {
		return s.StoresFindNormalized(opts)
	} else {
		if s.keysAreNormalized {
			var sample []float32
			if len(s.keys) > 5 {
				sample = tk[:5]
			} else {
				sample = tk
			}
			log.Debug().Msgf("Trying to compare non-normalized key with normalized keys: %v", sample)
		}

		return s.StoresFindFallback(opts)
	}
}
