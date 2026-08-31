package distributed_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Test database naming", Label("Distributed"), func() {
	Describe("sanitizeDBName", func() {
		It("lowercases and replaces characters Postgres will not accept unquoted", func() {
			Expect(sanitizeDBName("LocalAI-Test.Suite")).To(Equal("localai_test_suite"))
		})

		It("truncates to fit the 63-byte identifier limit with room for a suffix", func() {
			long := ""
			for i := 0; i < 100; i++ {
				long += "a"
			}
			Expect(len(sanitizeDBName(long))).To(Equal(50),
				"an over-long name must be truncated to exactly the 50-byte budget; <= 50 would also accept an empty name")
		})

		It("never produces an empty name", func() {
			Expect(sanitizeDBName("---")).ToNot(BeEmpty())
		})
	})

	Describe("replaceDBName", func() {
		It("swaps the database in a testcontainers DSN and keeps the query string", func() {
			dsn := "postgres://test:test@127.0.0.1:32768/localai_suite?sslmode=disable"
			Expect(replaceDBName(dsn, "spec_7")).To(Equal("postgres://test:test@127.0.0.1:32768/spec_7?sslmode=disable"))
		})
	})
})
