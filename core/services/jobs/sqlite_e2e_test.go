//go:build auth

package jobs_test

import (
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/jobs"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Reproduces the #10506 caller chain: auth.InitDB(sqlite) -> jobs.NewJobStore,
// which previously failed with "no such function: pg_advisory_lock".
var _ = Describe("NewJobStore on a SQLite auth DB (#10506)", func() {
	It("migrates without pg_advisory_lock errors", func() {
		db, err := auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())

		store, err := jobs.NewJobStore(db)
		Expect(err).ToNot(HaveOccurred())
		Expect(store).ToNot(BeNil())
	})
})
