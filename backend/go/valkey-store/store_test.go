package main

// Regression suite for the valkey-store gRPC backend, mirroring the
// local-store suite: the Stores{Set,Get,Find,Delete} surface is the
// only public contract, so the two backends are drop-in alternatives.
//
// The suite needs a Valkey server with the Valkey Search module
// (valkey/valkey-bundle). Resolution order:
//   1. VALKEY_TEST_ADDR — use an already-running server (local dev),
//   2. otherwise start a valkey/valkey-bundle testcontainer
//      (skipped on darwin, following the repo-wide pattern).
// Load's namespace-gate tests run everywhere: they fail before any
// connection is attempted.

import (
	"context"
	"fmt"
	"math"
	"os"
	"runtime"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/store"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("StoresLoad namespace gate", func() {
	It("refuses model names without the namespace prefix", func() {
		err := NewStore().Load(&pb.ModelOptions{Model: "some-llm.gguf"})
		Expect(err).To(MatchError(ContainSubstring("not a store namespace")))
		Expect(NewStore().Load(&pb.ModelOptions{})).NotTo(Succeed())
	})
})

var _ = Describe("valkey-store against a live server", Ordered, func() {
	var nsCounter int

	BeforeAll(func() {
		if os.Getenv("VALKEY_TEST_ADDR") != "" {
			Expect(os.Setenv("VALKEY_ADDR", os.Getenv("VALKEY_TEST_ADDR"))).To(Succeed())
			return
		}
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI (set VALKEY_TEST_ADDR to run against a local server)")
		}
		ctx := context.Background()
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "valkey/valkey-bundle:latest",
				ExposedPorts: []string{"6379/tcp"},
				WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(60 * time.Second),
			},
			Started: true,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = c.Terminate(context.Background()) })
		host, err := c.Host(ctx)
		Expect(err).NotTo(HaveOccurred())
		port, err := c.MappedPort(ctx, "6379/tcp")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Setenv("VALKEY_ADDR", fmt.Sprintf("%s:%s", host, port.Port()))).To(Succeed())
	})

	// newTestStore loads a Store bound to a fresh namespace so specs
	// can't see each other's vectors, and drops the namespace's index
	// and keys when the spec ends so reruns against a long-lived
	// server (VALKEY_TEST_ADDR) start clean too.
	newTestStore := func() *Store {
		nsCounter++
		ns := fmt.Sprintf("test-%d-%d", GinkgoRandomSeed(), nsCounter)
		s := NewStore()
		ExpectWithOffset(1, s.Load(&pb.ModelOptions{Model: store.NamespacePrefix + ns})).To(Succeed())
		DeferCleanup(func() { dropNamespace(s) })
		return s
	}

	// reopen simulates a backend process restart: a brand-new Store
	// loading the same namespace against the same server.
	reopen := func(s *Store) *Store {
		ns := s.prefix[len(keyspacePrefix) : len(s.prefix)-1]
		fresh := NewStore()
		ExpectWithOffset(1, fresh.Load(&pb.ModelOptions{Model: store.NamespacePrefix + ns})).To(Succeed())
		return fresh
	}

	Describe("Load", func() {
		It("accepts prefixed store namespaces", func() {
			s := newTestStore()
			Expect(s.client).NotTo(BeNil())
		})

		It("accepts the prefix alone (default store)", func() {
			s := NewStore()
			Expect(s.Load(&pb.ModelOptions{Model: store.NamespacePrefix})).To(Succeed())
			DeferCleanup(func() { dropNamespace(s) })
		})
	})

	Describe("StoresSet", func() {
		It("rejects empty input", func() {
			Expect(newTestStore().StoresSet(&pb.StoresSetOptions{})).NotTo(Succeed(), "Set with no keys should fail")
		})

		It("rejects key/value length mismatch", func() {
			err := newTestStore().StoresSet(&pb.StoresSetOptions{
				Keys:   wrapKeys([][]float32{{1, 0, 0}}),
				Values: wrapValues([][]byte{[]byte("a"), []byte("b")}),
			})
			Expect(err).To(HaveOccurred(), "len(keys) != len(values) should fail")
		})

		It("rejects dimension mismatch on later add", func() {
			s := newTestStore()
			mustSet(s, [][]float32{{1, 0, 0}}, [][]byte{[]byte("3d")})
			err := s.StoresSet(&pb.StoresSetOptions{
				Keys:   wrapKeys([][]float32{{1, 0}}),
				Values: wrapValues([][]byte{[]byte("2d")}),
			})
			Expect(err).To(HaveOccurred(), "dimension mismatch on later Set should fail")
		})

		It("rejects dimension mismatch within batch", func() {
			err := newTestStore().StoresSet(&pb.StoresSetOptions{
				Keys:   wrapKeys([][]float32{{1, 0, 0}, {1, 0}}),
				Values: wrapValues([][]byte{[]byte("3d"), []byte("2d")}),
			})
			Expect(err).To(HaveOccurred(), "mixed-dimension within one batch should fail")
		})

		It("upserts existing keys", func() {
			s := newTestStore()
			mustSet(s, [][]float32{{0.3, 0, 0}, {0.1, 0, 0}}, [][]byte{[]byte("c"), []byte("a")})
			mustSet(s, [][]float32{{0.2, 0, 0}, {0.1, 0, 0}}, [][]byte{[]byte("b"), []byte("a-updated")})
			got := singleGet(s, []float32{0.1, 0, 0})
			Expect(string(got)).To(Equal("a-updated"))
			res, err := s.StoresGet(&pb.StoresGetOptions{
				Keys: wrapKeys([][]float32{{0.1, 0, 0}, {0.2, 0, 0}, {0.3, 0, 0}}),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Keys).To(HaveLen(3))
		})
	})

	Describe("StoresGet", func() {
		It("round-trips multi-key", func() {
			s := newTestStore()
			mustSet(s,
				[][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}, {0.7, 0.8, 0.9}},
				[][]byte{[]byte("a"), []byte("b"), []byte("c")},
			)
			res, err := s.StoresGet(&pb.StoresGetOptions{
				Keys: wrapKeys([][]float32{{0.7, 0.8, 0.9}, {0.1, 0.2, 0.3}}),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Keys).To(HaveLen(2))
			Expect(string(res.Values[0].Bytes)).To(Equal("c"))
			Expect(string(res.Values[1].Bytes)).To(Equal("a"))
		})

		It("omits missing keys rather than erroring", func() {
			s := newTestStore()
			mustSet(s, [][]float32{{0.1, 0, 0}}, [][]byte{[]byte("a")})
			res, err := s.StoresGet(&pb.StoresGetOptions{
				Keys: wrapKeys([][]float32{{0.1, 0, 0}, {0.9, 0, 0}}),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Keys).To(HaveLen(1))
		})

		It("returns empty on a never-written namespace", func() {
			res, err := newTestStore().StoresGet(&pb.StoresGetOptions{
				Keys: wrapKeys([][]float32{{0.1, 0, 0}}),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Keys).To(BeEmpty())
		})
	})

	Describe("StoresDelete", func() {
		It("removes keys", func() {
			s := newTestStore()
			mustSet(s,
				[][]float32{{0.1, 0, 0}, {0.2, 0, 0}, {0.3, 0, 0}, {0.4, 0, 0}},
				[][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d")},
			)
			Expect(s.StoresDelete(&pb.StoresDeleteOptions{
				Keys: wrapKeys([][]float32{{0.2, 0, 0}, {0.4, 0, 0}}),
			})).To(Succeed())
			res, err := s.StoresGet(&pb.StoresGetOptions{
				Keys: wrapKeys([][]float32{{0.1, 0, 0}, {0.2, 0, 0}, {0.3, 0, 0}, {0.4, 0, 0}}),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Keys).To(HaveLen(2))
		})

		It("tolerates missing keys", func() {
			s := newTestStore()
			mustSet(s, [][]float32{{0.1, 0, 0}}, [][]byte{[]byte("a")})
			Expect(s.StoresDelete(&pb.StoresDeleteOptions{
				Keys: wrapKeys([][]float32{{0.9, 0, 0}}),
			})).To(Succeed(), "delete of missing key should succeed")
			Expect(singleGet(s, []float32{0.1, 0, 0})).NotTo(BeNil())
		})
	})

	Describe("StoresFind", func() {
		It("returns normalized top-K most-similar first", func() {
			s := newTestStore()
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

		It("ranks non-normalized keys by cosine similarity", func() {
			s := newTestStore()
			mustSet(s, [][]float32{{2, 0, 0}, {0, 3, 0}}, [][]byte{[]byte("x"), []byte("y")})
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
			s := newTestStore()
			mustSet(s, [][]float32{{1, 0, 0}}, [][]byte{[]byte("x")})
			_, err := s.StoresFind(&pb.StoresFindOptions{
				Key:  &pb.StoresKey{Floats: []float32{1, 0, 0}},
				TopK: 0,
			})
			Expect(err).To(HaveOccurred(), "Find with topK=0 should fail")
		})

		It("rejects dimension mismatch", func() {
			s := newTestStore()
			mustSet(s, [][]float32{{1, 0, 0}}, [][]byte{[]byte("x")})
			_, err := s.StoresFind(&pb.StoresFindOptions{
				Key:  &pb.StoresKey{Floats: []float32{1, 0}},
				TopK: 1,
			})
			Expect(err).To(HaveOccurred(), "Find with mismatched dimension should fail")
		})

		It("returns empty result on empty store", func() {
			res, err := newTestStore().StoresFind(&pb.StoresFindOptions{
				Key:  &pb.StoresKey{Floats: []float32{1, 0, 0}},
				TopK: 5,
			})
			Expect(err).NotTo(HaveOccurred(), "Find on empty store should succeed")
			Expect(res.Keys).To(BeEmpty())
		})

		It("handles topK larger than store", func() {
			s := newTestStore()
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

	Describe("durability across process restarts", func() {
		It("serves Get and Find from a fresh Store on the same namespace", func() {
			s := newTestStore()
			mustSet(s, [][]float32{normalizeVec([]float32{1, 2, 3})}, [][]byte{[]byte("persisted")})

			fresh := reopen(s)
			Expect(fresh.keyLen).To(Equal(3), "restarted process should restore the index dimension")
			Expect(string(singleGet(fresh, normalizeVec([]float32{1, 2, 3})))).To(Equal("persisted"))
			res, err := fresh.StoresFind(&pb.StoresFindOptions{
				Key:  &pb.StoresKey{Floats: normalizeVec([]float32{1, 2, 3})},
				TopK: 1,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Values).To(HaveLen(1))
			Expect(string(res.Values[0].Bytes)).To(Equal("persisted"))
		})
	})

	Describe("namespace isolation", func() {
		It("keeps two namespaces invisible to each other", func() {
			a := newTestStore()
			b := newTestStore()
			mustSet(a, [][]float32{{1, 0, 0}}, [][]byte{[]byte("a-only")})
			mustSet(b, [][]float32{{0, 1, 0}}, [][]byte{[]byte("b-only")})

			res, err := a.StoresFind(&pb.StoresFindOptions{
				Key:  &pb.StoresKey{Floats: []float32{0, 1, 0}},
				TopK: 10,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Values).To(HaveLen(1))
			Expect(string(res.Values[0].Bytes)).To(Equal("a-only"))
		})
	})
})

// --- test helpers ---

func dropNamespace(s *Store) {
	if s.client == nil {
		return
	}
	ctx := context.Background()
	s.client.Do(ctx, s.client.B().Arbitrary("FT.DROPINDEX").Args(s.index).Build())
	var cursor uint64
	for {
		scan, err := s.client.Do(ctx, s.client.B().Scan().Cursor(cursor).Match(s.prefix+"*").Count(100).Build()).AsScanEntry()
		if err != nil {
			break
		}
		if len(scan.Elements) > 0 {
			s.client.Do(ctx, s.client.B().Del().Key(scan.Elements...).Build())
		}
		cursor = scan.Cursor
		if cursor == 0 {
			break
		}
	}
	s.client.Close()
}

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
