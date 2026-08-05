package config

import (
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RouterKNNConfig validation", func() {
	validate := func(knn RouterKNNConfig) error {
		cfg := poolingTestConfig("", 0)
		cfg.Backend = "transformers"
		cfg.Router.KNN = &knn
		_, err := cfg.Validate()
		return err
	}

	It("accepts defaults and documented boundaries", func() {
		for _, knn := range []RouterKNNConfig{
			{},
			{K: 1, SimilarityThreshold: math.SmallestNonzeroFloat64, VoteThreshold: math.SmallestNonzeroFloat64},
			{K: RouterKNNMaxK, SimilarityThreshold: 1, VoteThreshold: 1},
		} {
			Expect(validate(knn)).To(Succeed())
		}
	})

	DescribeTable("rejects an invalid bound",
		func(knn RouterKNNConfig, field string) {
			err := validate(knn)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(field))
		},
		Entry("negative k", RouterKNNConfig{K: -1}, "router.knn.k"),
		Entry("k above the operational cap", RouterKNNConfig{K: RouterKNNMaxK + 1}, "router.knn.k"),
		Entry("negative similarity", RouterKNNConfig{SimilarityThreshold: -math.SmallestNonzeroFloat64}, "router.knn.similarity_threshold"),
		Entry("similarity above one", RouterKNNConfig{SimilarityThreshold: math.Nextafter(1, 2)}, "router.knn.similarity_threshold"),
		Entry("NaN similarity", RouterKNNConfig{SimilarityThreshold: math.NaN()}, "router.knn.similarity_threshold"),
		Entry("positive infinite similarity", RouterKNNConfig{SimilarityThreshold: math.Inf(1)}, "router.knn.similarity_threshold"),
		Entry("negative infinite similarity", RouterKNNConfig{SimilarityThreshold: math.Inf(-1)}, "router.knn.similarity_threshold"),
		Entry("negative vote", RouterKNNConfig{VoteThreshold: -math.SmallestNonzeroFloat64}, "router.knn.vote_threshold"),
		Entry("vote above one", RouterKNNConfig{VoteThreshold: math.Nextafter(1, 2)}, "router.knn.vote_threshold"),
		Entry("NaN vote", RouterKNNConfig{VoteThreshold: math.NaN()}, "router.knn.vote_threshold"),
		Entry("positive infinite vote", RouterKNNConfig{VoteThreshold: math.Inf(1)}, "router.knn.vote_threshold"),
		Entry("negative infinite vote", RouterKNNConfig{VoteThreshold: math.Inf(-1)}, "router.knn.vote_threshold"),
	)
})
