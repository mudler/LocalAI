package main

// Unit tests for the Valkey store, using the valkey-go gomock client so they
// run with no container. They assert the exact commands built for each RPC
// (the wire contract) plus the local-store parity semantics: empty/len/dim
// rejects, omit-missing Get, tolerate-missing Delete, topK<1 reject, the
// sim = 1 - distance conversion, lazy FT.CREATE, and the HNSW arg-shape.

import (
	"context"
	"fmt"
	"strconv"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/store"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	valkey "github.com/valkey-io/valkey-go"
	"github.com/valkey-io/valkey-go/mock"
	"go.uber.org/mock/gomock"
)

const testNamespace = "test"

func testCfg() Config {
	cfg, err := loadConfig() // reads defaults when no env is set
	Expect(err).NotTo(HaveOccurred())
	return cfg
}

func newMockStore(cfg Config) (*ValkeyStore, *mock.Client) {
	ctrl := gomock.NewController(GinkgoT())
	DeferCleanup(ctrl.Finish)
	c := mock.NewClient(ctrl)
	return newWithClient(c, cfg, testNamespace), c
}

func wrapSet(keys [][]float32, values [][]byte) *pb.StoresSetOptions {
	return &pb.StoresSetOptions{Keys: store.WrapKeys(keys), Values: store.WrapValues(values)}
}

