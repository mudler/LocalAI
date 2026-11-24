package integration_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/qdrant/go-client/qdrant"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/store"
	"github.com/mudler/LocalAI/pkg/system"
)

var _ = Describe("Integration tests for the qdrant-store backend", Label("stores"), Label("qdrant"), func() {
	Context("Qdrant get, set, delete and find", func() {
		var sl *model.ModelLoader
		var sc grpc.Backend
		var tmpdir string
		var namespace string
		var qdrantAddr string
		var backendsPath string

		tryLoadStore := func(ns string, extra ...string) (grpc.Backend, error) {
			storeOpts := []model.Option{
				model.WithBackendString(model.QdrantStoreBackend),
				model.WithModelID(ns),
				model.WithModel(store.NamespacePrefix + ns),
				model.WithLoadGRPCLoadModelOpts(&pb.ModelOptions{
					Options: append([]string{"addr:" + qdrantAddr}, extra...),
				}),
			}
			return sl.Load(storeOpts...)
		}

		loadStore := func(ns string, extra ...string) grpc.Backend {
			backend, err := tryLoadStore(ns, extra...)
			Expect(err).ToNot(HaveOccurred())
			return backend
		}

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
			qdrantAddr = os.Getenv("QDRANT_ADDR")
			if qdrantAddr == "" {
				Skip("QDRANT_ADDR is not set; skipping Qdrant integration tests")
			}
			backendsPath = os.Getenv("BACKENDS_PATH")
			if backendsPath == "" {
				Skip("BACKENDS_PATH is not set; build the backend and point BACKENDS_PATH at it (see make test-qdrant-store)")
			}

			var err error
			tmpdir, err = os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())

			namespace = fmt.Sprintf("it-%d-%d", GinkgoRandomSeed(), namespaceCounter.Add(1))

			initLoader()
			sc = loadStore(namespace)
		})

		AfterEach(func() {
			if sl != nil {
				Expect(sl.StopAllGRPC()).To(Succeed())
			}
			if tmpdir != "" {
				_ = os.RemoveAll(tmpdir)
			}
			dropNamespaceCollections(qdrantAddr, namespace)
		})

		It("should set, get and delete keys", func() {
			err := store.SetCols(context.Background(), sc, [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}, {0.7, 0.8, 0.9}}, [][]byte{[]byte("test1"), []byte("test2"), []byte("test3")})
			Expect(err).ToNot(HaveOccurred())

			keys, vals, err := store.GetCols(context.Background(), sc, [][]float32{{0.7, 0.8, 0.9}, {9, 9, 9}, {0.1, 0.2, 0.3}})
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(Equal([][]float32{{0.7, 0.8, 0.9}, {0.1, 0.2, 0.3}}))
			Expect(vals).To(Equal([][]byte{[]byte("test3"), []byte("test1")}))

			err = store.DeleteCols(context.Background(), sc, [][]float32{{0.1, 0.2, 0.3}, {0.7, 0.8, 0.9}, {5, 5, 5}})
			Expect(err).ToNot(HaveOccurred())

			keys, vals, err = store.GetCols(context.Background(), sc, [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}, {0.7, 0.8, 0.9}})
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(Equal([][]float32{{0.4, 0.5, 0.6}}))
			Expect(vals).To(Equal([][]byte{[]byte("test2")}))
		})

		It("should find similar keys, returning the exact unnormalised keys", func() {
			err := store.SetCols(context.Background(), sc, [][]float32{{0.5, 0.5, 0.5}, {0.6, 0.6, -0.6}, {0.7, -0.7, -0.7}}, [][]byte{[]byte("test1"), []byte("test2"), []byte("test3")})
			Expect(err).ToNot(HaveOccurred())

			keys, vals, sims, err := store.Find(context.Background(), sc, []float32{0.1, 0.3, 0.5}, 2)
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(2))
			Expect(sims).To(HaveLen(2))
			Expect(keys[0]).To(Equal([]float32{0.5, 0.5, 0.5}))
			Expect(vals[0]).To(Equal([]byte("test1")))
			Expect(keys[1]).To(Equal([]float32{0.6, 0.6, -0.6}))
		})

		It("persists data across a backend restart", func() {
			err := store.SetCols(context.Background(), sc,
				[][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}},
				[][]byte{[]byte("persisted1"), []byte("persisted2")})
			Expect(err).ToNot(HaveOccurred())

			Expect(sl.StopAllGRPC()).To(Succeed())

			initLoader()
			sc = loadStore(namespace)

			val, err := store.GetSingle(context.Background(), sc, []float32{0.1, 0.2, 0.3})
			Expect(err).ToNot(HaveOccurred())
			Expect(val).To(Equal([]byte("persisted1")))

			keys, _, _, err := store.Find(context.Background(), sc, []float32{0.1, 0.2, 0.3}, 2)
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(2))

			Expect(store.SetSingle(context.Background(), sc, []float32{0.1, 0.2}, []byte("bad"))).ToNot(Succeed())
		})

		It("creates, recreates and checks a named collection on the server", func() {
			Expect(sl.StopAllGRPC()).To(Succeed())
			initLoader()
			collection := "localai_it_" + namespace
			sc = loadStore(namespace, "collection:"+collection, "distance_metric:DOT")
			Expect(store.SetSingle(context.Background(), sc, []float32{1, 2, 3, 4}, []byte("a"))).To(Succeed())

			host, port := splitAddr(qdrantAddr)
			client, err := qdrant.NewClient(&qdrant.Config{Host: host, Port: port, SkipCompatibilityCheck: true})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(client.Close)
			DeferCleanup(func() { _ = client.DeleteCollection(context.Background(), collection) })

			info, err := client.GetCollectionInfo(context.Background(), collection)
			Expect(err).ToNot(HaveOccurred())
			params := info.GetConfig().GetParams().GetVectorsConfig().GetParams()
			Expect(params.GetSize()).To(Equal(uint64(4)))
			Expect(params.GetDistance()).To(Equal(qdrant.Distance_Dot))
			Expect(info.GetPointsCount()).To(Equal(uint64(1)))

			Expect(client.DeleteCollection(context.Background(), collection)).To(Succeed())
			Expect(store.SetSingle(context.Background(), sc, []float32{1, 2}, []byte("b"))).To(Succeed())

			Expect(sl.StopAllGRPC()).To(Succeed())
			initLoader()
			_, err = tryLoadStore(namespace, "collection:"+collection, "distance_metric:COSINE")
			Expect(err).To(MatchError(ContainSubstring("distance_metric is Cosine")))
		})
	})
})

func splitAddr(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	Expect(err).ToNot(HaveOccurred())
	port, err := strconv.Atoi(portStr)
	Expect(err).ToNot(HaveOccurred())
	return host, port
}

func dropNamespaceCollections(addr, namespace string) {
	if addr == "" || namespace == "" {
		return
	}
	host, port := splitAddr(addr)
	client, err := qdrant.NewClient(&qdrant.Config{Host: host, Port: port, SkipCompatibilityCheck: true})
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = client.Close() }()
	names, err := client.ListCollections(context.Background())
	Expect(err).ToNot(HaveOccurred())
	for _, n := range names {
		if strings.HasPrefix(n, "localai_"+namespace+"-") {
			Expect(client.DeleteCollection(context.Background(), n)).To(Succeed())
		}
	}
}
