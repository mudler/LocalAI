package main

// Regression suite for the local-store gRPC backend. Exercises the
// Stores{Set,Get,Find,Delete} surface — the only public contract.
// Callers (face/voice recognition, the routing KNN classifier) reach
// this code via grpc.Backend, so testing at the wire-shaped boundary
// matches the production import shape.

import (
	"math"
	"math/rand/v2"
	"testing"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/store"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("StoresSet", func() {
	It("rejects empty input", func() {
		Expect(NewStore().StoresSet(&pb.StoresSetOptions{})).NotTo(Succeed(), "Set with no keys should fail")
	})

	It("rejects key/value length mismatch", func() {
		err := NewStore().StoresSet(&pb.StoresSetOptions{
			Keys:   wrapKeys([][]float32{{1, 0, 0}}),
			Values: wrapValues([][]byte{[]byte("a"), []byte("b")}),
		})
		Expect(err).To(HaveOccurred(), "len(keys) != len(values) should fail")
	})

	It("rejects dimension mismatch on later add", func() {
		s := NewStore()
		mustSet(s, [][]float32{{1, 0, 0}}, [][]byte{[]byte("3d")})
		err := s.StoresSet(&pb.StoresSetOptions{
			Keys:   wrapKeys([][]float32{{1, 0}}),
			Values: wrapValues([][]byte{[]byte("2d")}),
		})
		Expect(err).To(HaveOccurred(), "dimension mismatch on later Set should fail")
	})

	It("rejects dimension mismatch within batch", func() {
		err := NewStore().StoresSet(&pb.StoresSetOptions{
			Keys:   wrapKeys([][]float32{{1, 0, 0}, {1, 0}}),
			Values: wrapValues([][]byte{[]byte("3d"), []byte("2d")}),
		})
		Expect(err).To(HaveOccurred(), "mixed-dimension within one batch should fail")
	})

	It("merges sorted and updates existing key", func() {
		s := NewStore()
		mustSet(s, [][]float32{{0.3, 0, 0}, {0.1, 0, 0}}, [][]byte{[]byte("c"), []byte("a")})
		mustSet(s, [][]float32{{0.2, 0, 0}, {0.1, 0, 0}}, [][]byte{[]byte("b"), []byte("a-updated")})
		Expect(s.keys).To(HaveLen(3))
		got := singleGet(s, []float32{0.1, 0, 0})
		Expect(string(got)).To(Equal("a-updated"))
	})
})

