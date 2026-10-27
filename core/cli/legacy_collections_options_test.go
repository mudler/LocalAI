package cli

import (
	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The collections settings used to carry the names of the old agent pool. The
// old names still work, as flags and as environment variables, and each one
// that is used is named in a deprecation warning. The new name wins.
var _ = Describe("collections options and their legacy names", func() {
	parse := func(args ...string) *RunCMD {
		var r RunCMD
		k, err := kong.New(&r, kong.Vars{"basepath": GinkgoT().TempDir(), "galleries": "[]", "backends": "[]", "generatedcontentpath": DefaultGeneratedContentPath(), "uploadpath": DefaultUploadPath()})
		Expect(err).ToNot(HaveOccurred())
		_, err = k.Parse(args)
		Expect(err).ToNot(HaveOccurred())
		return &r
	}

	It("reads the new flags", func() {
		r := parse("--collections-vector-engine=postgres", "--collections-database-url=pg://x", "--collections-max-chunking-size=900", "--disable-collections")
		Expect(r.CollectionsVectorEngine).To(Equal("postgres"))
		Expect(r.CollectionsDatabaseURL).To(Equal("pg://x"))
		Expect(r.CollectionsMaxChunkingSize).To(Equal(900))
		Expect(r.DisableCollections).To(BeTrue())
	})

	It("reads the new environment variables", func() {
		GinkgoT().Setenv("LOCALAI_COLLECTIONS_STATE_DIR", "/s")
		GinkgoT().Setenv("LOCALAI_COLLECTIONS_DB_PATH", "/db")
		GinkgoT().Setenv("LOCALAI_DISABLE_COLLECTIONS", "true")
		r := parse()
		Expect(r.CollectionsStateDir).To(Equal("/s"))
		Expect(r.CollectionsDBPath).To(Equal("/db"))
		Expect(r.DisableCollections).To(BeTrue())
	})

	DescribeTable("an old environment variable still sets the new option and is named",
		func(env, value string, check func(*RunCMD)) {
			GinkgoT().Setenv(env, value)
			r := parse()
			Expect(r.resolveLegacyCollections()).To(ConsistOf(env))
			check(r)
		},
		// Master's one switch turned off the whole agent pool, knowledge base included.
		Entry("disable", "LOCALAI_DISABLE_AGENTS", "true", func(r *RunCMD) {
			Expect(r.DisableCollections).To(BeTrue())
			Expect(r.DisableChats).To(BeTrue())
		}),
		Entry("api url", "LOCALAI_AGENT_POOL_API_URL", "http://a", func(r *RunCMD) { Expect(r.CollectionsAPIURL).To(Equal("http://a")) }),
		Entry("api key", "LOCALAI_AGENT_POOL_API_KEY", "k", func(r *RunCMD) { Expect(r.CollectionsAPIKey).To(Equal("k")) }),
		Entry("default model", "LOCALAI_AGENT_POOL_DEFAULT_MODEL", "m", func(r *RunCMD) { Expect(r.CollectionsDefaultModel).To(Equal("m")) }),
		Entry("state dir", "LOCALAI_AGENT_POOL_STATE_DIR", "/s", func(r *RunCMD) { Expect(r.CollectionsStateDir).To(Equal("/s")) }),
		Entry("vector engine", "LOCALAI_AGENT_POOL_VECTOR_ENGINE", "postgres", func(r *RunCMD) { Expect(r.CollectionsVectorEngine).To(Equal("postgres")) }),
		Entry("embedding model", "LOCALAI_AGENT_POOL_EMBEDDING_MODEL", "e", func(r *RunCMD) { Expect(r.CollectionsEmbeddingModel).To(Equal("e")) }),
		Entry("database url", "LOCALAI_AGENT_POOL_DATABASE_URL", "pg://x", func(r *RunCMD) { Expect(r.CollectionsDatabaseURL).To(Equal("pg://x")) }),
		Entry("max chunking size", "LOCALAI_AGENT_POOL_MAX_CHUNKING_SIZE", "123", func(r *RunCMD) { Expect(r.CollectionsMaxChunkingSize).To(Equal(123)) }),
		Entry("chunk overlap", "LOCALAI_AGENT_POOL_CHUNK_OVERLAP", "7", func(r *RunCMD) { Expect(r.CollectionsChunkOverlap).To(Equal(7)) }),
		Entry("db path", "LOCALAI_AGENT_POOL_COLLECTION_DB_PATH", "/db", func(r *RunCMD) { Expect(r.CollectionsDBPath).To(Equal("/db")) }),
	)

	It("accepts the old flags", func() {
		r := parse("--disable-agents", "--agent-pool-vector-engine=postgres")
		Expect(r.resolveLegacyCollections()).To(ConsistOf("LOCALAI_DISABLE_AGENTS", "LOCALAI_AGENT_POOL_VECTOR_ENGINE"))
		Expect(r.DisableCollections).To(BeTrue())
		Expect(r.CollectionsVectorEngine).To(Equal("postgres"))
	})

	It("reads the switches of the chats and the agent job settings", func() {
		r := parse("--disable-chats", "--disable-triggers", "--disable-connectors", "--agent-job-retention-days=7", "--mcp-ci-job-timeout=2m")
		Expect(r.DisableChats).To(BeTrue())
		Expect(r.DisableTriggers).To(BeTrue())
		Expect(r.DisableConnectors).To(BeTrue())
		Expect(r.AgentJobRetentionDays).To(Equal(7))
		Expect(r.MCPCIJobTimeout.String()).To(Equal("2m0s"))
		Expect(r.DisableCollections).To(BeFalse(), "the chats switch leaves the collections on")
	})

	It("keeps the collections on when only the chats are switched off", func() {
		GinkgoT().Setenv("LOCALAI_DISABLE_CHATS", "true")
		r := parse()
		Expect(r.DisableChats).To(BeTrue())
		Expect(r.DisableCollections).To(BeFalse())
	})

	It("lets the new name win when both are set", func() {
		GinkgoT().Setenv("LOCALAI_COLLECTIONS_VECTOR_ENGINE", "chromem")
		GinkgoT().Setenv("LOCALAI_AGENT_POOL_VECTOR_ENGINE", "postgres")
		r := parse()
		Expect(r.resolveLegacyCollections()).To(ConsistOf("LOCALAI_AGENT_POOL_VECTOR_ENGINE"))
		Expect(r.CollectionsVectorEngine).To(Equal("chromem"))
	})

	It("names nothing when no old name is used", func() {
		Expect(parse().resolveLegacyCollections()).To(BeEmpty())
	})
})
