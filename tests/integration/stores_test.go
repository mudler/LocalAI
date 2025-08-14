package integration_test

import (
	"context"
	"math"
	"math/rand"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/store"
	"github.com/mudler/LocalAI/pkg/system"
)

func normalize(vecs [][]float32) {
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

var _ = Describe("Integration tests for the stores backend(s) and internal APIs", Label("stores"), func() {
	Context("Embedded Store get,set and delete", func() {
		var sl *model.ModelLoader
		var sc grpc.Backend
		var tmpdir string

		BeforeEach(func() {
			var err error

			zerolog.SetGlobalLevel(zerolog.DebugLevel)

			tmpdir, err = os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())

			debug := true

			bc := config.ModelConfig{
				Name:    "store test",
				Debug:   &debug,
				Backend: model.LocalStoreBackend,
			}

			storeOpts := []model.Option{
				model.WithBackendString(bc.Backend),
				model.WithModel("test"),
			}

			systemState, err := system.GetSystemState(
				system.WithModelPath(tmpdir),
			)
			Expect(err).ToNot(HaveOccurred())

			sl = model.NewModelLoader(systemState, false)
			sc, err = sl.Load(storeOpts...)
			Expect(err).ToNot(HaveOccurred())
			Expect(sc).ToNot(BeNil())
		})

		AfterEach(func() {
			err := sl.StopAllGRPC()
			Expect(err).ToNot(HaveOccurred())
			err = os.RemoveAll(tmpdir)
			Expect(err).ToNot(HaveOccurred())
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
			//set 3 entries
			err := store.SetCols(context.Background(), sc, [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}, {0.7, 0.8, 0.9}}, [][]byte{[]byte("test1"), []byte("test2"), []byte("test3")})
			Expect(err).ToNot(HaveOccurred())

			//get 3 entries
			keys, vals, err := store.GetCols(context.Background(), sc, [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}, {0.7, 0.8, 0.9}})
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(3))
			Expect(vals).To(HaveLen(3))
			for i, k := range keys {
				v := vals[i]

				if k[0] == 0.1 && k[1] == 0.2 && k[2] == 0.3 {
					Expect(v).To(Equal([]byte("test1")))
				} else if k[0] == 0.4 && k[1] == 0.5 && k[2] == 0.6 {
					Expect(v).To(Equal([]byte("test2")))
				} else {
					Expect(k).To(Equal([]float32{0.7, 0.8, 0.9}))
					Expect(v).To(Equal([]byte("test3")))
				}
			}

			//get 2 entries
			keys, vals, err = store.GetCols(context.Background(), sc, [][]float32{{0.7, 0.8, 0.9}, {0.1, 0.2, 0.3}})
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(2))
			Expect(vals).To(HaveLen(2))
			for i, k := range keys {
				v := vals[i]

				if k[0] == 0.1 && k[1] == 0.2 && k[2] == 0.3 {
					Expect(v).To(Equal([]byte("test1")))
				} else {
					Expect(k).To(Equal([]float32{0.7, 0.8, 0.9}))
					Expect(v).To(Equal([]byte("test3")))
				}
			}
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
			//set 3 entries
			err := store.SetCols(context.Background(), sc, [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}, {0.7, 0.8, 0.9}}, [][]byte{[]byte("test1"), []byte("test2"), []byte("test3")})
			Expect(err).ToNot(HaveOccurred())

			//delete 2 entries
			err = store.DeleteCols(context.Background(), sc, [][]float32{{0.1, 0.2, 0.3}, {0.7, 0.8, 0.9}})
			Expect(err).ToNot(HaveOccurred())

			//get 1 entry
			keys, vals, err := store.GetCols(context.Background(), sc, [][]float32{{0.4, 0.5, 0.6}})
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(1))
			Expect(vals).To(HaveLen(1))
			Expect(keys[0]).To(Equal([]float32{0.4, 0.5, 0.6}))
			Expect(vals[0]).To(Equal([]byte("test2")))

			//get deleted entries
			keys, vals, err = store.GetCols(context.Background(), sc, [][]float32{{0.1, 0.2, 0.3}, {0.7, 0.8, 0.9}})
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(0))
			Expect(vals).To(HaveLen(0))
		})

		It("should be able to find smilar keys", func() {
			// set 3 vectors that are at varying angles to {0.5, 0.5, 0.5}
			err := store.SetCols(context.Background(), sc, [][]float32{{0.5, 0.5, 0.5}, {0.6, 0.6, -0.6}, {0.7, -0.7, -0.7}}, [][]byte{[]byte("test1"), []byte("test2"), []byte("test3")})
			Expect(err).ToNot(HaveOccurred())

			// find similar keys
			keys, vals, sims, err := store.Find(context.Background(), sc, []float32{0.1, 0.3, 0.5}, 2)
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(2))
			Expect(vals).To(HaveLen(2))
			Expect(sims).To(HaveLen(2))

			for i, k := range keys {
				s := sims[i]
				log.Debug().Float32("similarity", s).Msgf("key: %v", k)
			}

			Expect(keys[0]).To(Equal([]float32{0.5, 0.5, 0.5}))
			Expect(vals[0]).To(Equal([]byte("test1")))
			Expect(keys[1]).To(Equal([]float32{0.6, 0.6, -0.6}))
		})

		It("should be able to find similar normalized keys", func() {
			// set 3 vectors that are at varying angles to {0.5, 0.5, 0.5}
			keys := [][]float32{{0.1, 0.3, 0.5}, {0.5, 0.5, 0.5}, {0.6, 0.6, -0.6}, {0.7, -0.7, -0.7}}
			vals := [][]byte{[]byte("test0"), []byte("test1"), []byte("test2"), []byte("test3")}

			normalize(keys)

			err := store.SetCols(context.Background(), sc, keys, vals)
			Expect(err).ToNot(HaveOccurred())

			// find similar keys
			ks, vals, sims, err := store.Find(context.Background(), sc, keys[0], 3)
			Expect(err).ToNot(HaveOccurred())
			Expect(ks).To(HaveLen(3))
			Expect(vals).To(HaveLen(3))
			Expect(sims).To(HaveLen(3))

			for i, k := range ks {
				s := sims[i]
				log.Debug().Float32("similarity", s).Msgf("key: %v", k)
			}

			Expect(ks[0]).To(Equal(keys[0]))
			Expect(vals[0]).To(Equal(vals[0]))
			Expect(sims[0]).To(BeNumerically("~", 1, 0.0001))
			Expect(ks[1]).To(Equal(keys[1]))
			Expect(vals[1]).To(Equal(vals[1]))
		})

		It("It produces the correct cosine similarities for orthogonal and opposite unit vectors", func() {
			keys := [][]float32{{1.0, 0.0, 0.0}, {0.0, 1.0, 0.0}, {0.0, 0.0, 1.0}, {-1.0, 0.0, 0.0}}
			vals := [][]byte{[]byte("x"), []byte("y"), []byte("z"), []byte("-z")}

			err := store.SetCols(context.Background(), sc, keys, vals)
			Expect(err).ToNot(HaveOccurred())

			_, _, sims, err := store.Find(context.Background(), sc, keys[0], 4)
			Expect(err).ToNot(HaveOccurred())
			Expect(sims).To(Equal([]float32{1.0, 0.0, 0.0, -1.0}))
		})

		It("It produces the correct cosine similarities for orthogonal and opposite vectors", func() {
			keys := [][]float32{{1.0, 0.0, 1.0}, {0.0, 2.0, 0.0}, {0.0, 0.0, -1.0}, {-1.0, 0.0, -1.0}}
			vals := [][]byte{[]byte("x"), []byte("y"), []byte("z"), []byte("-z")}

			err := store.SetCols(context.Background(), sc, keys, vals)
			Expect(err).ToNot(HaveOccurred())

			_, _, sims, err := store.Find(context.Background(), sc, keys[0], 4)
			Expect(err).ToNot(HaveOccurred())
			Expect(sims[0]).To(BeNumerically("~", 1, 0.1))
			Expect(sims[1]).To(BeNumerically("~", 0, 0.1))
			Expect(sims[2]).To(BeNumerically("~", -0.7, 0.1))
			Expect(sims[3]).To(BeNumerically("~", -1, 0.1))
		})

		expectTriangleEq := func(keys [][]float32, vals [][]byte) {
			sims := map[string]map[string]float32{}

			// compare every key vector pair and store the similarities in a lookup table
			// that uses the values as keys
			for i, k := range keys {
				_, valsk, simsk, err := store.Find(context.Background(), sc, k, 9)
				Expect(err).ToNot(HaveOccurred())

				for j, v := range valsk {
					p := string(vals[i])
					q := string(v)

					if sims[p] == nil {
						sims[p] = map[string]float32{}
					}

					//log.Debug().Strs("vals", []string{p, q}).Float32("similarity", simsk[j]).Send()

					sims[p][q] = simsk[j]
				}
			}

			// Check that the triangle inequality holds for every combination of the triplet
			// u, v and w
			for _, simsu := range sims {
				for w, simw := range simsu {
					// acos(u,w) <= ...
					uws := math.Acos(float64(simw))

					// ... acos(u,v) + acos(v,w)
					for v, _ := range simsu {
						uvws := math.Acos(float64(simsu[v])) + math.Acos(float64(sims[v][w]))

						//log.Debug().Str("u", u).Str("v", v).Str("w", w).Send()
						//log.Debug().Float32("uw", simw).Float32("uv", simsu[v]).Float32("vw", sims[v][w]).Send()
						Expect(uws).To(BeNumerically("<=", uvws))
					}
				}
			}
		}

		It("It obeys the triangle inequality for normalized values", func() {
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

			normalize(keys[6:])

			err := store.SetCols(context.Background(), sc, keys, vals)
			Expect(err).ToNot(HaveOccurred())

			expectTriangleEq(keys, vals)
		})

		It("It obeys the triangle inequality", func() {
			rnd := rand.New(rand.NewSource(151))
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
				c += 1
			}

			err := store.SetCols(context.Background(), sc, keys, vals)
			Expect(err).ToNot(HaveOccurred())

			expectTriangleEq(keys, vals)
		})
	})
})
