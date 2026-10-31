package dberr_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/internal/dberr"
)

var _ = Describe("IsUniqueViolation", func() {
	DescribeTable("reads the error text of both databases",
		func(err error, want bool) {
			Expect(dberr.IsUniqueViolation(err)).To(Equal(want))
		},
		Entry("no error", nil, false),
		Entry("SQLite", errors.New("UNIQUE constraint failed: studio_runs.trigger_id, studio_runs.due_key"), true),
		Entry("PostgreSQL", errors.New(`ERROR: duplicate key value violates unique constraint "idx_x" (SQLSTATE 23505)`), true),
		Entry("another error", errors.New("database is locked"), false),
	)
})
