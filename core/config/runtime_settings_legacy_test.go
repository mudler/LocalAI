package config

import (
	"encoding/json"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("legacy agent_pool_* runtime settings keys", func() {
	var calls [][]string
	BeforeEach(func() {
		calls = nil
		oldLog, oldOnce := logLegacyKeys, legacyKeysWarned
		logLegacyKeys = func(keys []string) { calls = append(calls, keys) }
		legacyKeysWarned = &sync.Once{}
		DeferCleanup(func() { logLegacyKeys, legacyKeysWarned = oldLog, oldOnce })
	})

	It("still loads and warns once, naming the keys", func() {
		var s RuntimeSettings
		Expect(json.Unmarshal([]byte(`{"agent_pool_enabled": true, "agent_pool_vector_engine": "chromem"}`), &s)).To(Succeed())
		Expect(s.CollectionsEnabled).To(HaveValue(BeTrue()))
		Expect(s.CollectionsVectorEngine).To(HaveValue(Equal("chromem")))
		Expect(calls).To(Equal([][]string{{"agent_pool_enabled", "agent_pool_vector_engine"}}))

		Expect(json.Unmarshal([]byte(`{"agent_pool_enabled": false}`), &s)).To(Succeed())
		Expect(calls).To(HaveLen(1), "a second read must not warn again")
	})

	It("does not warn for a file with only the new keys", func() {
		var s RuntimeSettings
		Expect(json.Unmarshal([]byte(`{"collections_enabled": true}`), &s)).To(Succeed())
		Expect(calls).To(BeEmpty())
	})
})