var _ = Describe("StoresGet", func() {
	It("round-trips multi-key", func() {
		s := NewStore()
		mustSet(s,
			[][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}, {0.7, 0.8, 0.9}},
			[][]byte{[]byte("a"), []byte("b"), []byte("c")},
		)
		res, err := s.StoresGet(&pb.StoresGetOptions{
			Keys: wrapKeys([][]float32{{0.7, 0.8, 0.9}, {0.1, 0.2, 0.3}}),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Keys).To(HaveLen(2))
	})

	It("omits missing keys rather than erroring", func() {
		s := NewStore()
		mustSet(s, [][]float32{{0.1, 0, 0}}, [][]byte{[]byte("a")})
		res, err := s.StoresGet(&pb.StoresGetOptions{
			Keys: wrapKeys([][]float32{{0.1, 0, 0}, {0.9, 0, 0}}),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Keys).To(HaveLen(1))
	})
})

var _ = Describe("StoresDelete", func() {
	It("removes and preserves sort", func() {
		s := NewStore()
		mustSet(s,
			[][]float32{{0.1, 0, 0}, {0.2, 0, 0}, {0.3, 0, 0}, {0.4, 0, 0}},
			[][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d")},
		)
		Expect(s.StoresDelete(&pb.StoresDeleteOptions{
			Keys: wrapKeys([][]float32{{0.2, 0, 0}, {0.4, 0, 0}}),
		})).To(Succeed())
		Expect(s.keys).To(HaveLen(2))
	})

	It("tolerates missing keys", func() {
		s := NewStore()
		mustSet(s, [][]float32{{0.1, 0, 0}}, [][]byte{[]byte("a")})
		Expect(s.StoresDelete(&pb.StoresDeleteOptions{
			Keys: wrapKeys([][]float32{{0.9, 0, 0}}),
		})).To(Succeed(), "delete of missing key should succeed")
		Expect(s.keys).To(HaveLen(1))
	})
})

var _ = Describe("StoresFind", func() {
	It("returns normalized top-K", func() {
		s := NewStore()
		mustSet(s,
			[][]float32{
				normalizeVec([]float32{1, 0, 0}),
				normalizeVec([]float32{0, 1, 0}),
				normalizeVec([]float32{0, 0, 1}),
			},
			[][]byte{[]byte("x"), []byte("y"), []byte("z")},
		)
		res, err := s.StoresFind(&pb.StoresFindOptions{
			Key:  &pb.StoresKey{Floats: normalizeVec([]float32{0.9, 0.1, 0})},
			TopK: 2,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Keys).To(HaveLen(2))
		Expect(res.Similarities[0]).To(BeNumerically(">=", res.Similarities[1]), "results not sorted desc by similarity")
		Expect(string(res.Values[0].Bytes)).To(Equal("x"))
	})

	It("falls back for non-normalized keys", func() {
		s := NewStore()
		mustSet(s, [][]float32{{2, 0, 0}, {0, 3, 0}}, [][]byte{[]byte("x"), []byte("y")})
		Expect(s.keysAreNormalized).To(BeFalse(), "store should report non-normalized after Set with magnitude > 1")
		res, err := s.StoresFind(&pb.StoresFindOptions{
			Key:  &pb.StoresKey{Floats: []float32{4, 0, 0}},
			TopK: 1,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(res.Values[0].Bytes)).To(Equal("x"))
		Expect(res.Similarities[0]).To(BeNumerically(">=", float32(0.99)))
		Expect(res.Similarities[0]).To(BeNumerically("<=", float32(1.01)))
	})

	It("rejects zero topK", func() {
		s := NewStore()
		mustSet(s, [][]float32{{1, 0, 0}}, [][]byte{[]byte("x")})
		_, err := s.StoresFind(&pb.StoresFindOptions{
			Key:  &pb.StoresKey{Floats: []float32{1, 0, 0}},
			TopK: 0,
		})
		Expect(err).To(HaveOccurred(), "Find with topK=0 should fail")
	})

	It("rejects dimension mismatch", func() {
		s := NewStore()
		mustSet(s, [][]float32{{1, 0, 0}}, [][]byte{[]byte("x")})
		_, err := s.StoresFind(&pb.StoresFindOptions{
			Key:  &pb.StoresKey{Floats: []float32{1, 0}},
			TopK: 1,
		})
		Expect(err).To(HaveOccurred(), "Find with mismatched dimension should fail")
	})

	It("returns empty result on empty store", func() {
		res, err := NewStore().StoresFind(&pb.StoresFindOptions{
			Key:  &pb.StoresKey{Floats: []float32{1, 0, 0}},
			TopK: 5,
		})
		Expect(err).NotTo(HaveOccurred(), "Find on empty store should succeed")
		Expect(res.Keys).To(BeEmpty())
	})

	It("handles topK larger than store", func() {
		s := NewStore()
		mustSet(s,
			[][]float32{normalizeVec([]float32{1, 0, 0}), normalizeVec([]float32{0, 1, 0})},
			[][]byte{[]byte("x"), []byte("y")},
		)
		res, err := s.StoresFind(&pb.StoresFindOptions{
			Key:  &pb.StoresKey{Floats: normalizeVec([]float32{1, 0, 0})},
			TopK: 10,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Keys).To(HaveLen(2))
	})
})

var _ = Describe("StoresLoad", func() {
	It("accepts prefixed store namespaces", func() {
		Expect(NewStore().Load(&pb.ModelOptions{Model: store.NamespacePrefix + "any-namespace"})).To(Succeed())
	})

	It("accepts the prefix alone (default store)", func() {
		Expect(NewStore().Load(&pb.ModelOptions{Model: store.NamespacePrefix})).To(Succeed())
	})

	It("refuses model names without the namespace prefix", func() {
		err := NewStore().Load(&pb.ModelOptions{Model: "some-llm.gguf"})
		Expect(err).To(MatchError(ContainSubstring("not a store namespace")))
		Expect(NewStore().Load(&pb.ModelOptions{})).NotTo(Succeed())
	})
})

func BenchmarkStoresFindNormalized(b *testing.B) {
	const dim = 768
	for _, n := range []int{8, 32, 128, 512} {
		b.Run(fmtN(n), func(b *testing.B) {
			s := buildStore(b, n, dim)
			query := normalizeVec(randVec(dim, 42))
			req := &pb.StoresFindOptions{Key: &pb.StoresKey{Floats: query}, TopK: 1}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.StoresFind(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// --- test helpers ---

func mustSet(s *Store, keys [][]float32, values [][]byte) {
	ExpectWithOffset(1, s.StoresSet(&pb.StoresSetOptions{Keys: wrapKeys(keys), Values: wrapValues(values)})).To(Succeed())
}

func singleGet(s *Store, key []float32) []byte {
	res, err := s.StoresGet(&pb.StoresGetOptions{Keys: wrapKeys([][]float32{key})})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	if len(res.Values) == 0 {
		return nil
	}
	return res.Values[0].Bytes
}

func wrapKeys(in [][]float32) []*pb.StoresKey {
	out := make([]*pb.StoresKey, len(in))
	for i, k := range in {
		out[i] = &pb.StoresKey{Floats: k}
	}
	return out
}

func wrapValues(in [][]byte) []*pb.StoresValue {
	out := make([]*pb.StoresValue, len(in))
	for i, v := range in {
		out[i] = &pb.StoresValue{Bytes: v}
	}
	return out
}

func buildStore(tb testing.TB, n, dim int) *Store {
	tb.Helper()
	s := NewStore()
	keys := make([][]float32, n)
	values := make([][]byte, n)
	for i := 0; i < n; i++ {
		keys[i] = normalizeVec(randVec(dim, int64(i)+1))
		values[i] = []byte{byte(i)}
	}
	if err := s.StoresSet(&pb.StoresSetOptions{Keys: wrapKeys(keys), Values: wrapValues(values)}); err != nil {
		tb.Fatal(err)
	}
	return s
}

func randVec(dim int, seed int64) []float32 {
	r := rand.New(rand.NewPCG(uint64(seed), 0xabcdef))
	v := make([]float32, dim)
	for i := range v {
		v[i] = float32(r.NormFloat64())
	}
	return v
}

func normalizeVec(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	mag := math.Sqrt(sum)
	if mag == 0 {
		return v
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / mag)
	}
	return out
}

func fmtN(n int) string {
	return map[int]string{8: "n=8", 32: "n=32", 128: "n=128", 512: "n=512"}[n]
}
