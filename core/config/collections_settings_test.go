package config_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("collections runtime settings", func() {
	It("reads the collections_* keys", func() {
		var s config.RuntimeSettings
		Expect(json.Unmarshal([]byte(`{"collections_enabled": false, "collections_vector_engine": "postgres", "collections_max_chunking_size": 800}`), &s)).To(Succeed())
		Expect(*s.CollectionsEnabled).To(BeFalse())
		Expect(*s.CollectionsVectorEngine).To(Equal("postgres"))
		Expect(*s.CollectionsMaxChunkingSize).To(Equal(800))
	})

	It("still reads the old agent_pool_* keys", func() {
		var s config.RuntimeSettings
		Expect(json.Unmarshal([]byte(`{"agent_pool_enabled": false, "agent_pool_default_model": "m", "agent_pool_embedding_model": "e",
			"agent_pool_max_chunking_size": 5, "agent_pool_chunk_overlap": 6, "agent_pool_collection_db_path": "/p",
			"agent_pool_vector_engine": "postgres", "agent_pool_database_url": "pg://x"}`), &s)).To(Succeed())
		Expect(*s.CollectionsEnabled).To(BeFalse())
		Expect(*s.CollectionsDefaultModel).To(Equal("m"))
		Expect(*s.CollectionsEmbeddingModel).To(Equal("e"))
		Expect(*s.CollectionsMaxChunkingSize).To(Equal(5))
		Expect(*s.CollectionsChunkOverlap).To(Equal(6))
		Expect(*s.CollectionsDBPath).To(Equal("/p"))
		Expect(*s.CollectionsVectorEngine).To(Equal("postgres"))
		Expect(*s.CollectionsDatabaseURL).To(Equal("pg://x"))
	})

	It("prefers the new key when both are present", func() {
		var s config.RuntimeSettings
		Expect(json.Unmarshal([]byte(`{"agent_pool_vector_engine": "postgres", "collections_vector_engine": "chromem"}`), &s)).To(Succeed())
		Expect(*s.CollectionsVectorEngine).To(Equal("chromem"))
	})

	It("writes only the new keys", func() {
		v := "chromem"
		out, err := json.Marshal(config.RuntimeSettings{CollectionsVectorEngine: &v})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).To(ContainSubstring("collections_vector_engine"))
		Expect(string(out)).ToNot(ContainSubstring("agent_pool"))
	})
})