var _ = Describe("loadConfig", func() {
	// Clear any VALKEY_* the developer's shell may have set so the defaults are
	// deterministic. GinkgoT().Setenv restores the previous value after each
	// spec automatically, so no manual save/restore is needed (and it returns
	// nothing, so there's no error to check).
	envKeys := []string{
		"VALKEY_ADDR", "VALKEY_CLIENT_NAME", "VALKEY_INDEX_ALGO", "VALKEY_DISTANCE_METRIC",
		"VALKEY_REQUEST_TIMEOUT_MS", "VALKEY_TLS", "VALKEY_TLS_SKIP_VERIFY", "VALKEY_TLS_CA_CERT",
		"VALKEY_DB", "VALKEY_HNSW_M", "VALKEY_HNSW_EF_CONSTRUCTION", "VALKEY_HNSW_EF_RUNTIME",
	}

	BeforeEach(func() {
		for _, k := range envKeys {
			GinkgoT().Setenv(k, "")
		}
	})

	It("uses documented defaults", func() {
		cfg, err := loadConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Addr).To(Equal("localhost:6379"))
		Expect(cfg.ClientName).To(Equal("localai-valkey-store"))
		Expect(cfg.IndexAlgo).To(Equal("FLAT"))
		Expect(cfg.DistanceMetric).To(Equal("COSINE"))
		Expect(cfg.RequestTimeout.Milliseconds()).To(Equal(int64(5000)))
	})

	It("honours env overrides", func() {
		GinkgoT().Setenv("VALKEY_ADDR", "valkey.example:6380")
		GinkgoT().Setenv("VALKEY_INDEX_ALGO", "hnsw")
		GinkgoT().Setenv("VALKEY_DISTANCE_METRIC", "l2")
		GinkgoT().Setenv("VALKEY_REQUEST_TIMEOUT_MS", "1234")
		cfg, err := loadConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Addr).To(Equal("valkey.example:6380"))
		Expect(cfg.IndexAlgo).To(Equal("HNSW"))
		Expect(cfg.DistanceMetric).To(Equal("L2"))
		Expect(cfg.RequestTimeout.Milliseconds()).To(Equal(int64(1234)))
	})

	It("keeps the mandatory client name when blanked", func() {
		GinkgoT().Setenv("VALKEY_CLIENT_NAME", "")
		cfg, err := loadConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.ClientName).To(Equal("localai-valkey-store"))
	})

	It("rejects an invalid index algo", func() {
		GinkgoT().Setenv("VALKEY_INDEX_ALGO", "bogus")
		_, err := loadConfig()
		Expect(err).To(HaveOccurred())
	})

	It("rejects an invalid distance metric", func() {
		GinkgoT().Setenv("VALKEY_DISTANCE_METRIC", "bogus")
		_, err := loadConfig()
		Expect(err).To(HaveOccurred())
	})

	It("fails fast on a malformed HNSW integer instead of silently defaulting", func() {
		GinkgoT().Setenv("VALKEY_HNSW_M", "1x6")
		_, err := loadConfig()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("VALKEY_HNSW_M"))
	})

	It("honours a valid VALKEY_DB override", func() {
		GinkgoT().Setenv("VALKEY_DB", "3")
		cfg, err := loadConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.DB).To(Equal(3))
	})

	It("rejects a negative VALKEY_DB", func() {
		GinkgoT().Setenv("VALKEY_DB", "-1")
		_, err := loadConfig()
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("StoresSet", func() {
	It("rejects empty input", func() {
		s, _ := newMockStore(testCfg())
		Expect(s.StoresSet(&pb.StoresSetOptions{})).NotTo(Succeed())
	})

	It("rejects key/value length mismatch", func() {
		s, _ := newMockStore(testCfg())
		err := s.StoresSet(wrapSet([][]float32{{1, 0, 0}}, [][]byte{[]byte("a"), []byte("b")}))
		Expect(err).To(HaveOccurred())
	})

	It("rejects dimension mismatch on a later add", func() {
		s, c := newMockStore(testCfg())
		// First Set issues FT.CREATE then a sequential HSET (both via Do).
		c.EXPECT().Do(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
				if cmd.Commands()[0] == "FT.CREATE" {
					return assertFTCreate(3, "FLAT")(ctx, cmd)
				}
				return mock.Result(mock.ValkeyInt64(1)) // HSET
			}).AnyTimes()
		Expect(s.StoresSet(wrapSet([][]float32{{1, 0, 0}}, [][]byte{[]byte("3d")}))).To(Succeed())

		err := s.StoresSet(wrapSet([][]float32{{1, 0}}, [][]byte{[]byte("2d")}))
		Expect(err).To(HaveOccurred())
	})

	It("rejects dimension mismatch within a batch", func() {
		s, _ := newMockStore(testCfg())
		err := s.StoresSet(wrapSet([][]float32{{1, 0, 0}, {1, 0}}, [][]byte{[]byte("3d"), []byte("2d")}))
		Expect(err).To(HaveOccurred())
	})

	It("creates the FLAT index once and HSETs each entry", func() {
		s, c := newMockStore(testCfg())
		// FT.CREATE must run exactly once (on the first Set); each entry is then
		// written with an individual sequential HSET (Do, not DoMulti — see the
		// pipeline-deadlock note in StoresSet).
		var ftCreateCount, hsetCount int
		c.EXPECT().Do(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
				toks := cmd.Commands()
				switch toks[0] {
				case "FT.CREATE":
					ftCreateCount++
					return assertFTCreate(3, "FLAT")(ctx, cmd)
				case "HSET":
					hsetCount++
					Expect(toks[1]).To(HavePrefix(s.prefix))
					Expect(toks).To(ContainElements("vec", "val"))
					return mock.Result(mock.ValkeyInt64(1))
				default:
					Fail("unexpected command: " + toks[0])
					return valkey.ValkeyResult{}
				}
			}).AnyTimes()
		Expect(s.StoresSet(wrapSet([][]float32{{1, 0, 0}}, [][]byte{[]byte("a")}))).To(Succeed())
		Expect(s.StoresSet(wrapSet([][]float32{{2, 0, 0}}, [][]byte{[]byte("b")}))).To(Succeed())

		Expect(ftCreateCount).To(Equal(1))
		Expect(hsetCount).To(Equal(2))
	})
})

