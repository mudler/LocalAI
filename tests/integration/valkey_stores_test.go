package integration_test

// Integration tests for the valkey-store gRPC backend. They mirror the
// local-store specs one-for-one and add the two capabilities Valkey provides
// that local-store cannot: persistence across a backend restart and an
// identifiable client name.
//
// These require a running Valkey Search server (valkey/valkey-bundle:9.1.0,
// which ships the FT.* module) reachable at $VALKEY_ADDR. When VALKEY_ADDR is
// unset the whole suite is skipped, so the unit CI never needs the container:
//
//	podman run -d --name valkey-store-it -p 6379:6379 valkey/valkey-bundle:9.1.0
//	make backends/valkey-store
//	VALKEY_ADDR=localhost:6379 make test-valkey-store
//
// Index back-fill is asynchronous, so every Find is preceded by a bounded poll
// (eventuallyFindable) on the search result rather than a fixed sleep.

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	valkey "github.com/valkey-io/valkey-go"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/store"
	"github.com/mudler/LocalAI/pkg/system"
)

// namespaceCounter gives each spec a fresh namespace so the persistent Valkey
// server does not leak state between tests sharing one instance.
var namespaceCounter atomic.Int64

func valkeyNormalize(vecs [][]float32) {
	for i, k := range vecs {
		norm := float64(0)
		for _, x := range k {
			norm += float64(x * x)
		}
		norm = math.Sqrt(norm)
		for j, x := range k {
			vecs[i][j] = x / float32(norm)
		}
	}
}

// eventuallyFindable polls Find until at least want results come back, absorbing
// the asynchronous index back-fill without a hard-coded sleep.
func eventuallyFindable(sc grpc.Backend, query []float32, want int) {
	EventuallyWithOffset(1, func() int {
		keys, _, _, err := store.Find(context.Background(), sc, query, want)
		if err != nil {
			return -1
		}
		return len(keys)
	}, 15*time.Second, 200*time.Millisecond).Should(BeNumerically(">=", want))
}

