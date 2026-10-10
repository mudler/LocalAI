package main

import (
	"math"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/store"
	"github.com/qdrant/go-client/qdrant"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("loadConfig", func() {
	It("applies defaults and parses every option", func() {
		cfg, err := loadConfig(&pb.ModelOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).To(Equal(Config{Host: "localhost", Port: 6334, Distance: qdrant.Distance_Cosine, RequestTimeout: 5 * time.Second}))

		GinkgoT().Setenv("QDRANT_STORE_TEST_KEY", "from-env")
		cfg, err = loadConfig(&pb.ModelOptions{Options: []string{"addr:[::1]", "api_key_env:QDRANT_STORE_TEST_KEY", "tls:true",
			"tls_skip_verify:true", "tls_ca_cert:/ca.pem", "collection:c", "distance_metric:dot", "request_timeout_ms:250"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).To(Equal(Config{Host: "::1", Port: 6334, APIKey: "from-env", UseTLS: true, TLSSkipVerify: true,
			TLSCACert: "/ca.pem", Collection: "c", Distance: qdrant.Distance_Dot, RequestTimeout: 250 * time.Millisecond}))
	})

	It("turns TLS on for tls_ca_cert and tls_skip_verify", func() {
		for _, o := range []string{"tls_ca_cert:/ca.pem", "tls_skip_verify:true"} {
			cfg, err := loadConfig(&pb.ModelOptions{Options: []string{o}})
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.UseTLS).To(BeTrue(), o)
		}
	})

	It("rejects malformed values", func() {
		for _, o := range []string{"addr:host:0", "addr:https://x:6333", "tls:maybe", "distance_metric:L2", "request_timeout_ms:soon"} {
			_, err := loadConfig(&pb.ModelOptions{Options: []string{o}})
			Expect(err).To(HaveOccurred(), o)
		}
	})
})

var _ = Describe("QdrantStore", func() {
	It("derives deterministic point IDs and distinct collection names", func() {
		Expect(pointID([]float32{1, 2})).To(Equal(pointID([]float32{1, 2})))
		Expect(pointID([]float32{1, 2})).NotTo(Equal(pointID([]float32{2, 1})))
		Expect(collectionName(Config{}, "store://faces")).To(HavePrefix("localai_faces-"))
		Expect(collectionName(Config{}, "store://a b")).NotTo(Equal(collectionName(Config{}, "store://a/b")))
		Expect(collectionName(Config{Collection: "mine"}, "store://faces")).To(Equal("mine"))
	})

	It("fails to load a non-store name, a bad tls_ca_cert or an unreachable server", func() {
		load := func(model string, opts ...string) error {
			return (&QdrantStore{}).Load(&pb.ModelOptions{Model: model, Options: opts})
		}
		Expect(load("llama-3.gguf")).To(MatchError(ContainSubstring("not a store namespace")))
		Expect(load("store://x", "tls_ca_cert:/nonexistent.pem")).To(MatchError(ContainSubstring("read tls_ca_cert")))
		Expect(load("store://x", "addr:127.0.0.1:1")).To(MatchError(ContainSubstring("127.0.0.1:1")))
	})

	It("rejects invalid input before calling Qdrant", func() {
		s := &QdrantStore{}
		set := func(keys [][]float32, values ...[]byte) error {
			return s.StoresSet(&pb.StoresSetOptions{Keys: store.WrapKeys(keys), Values: store.WrapValues(values)})
		}
		Expect(set(nil)).To(HaveOccurred())
		Expect(set([][]float32{{1, 2}})).To(HaveOccurred())
		Expect(set([][]float32{{float32(math.NaN()), 1}}, []byte("a"))).To(MatchError(ContainSubstring("must be finite")))
		Expect(s.StoresDelete(&pb.StoresDeleteOptions{})).To(HaveOccurred())
		_, err := s.StoresFind(&pb.StoresFindOptions{Key: &pb.StoresKey{Floats: []float32{1}}, TopK: 0})
		Expect(err).To(MatchError(ContainSubstring("topK")))
	})
})
