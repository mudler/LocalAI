package integration_test

import (
	"context"
	"embed"
	"math"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/assets"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/store"
)

//go:embed backend-assets/*
var backendAssets embed.FS

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
			backendAssetsDir := filepath.Join(tmpdir, "backend-assets")
			err = os.Mkdir(backendAssetsDir, 0750)
			Expect(err).ToNot(HaveOccurred())

			err = assets.ExtractFiles(backendAssets, backendAssetsDir)
			Expect(err).ToNot(HaveOccurred())

			debug := true

			bc := config.BackendConfig{
				Name:    "store test",
				Debug:   &debug,
				Backend: model.LocalStoreBackend,
			}

			storeOpts := []model.Option{
				model.WithBackendString(bc.Backend),
				model.WithAssetDir(backendAssetsDir),
				model.WithModel("test"),
			}

			sl = model.NewModelLoader("")
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
			// normalize the keys
			for i, k := range keys {
				norm := float64(0)
				for _, x := range k {
					norm += float64(x * x)
				}
				norm = math.Sqrt(norm)
				for j, x := range k {
					keys[i][j] = x / float32(norm)
				}
			}

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
	})
})