var _ = Describe("StoresGet", func() {
	It("round-trips values and omits missing keys", func() {
		s, c := newMockStore(testCfg())
		s.keyLen = 3
		s.indexCreated = true
		// First key present, second missing (nil reply).
		c.EXPECT().DoMulti(gomock.Any(), gomock.Any(), gomock.Any()).Return([]valkey.ValkeyResult{
			mock.Result(mock.ValkeyString("hello")),
			mock.Result(mock.ValkeyNil()),
		}).Times(1)

		res, err := s.StoresGet(&pb.StoresGetOptions{
			Keys: store.WrapKeys([][]float32{{1, 0, 0}, {9, 0, 0}}),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Keys).To(HaveLen(1))
		Expect(res.Values).To(HaveLen(1))
		Expect(res.Values[0].Bytes).To(Equal([]byte("hello")))
	})

	It("rejects dimension mismatch", func() {
		s, _ := newMockStore(testCfg())
		s.keyLen = 3
		_, err := s.StoresGet(&pb.StoresGetOptions{Keys: store.WrapKeys([][]float32{{1, 0}})})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("StoresDelete", func() {
	It("issues DEL per key and tolerates missing", func() {
		s, c := newMockStore(testCfg())
		s.keyLen = 3
		// DEL of a missing key returns 0 — still a success. DELs are issued
		// sequentially (Do, not DoMulti — see the deadlock note in StoresSet).
		c.EXPECT().Do(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, cmd valkey.Completed) valkey.ValkeyResult {
				Expect(cmd.Commands()[0]).To(Equal("DEL"))
				return mock.Result(mock.ValkeyInt64(0))
			}).Times(1)
		Expect(s.StoresDelete(&pb.StoresDeleteOptions{
			Keys: store.WrapKeys([][]float32{{9, 0, 0}}),
		})).To(Succeed())
	})

	It("rejects dimension mismatch", func() {
		s, _ := newMockStore(testCfg())
		s.keyLen = 3
		err := s.StoresDelete(&pb.StoresDeleteOptions{Keys: store.WrapKeys([][]float32{{1, 0}})})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("StoresFind", func() {
	It("builds the KNN query and converts distance to similarity nearest-first", func() {
		s, c := newMockStore(testCfg())
		s.keyLen = 3
		s.indexCreated = true

		// Distances 0, 1, 2 must map to similarities 1, 0, -1 (COSINE).
		docs := []ftDoc{
			{vec: []float32{1, 0, 0}, val: "a", dist: 0},
			{vec: []float32{0, 1, 0}, val: "b", dist: 1},
			{vec: []float32{-1, 0, 0}, val: "c", dist: 2},
		}
		c.EXPECT().Do(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, cmd valkey.Completed) valkey.ValkeyResult {
				toks := cmd.Commands()
				Expect(toks[0]).To(Equal("FT.SEARCH"))
				Expect(toks[1]).To(Equal(s.indexName))
				Expect(toks[2]).To(ContainSubstring("KNN 3 @vec $q AS __score"))
				Expect(toks).To(ContainElements("PARAMS", "2", "q", "DIALECT", "2"))
				return mock.Result(ftSearchReply(s.prefix, docs))
			}).Times(1)

		keys, values, sims, err := findViaRPC(s, []float32{1, 0, 0}, 3)
		Expect(err).NotTo(HaveOccurred())
		Expect(sims).To(Equal([]float32{1, 0, -1}))
		Expect(values[0]).To(Equal([]byte("a")))
		// Key decoded from returned vec bytes equals the original vector.
		Expect(keys[0]).To(Equal([]float32{1, 0, 0}))
	})

	It("rejects topK < 1", func() {
		s, _ := newMockStore(testCfg())
		s.keyLen = 3
		s.indexCreated = true
		_, err := s.StoresFind(&pb.StoresFindOptions{Key: &pb.StoresKey{Floats: []float32{1, 0, 0}}, TopK: 0})
		Expect(err).To(HaveOccurred())
	})

	It("rejects a nil Key without panicking", func() {
		s, _ := newMockStore(testCfg())
		s.keyLen = 3
		s.indexCreated = true
		_, err := s.StoresFind(&pb.StoresFindOptions{TopK: 5})
		Expect(err).To(HaveOccurred())
	})

	It("rejects an empty query vector", func() {
		s, _ := newMockStore(testCfg())
		s.keyLen = 3
		s.indexCreated = true
		_, err := s.StoresFind(&pb.StoresFindOptions{Key: &pb.StoresKey{Floats: []float32{}}, TopK: 5})
		Expect(err).To(HaveOccurred())
	})

	It("rejects query dimension mismatch", func() {
		s, _ := newMockStore(testCfg())
		s.keyLen = 3
		s.indexCreated = true
		_, err := s.StoresFind(&pb.StoresFindOptions{Key: &pb.StoresKey{Floats: []float32{1, 0}}, TopK: 1})
		Expect(err).To(HaveOccurred())
	})

	It("returns empty (no error) when the index was never created", func() {
		s, _ := newMockStore(testCfg())
		res, err := s.StoresFind(&pb.StoresFindOptions{Key: &pb.StoresKey{Floats: []float32{1, 0, 0}}, TopK: 5})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Keys).To(BeEmpty())
	})
})

var _ = Describe("ensureIndex arg-shape", func() {
	It("emits HNSW tuning tokens when the algo is HNSW", func() {
		cfg := testCfg()
		cfg.IndexAlgo = indexAlgoHNSW
		cfg.HNSW = hnswParams{M: 16, EFConstruction: 200, EFRuntime: 10}
		s, c := newMockStore(cfg)
		c.EXPECT().Do(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, cmd valkey.Completed) valkey.ValkeyResult {
				toks := cmd.Commands()
				Expect(toks).To(ContainElements("HNSW", "M", "16", "EF_CONSTRUCTION", "200", "EF_RUNTIME", "10"))
				return mock.Result(mock.ValkeyString("OK"))
			}).Times(1)
		Expect(s.ensureIndex(4)).To(Succeed())
	})

	It("treats an already-exists error as success", func() {
		s, c := newMockStore(testCfg())
		c.EXPECT().Do(gomock.Any(), gomock.Any()).Return(
			mock.ErrorResult(fmt.Errorf("Index already exists"))).Times(1)
		Expect(s.ensureIndex(4)).To(Succeed())
		Expect(s.indexCreated).To(BeTrue())
	})
})

var _ = Describe("distanceToSimilarity", func() {
	It("converts cosine distance to similarity", func() {
		Expect(distanceToSimilarity(distanceCosine, 0)).To(Equal(float32(1)))
		Expect(distanceToSimilarity(distanceCosine, 1)).To(Equal(float32(0)))
		Expect(distanceToSimilarity(distanceCosine, 2)).To(Equal(float32(-1)))
	})

	It("passes the raw score through for non-cosine metrics", func() {
		Expect(distanceToSimilarity(distanceL2, 0.42)).To(Equal(float32(0.42)))
	})
})

var _ = Describe("findDimensions", func() {
	It("recovers an integer dimension from a nested FT.INFO reply", func() {
		dim, ok := findDimensions(ftInfoReply(mock.ValkeyInt64(768)))
		Expect(ok).To(BeTrue())
		Expect(dim).To(Equal(768))
	})

	It("recovers a string-encoded dimension", func() {
		dim, ok := findDimensions(ftInfoReply(mock.ValkeyString("384")))
		Expect(ok).To(BeTrue())
		Expect(dim).To(Equal(384))
	})

	It("reports not-found when no dimension token is present", func() {
		reply := mock.ValkeyArray(
			mock.ValkeyString("index_name"), mock.ValkeyString("idx:test"),
			mock.ValkeyString("num_docs"), mock.ValkeyInt64(0),
		)
		_, ok := findDimensions(reply)
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("loadIndexState", func() {
	It("recovers indexCreated and keyLen from a persisted index", func() {
		s, c := newMockStore(testCfg())
		c.EXPECT().Do(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, cmd valkey.Completed) valkey.ValkeyResult {
				Expect(cmd.Commands()[0]).To(Equal("FT.INFO"))
				return mock.Result(ftInfoReply(mock.ValkeyInt64(768)))
			}).Times(1)

		ctx, cancel := s.ctx()
		defer cancel()
		s.loadIndexState(ctx)
		Expect(s.indexCreated).To(BeTrue())
		Expect(s.keyLen).To(Equal(768))
	})

	It("leaves state untouched when the index does not exist", func() {
		s, c := newMockStore(testCfg())
		c.EXPECT().Do(gomock.Any(), gomock.Any()).Return(
			mock.ErrorResult(fmt.Errorf("Index with name 'idx:test' not found"))).Times(1)

		ctx, cancel := s.ctx()
		defer cancel()
		s.loadIndexState(ctx)
		Expect(s.indexCreated).To(BeFalse())
		Expect(s.keyLen).To(Equal(-1))
	})

	It("marks the index created but leaves keyLen open when the dim is unparseable", func() {
		s, c := newMockStore(testCfg())
		c.EXPECT().Do(gomock.Any(), gomock.Any()).Return(
			mock.Result(mock.ValkeyArray(mock.ValkeyString("index_name"), mock.ValkeyString("idx:test")))).Times(1)

		ctx, cancel := s.ctx()
		defer cancel()
		s.loadIndexState(ctx)
		Expect(s.indexCreated).To(BeTrue())
		Expect(s.keyLen).To(Equal(-1))
	})
})

var _ = Describe("StoresFind on a dropped index", func() {
	It("returns empty (no error) and clears the stale flag when the index is gone", func() {
		s, c := newMockStore(testCfg())
		s.keyLen = 3
		s.indexCreated = true
		c.EXPECT().Do(gomock.Any(), gomock.Any()).Return(
			mock.ErrorResult(fmt.Errorf("Index with name 'idx:test' not found"))).Times(1)

		res, err := s.StoresFind(&pb.StoresFindOptions{Key: &pb.StoresKey{Floats: []float32{1, 0, 0}}, TopK: 5})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Keys).To(BeEmpty())
		Expect(s.indexCreated).To(BeFalse())
	})

	It("still surfaces a genuine FT.SEARCH error", func() {
		s, c := newMockStore(testCfg())
		s.keyLen = 3
		s.indexCreated = true
		c.EXPECT().Do(gomock.Any(), gomock.Any()).Return(
			mock.ErrorResult(fmt.Errorf("timeout"))).Times(1)

		_, err := s.StoresFind(&pb.StoresFindOptions{Key: &pb.StoresKey{Floats: []float32{1, 0, 0}}, TopK: 5})
		Expect(err).To(HaveOccurred())
		Expect(s.indexCreated).To(BeTrue())
	})
})

// --- test helpers ---

type ftDoc struct {
	vec  []float32
	val  string
	dist float64
}

func findViaRPC(s *ValkeyStore, query []float32, topK int) ([][]float32, [][]byte, []float32, error) {
	res, err := s.StoresFind(&pb.StoresFindOptions{Key: &pb.StoresKey{Floats: query}, TopK: int32(topK)})
	if err != nil {
		return nil, nil, nil, err
	}
	return store.UnwrapKeys(res.Keys), store.UnwrapValues(res.Values), res.Similarities, nil
}

// assertFTCreate returns a DoAndReturn func that verifies the FT.CREATE command
// carries the expected dimension and algorithm, then replies OK.
func assertFTCreate(dim int, algo string) func(context.Context, valkey.Completed) valkey.ValkeyResult {
	return func(_ context.Context, cmd valkey.Completed) valkey.ValkeyResult {
		toks := cmd.Commands()
		Expect(toks[0]).To(Equal("FT.CREATE"))
		Expect(toks).To(ContainElements("VECTOR", algo, "TYPE", "FLOAT32", "DIM", strconv.Itoa(dim), "DISTANCE_METRIC", "COSINE"))
		return mock.Result(mock.ValkeyString("OK"))
	}
}

// ftInfoReply builds a RESP2-shaped FT.INFO reply that mirrors Valkey Search's
// nesting: the VECTOR attribute carries its params under an `index` array whose
// `dimensions` key holds the DIM. dimValue is the message the parser must read
// back (integer or string-encoded), so both wire shapes can be exercised.
func ftInfoReply(dimValue valkey.ValkeyMessage) valkey.ValkeyMessage {
	vectorAttr := mock.ValkeyArray(
		mock.ValkeyString("identifier"), mock.ValkeyString(_vecField),
		mock.ValkeyString("attribute"), mock.ValkeyString(_vecField),
		mock.ValkeyString("type"), mock.ValkeyString("VECTOR"),
		mock.ValkeyString("index"), mock.ValkeyArray(
			mock.ValkeyString("capacity"), mock.ValkeyInt64(1000),
			mock.ValkeyString("dimensions"), dimValue,
			mock.ValkeyString("distance_metric"), mock.ValkeyString("COSINE"),
			mock.ValkeyString("data_type"), mock.ValkeyString("FLOAT32"),
		),
	)
	return mock.ValkeyArray(
		mock.ValkeyString("index_name"), mock.ValkeyString("idx:test"),
		mock.ValkeyString("attributes"), mock.ValkeyArray(vectorAttr),
		mock.ValkeyString("num_docs"), mock.ValkeyInt64(0),
	)
}

// ftSearchReply builds a RESP2-shaped FT.SEARCH reply: [total, key, attrs, ...]
// where attrs carries the returned vec/val/__score fields.
func ftSearchReply(prefix string, docs []ftDoc) valkey.ValkeyMessage {
	arr := []valkey.ValkeyMessage{mock.ValkeyInt64(int64(len(docs)))}
	for _, d := range docs {
		arr = append(arr, mock.ValkeyString(encodeKey(prefix, d.vec)))
		attrs := mock.ValkeyArray(
			mock.ValkeyString(_vecField), mock.ValkeyString(valkey.BinaryString(vecToBytes(d.vec))),
			mock.ValkeyString(_valField), mock.ValkeyString(d.val),
			mock.ValkeyString(_scoreField), mock.ValkeyString(strconv.FormatFloat(d.dist, 'f', -1, 64)),
		)
		arr = append(arr, attrs)
	}
	return mock.ValkeyArray(arr...)
}

var _ = Describe("namespace token", func() {
	It("is stable for the same namespace (so a persisted index is found again)", func() {
		Expect(nsToken("faces")).To(Equal(nsToken("faces")))
	})

	It("does not collide for namespaces that sanitize to the same token", func() {
		// "a b", "a/b" and "a:b" all sanitize to "a_b"; the hash suffix must
		// keep them distinct so two logically-distinct stores never share one
		// keyspace/index (the data-isolation guarantee).
		Expect(sanitize("a b")).To(Equal(sanitize("a/b")))
		Expect(nsToken("a b")).NotTo(Equal(nsToken("a/b")))
		Expect(nsToken("a/b")).NotTo(Equal(nsToken("a:b")))
	})

	It("keeps the sanitized part human-readable", func() {
		Expect(nsToken("faces")).To(HavePrefix("faces-"))
	})

	It("maps an empty namespace to a stable default token", func() {
		Expect(nsToken("")).To(HavePrefix("default-"))
		Expect(nsToken("")).To(Equal(nsToken("")))
	})
})

var _ = Describe("sanitize", func() {
	It("passes through allowed runes and folds the rest to '_'", func() {
		Expect(sanitize("Ok_9.-")).To(Equal("Ok_9.-"))
		Expect(sanitize("a b/c:d")).To(Equal("a_b_c_d"))
	})

	It("maps empty to 'default'", func() {
		Expect(sanitize("")).To(Equal("default"))
	})
})