var _ = Describe("Integration tests for the valkey-store backend", Label("stores"), Label("valkey"), func() {
	Context("Valkey Search get, set, delete and find", func() {
		var sl *model.ModelLoader
		var sc grpc.Backend
		var tmpdir string
		var namespace string
		var valkeyAddr string
		var backendsPath string

		loadStore := func(ns string) grpc.Backend {
			storeOpts := []model.Option{
				model.WithBackendString(model.ValkeyStoreBackend),
				model.WithModel(ns),
			}
			backend, err := sl.Load(storeOpts...)
			Expect(err).ToNot(HaveOccurred())
			Expect(backend).ToNot(BeNil())
			return backend
		}

		// initLoader builds a fresh model loader that can discover the
		// valkey-store gRPC binary. The backend is registered from BACKENDS_PATH
		// exactly as the real application does at startup
		// (core/application/startup.go), so sl.Load(WithBackendString(...)) can
		// resolve the run.sh and spawn the process.
		initLoader := func() {
			systemState, err := system.GetSystemState(
				system.WithModelPath(tmpdir),
				system.WithBackendPath(backendsPath),
			)
			Expect(err).ToNot(HaveOccurred())

			sl = model.NewModelLoader(systemState)
			Expect(gallery.RegisterBackends(systemState, sl)).To(Succeed())
		}

		BeforeEach(func() {
			valkeyAddr = os.Getenv("VALKEY_ADDR")
			if valkeyAddr == "" {
				Skip("VALKEY_ADDR is not set; skipping Valkey integration tests")
			}
			backendsPath = os.Getenv("BACKENDS_PATH")
			if backendsPath == "" {
				Skip("BACKENDS_PATH is not set; build the backend and point BACKENDS_PATH at it (see make test-valkey-store)")
			}

			var err error
			tmpdir, err = os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())

			namespace = fmt.Sprintf("it-%d-%d", GinkgoRandomSeed(), namespaceCounter.Add(1))

			debug := true
			_ = config.ModelConfig{Name: "valkey store test", Debug: &debug, Backend: model.ValkeyStoreBackend}

			initLoader()
			sc = loadStore(namespace)
		})

		AfterEach(func() {
			if sl != nil {
				err := sl.StopAllGRPC()
				Expect(err).ToNot(HaveOccurred())
			}
			if tmpdir != "" {
				_ = os.RemoveAll(tmpdir)
			}
		})

		It("should be able to set a key", func() {
			err := store.SetSingle(context.Background(), sc, []float32{0.1, 0.2, 0.3}, []byte("test"))
			Expect(err).ToNot(HaveOccurred())
		})

		It("should be able to set keys", func() {
			err := store.SetCols(context.Background(), sc, [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}}, [][]byte{[]byte("test1"), []byte("test2")})
			Expect(err).ToNot(HaveOccurred())
			err = store.SetCols(context.Background(), sc, [][]float32{{0.7, 0.8, 0.9}, {0.10, 0.11, 0.12}}, [][]byte{[]byte("test3"), []byte("test4")})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should be able to get a key", func() {
			err := store.SetSingle(context.Background(), sc, []float32{0.1, 0.2, 0.3}, []byte("test"))
			Expect(err).ToNot(HaveOccurred())

			val, err := store.GetSingle(context.Background(), sc, []float32{0.1, 0.2, 0.3})
			Expect(err).ToNot(HaveOccurred())
			Expect(val).To(Equal([]byte("test")))
		})

		It("should be able to get keys", func() {
			err := store.SetCols(context.Background(), sc, [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}, {0.7, 0.8, 0.9}}, [][]byte{[]byte("test1"), []byte("test2"), []byte("test3")})
			Expect(err).ToNot(HaveOccurred())

			keys, vals, err := store.GetCols(context.Background(), sc, [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}, {0.7, 0.8, 0.9}})
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(3))
			Expect(vals).To(HaveLen(3))
			for i, k := range keys {
				v := vals[i]
				switch {
				case k[0] == 0.1 && k[1] == 0.2 && k[2] == 0.3:
					Expect(v).To(Equal([]byte("test1")))
				case k[0] == 0.4 && k[1] == 0.5 && k[2] == 0.6:
					Expect(v).To(Equal([]byte("test2")))
				default:
					Expect(k).To(Equal([]float32{0.7, 0.8, 0.9}))
					Expect(v).To(Equal([]byte("test3")))
				}
			}

			keys, vals, err = store.GetCols(context.Background(), sc, [][]float32{{0.7, 0.8, 0.9}, {0.1, 0.2, 0.3}})
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(2))
			Expect(vals).To(HaveLen(2))
		})

		It("should be able to delete a key", func() {
			err := store.SetSingle(context.Background(), sc, []float32{0.1, 0.2, 0.3}, []byte("test"))
			Expect(err).ToNot(HaveOccurred())

			err = store.DeleteSingle(context.Background(), sc, []float32{0.1, 0.2, 0.3})
			Expect(err).ToNot(HaveOccurred())

			val, _ := store.GetSingle(context.Background(), sc, []float32{0.1, 0.2, 0.3})
			Expect(val).To(BeNil())
		})

		It("should be able to delete keys", func() {
			err := store.SetCols(context.Background(), sc, [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}, {0.7, 0.8, 0.9}}, [][]byte{[]byte("test1"), []byte("test2"), []byte("test3")})
			Expect(err).ToNot(HaveOccurred())

			err = store.DeleteCols(context.Background(), sc, [][]float32{{0.1, 0.2, 0.3}, {0.7, 0.8, 0.9}})
			Expect(err).ToNot(HaveOccurred())

			keys, vals, err := store.GetCols(context.Background(), sc, [][]float32{{0.4, 0.5, 0.6}})
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(1))
			Expect(vals).To(HaveLen(1))
			Expect(keys[0]).To(Equal([]float32{0.4, 0.5, 0.6}))
			Expect(vals[0]).To(Equal([]byte("test2")))

			keys, vals, err = store.GetCols(context.Background(), sc, [][]float32{{0.1, 0.2, 0.3}, {0.7, 0.8, 0.9}})
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(0))
			Expect(vals).To(HaveLen(0))
		})

		It("should be able to find similar keys", func() {
			err := store.SetCols(context.Background(), sc, [][]float32{{0.5, 0.5, 0.5}, {0.6, 0.6, -0.6}, {0.7, -0.7, -0.7}}, [][]byte{[]byte("test1"), []byte("test2"), []byte("test3")})
			Expect(err).ToNot(HaveOccurred())

			eventuallyFindable(sc, []float32{0.1, 0.3, 0.5}, 2)

			keys, vals, sims, err := store.Find(context.Background(), sc, []float32{0.1, 0.3, 0.5}, 2)
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(2))
			Expect(vals).To(HaveLen(2))
			Expect(sims).To(HaveLen(2))
			Expect(keys[0]).To(Equal([]float32{0.5, 0.5, 0.5}))
			Expect(vals[0]).To(Equal([]byte("test1")))
			Expect(keys[1]).To(Equal([]float32{0.6, 0.6, -0.6}))
		})

		It("should be able to find similar normalized keys", func() {
			keys := [][]float32{{0.1, 0.3, 0.5}, {0.5, 0.5, 0.5}, {0.6, 0.6, -0.6}, {0.7, -0.7, -0.7}}
			vals := [][]byte{[]byte("test0"), []byte("test1"), []byte("test2"), []byte("test3")}
			valkeyNormalize(keys)

			err := store.SetCols(context.Background(), sc, keys, vals)
			Expect(err).ToNot(HaveOccurred())

			eventuallyFindable(sc, keys[0], 3)

			ks, _, sims, err := store.Find(context.Background(), sc, keys[0], 3)
			Expect(err).ToNot(HaveOccurred())
			Expect(ks).To(HaveLen(3))
			Expect(sims).To(HaveLen(3))
			Expect(ks[0]).To(Equal(keys[0]))
			Expect(sims[0]).To(BeNumerically("~", 1, 0.0001))
		})

		It("produces the correct cosine similarities for orthogonal and opposite unit vectors", func() {
			keys := [][]float32{{1.0, 0.0, 0.0}, {0.0, 1.0, 0.0}, {0.0, 0.0, 1.0}, {-1.0, 0.0, 0.0}}
			vals := [][]byte{[]byte("x"), []byte("y"), []byte("z"), []byte("-z")}

			err := store.SetCols(context.Background(), sc, keys, vals)
			Expect(err).ToNot(HaveOccurred())

			eventuallyFindable(sc, keys[0], 4)

			_, _, sims, err := store.Find(context.Background(), sc, keys[0], 4)
			Expect(err).ToNot(HaveOccurred())
			Expect(sims).To(HaveLen(4))
			Expect(sims[0]).To(BeNumerically("~", 1, 0.0001))
			Expect(sims[1]).To(BeNumerically("~", 0, 0.0001))
			Expect(sims[2]).To(BeNumerically("~", 0, 0.0001))
			Expect(sims[3]).To(BeNumerically("~", -1, 0.0001))
		})

		It("produces the correct cosine similarities for orthogonal and opposite vectors", func() {
			keys := [][]float32{{1.0, 0.0, 1.0}, {0.0, 2.0, 0.0}, {0.0, 0.0, -1.0}, {-1.0, 0.0, -1.0}}
			vals := [][]byte{[]byte("x"), []byte("y"), []byte("z"), []byte("-z")}

			err := store.SetCols(context.Background(), sc, keys, vals)
			Expect(err).ToNot(HaveOccurred())

			eventuallyFindable(sc, keys[0], 4)

			_, _, sims, err := store.Find(context.Background(), sc, keys[0], 4)
			Expect(err).ToNot(HaveOccurred())
			Expect(sims[0]).To(BeNumerically("~", 1, 0.1))
			Expect(sims[1]).To(BeNumerically("~", 0, 0.1))
			Expect(sims[2]).To(BeNumerically("~", -0.7, 0.1))
			Expect(sims[3]).To(BeNumerically("~", -1, 0.1))
		})

		expectTriangleEq := func(keys [][]float32, vals [][]byte) {
			eventuallyFindable(sc, keys[0], len(keys))
			sims := map[string]map[string]float32{}
			for i, k := range keys {
				_, valsk, simsk, err := store.Find(context.Background(), sc, k, len(keys))
				Expect(err).ToNot(HaveOccurred())
				for j, v := range valsk {
					p := string(vals[i])
					q := string(v)
					if sims[p] == nil {
						sims[p] = map[string]float32{}
					}
					sims[p][q] = simsk[j]
				}
			}
			for _, simsu := range sims {
				for w, simw := range simsu {
					uws := math.Acos(clampUnit(float64(simw)))
					for v := range simsu {
						uvws := math.Acos(clampUnit(float64(simsu[v]))) + math.Acos(clampUnit(float64(sims[v][w])))
						Expect(uws).To(BeNumerically("<=", uvws+0.0001))
					}
				}
			}
		}

		It("obeys the triangle inequality for normalized values", func() {
			keys := [][]float32{
				{1.0, 0.0, 0.0}, {0.0, 1.0, 0.0}, {0.0, 0.0, 1.0},
				{-1.0, 0.0, 0.0}, {0.0, -1.0, 0.0}, {0.0, 0.0, -1.0},
				{2.0, 3.0, 4.0}, {9.0, 7.0, 1.0}, {0.0, -1.2, 2.3},
			}
			vals := [][]byte{
				[]byte("x"), []byte("y"), []byte("z"),
				[]byte("-x"), []byte("-y"), []byte("-z"),
				[]byte("u"), []byte("v"), []byte("w"),
			}
			valkeyNormalize(keys[6:])

			err := store.SetCols(context.Background(), sc, keys, vals)
			Expect(err).ToNot(HaveOccurred())
			expectTriangleEq(keys, vals)
		})

		It("obeys the triangle inequality for random 768-d vectors", func() {
			rnd := rand.New(rand.NewPCG(151, 0))
			keys := make([][]float32, 20)
			vals := make([][]byte, 20)
			for i := range keys {
				k := make([]float32, 768)
				for j := range k {
					k[j] = rnd.Float32()
				}
				keys[i] = k
			}
			c := byte('a')
			for i := range vals {
				vals[i] = []byte{c}
				c++
			}

			err := store.SetCols(context.Background(), sc, keys, vals)
			Expect(err).ToNot(HaveOccurred())
			expectTriangleEq(keys, vals)
		})

		It("persists data across a backend restart", func() {
			// This is the capability local-store lacks: after the backend
			// process is torn down and a fresh one is spawned (same Valkey
			// server, same namespace), the data is still there.
			err := store.SetCols(context.Background(), sc,
				[][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}},
				[][]byte{[]byte("persisted1"), []byte("persisted2")})
			Expect(err).ToNot(HaveOccurred())

			Expect(sl.StopAllGRPC()).ToNot(HaveOccurred())

			// Fresh loader/process, same namespace → same index/prefix.
			initLoader()
			sc = loadStore(namespace)

			val, err := store.GetSingle(context.Background(), sc, []float32{0.1, 0.2, 0.3})
			Expect(err).ToNot(HaveOccurred())
			Expect(val).To(Equal([]byte("persisted1")))

			eventuallyFindable(sc, []float32{0.1, 0.2, 0.3}, 2)
			keys, _, _, err := store.Find(context.Background(), sc, []float32{0.1, 0.2, 0.3}, 2)
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(2))
		})

		It("identifies its connection with the mandatory client name", func() {
			// Seed a key so the backend has certainly opened a connection.
			err := store.SetSingle(context.Background(), sc, []float32{0.1, 0.2, 0.3}, []byte("test"))
			Expect(err).ToNot(HaveOccurred())

			client, err := valkey.NewClient(valkey.ClientOption{
				InitAddress:  []string{valkeyAddr},
				DisableCache: true,
			})
			Expect(err).ToNot(HaveOccurred())
			defer client.Close()

			list, err := client.Do(context.Background(), client.B().ClientList().Build()).ToString()
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.Contains(list, "name=localai-valkey-store")).To(BeTrue(), "expected a connection named localai-valkey-store in CLIENT LIST")
		})
	})
})

// clampUnit keeps a similarity inside [-1, 1] before math.Acos, guarding against
// float rounding that would otherwise push |x| slightly over 1 and yield NaN.
func clampUnit(x float64) float64 {
	if x > 1 {
		return 1
	}
	if x < -1 {
		return -1
	}
	return x
}
