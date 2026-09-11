package testutil_test

import (
	"testing"

	"github.com/mudler/LocalAI/core/services/testutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTestutil(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Test Utilities Suite")
}

// The container is shared per process now, so the isolation callers depend on
// comes from a database per call rather than from a server per call. That is
// the property 69 call sites across eleven packages assume without saying so,
// and nothing else in the tree asserts it.
var _ = Describe("SetupTestDB", func() {
	type row struct {
		ID int
	}

	It("hands back a database no other caller can see into", func() {
		first := testutil.SetupTestDB()
		second := testutil.SetupTestDB()

		Expect(first.Exec(`CREATE TABLE isolation_probe (id int)`).Error).To(Succeed())
		Expect(first.Exec(`INSERT INTO isolation_probe VALUES (1)`).Error).To(Succeed())

		var found []row
		err := second.Raw(`SELECT id FROM isolation_probe`).Scan(&found).Error
		Expect(err).To(HaveOccurred(),
			"two SetupTestDB calls landed on the same database, so every spec can now see every other spec's rows")

		// The second database must also be usable, not merely different: an
		// isolation check that passed because the second handle was broken
		// would prove nothing.
		Expect(second.Exec(`CREATE TABLE isolation_probe (id int)`).Error).To(Succeed())
	})

	It("hands back an empty database", func() {
		db := testutil.SetupTestDB()
		var tables int64
		Expect(db.Raw(
			`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'`,
		).Scan(&tables).Error).To(Succeed())
		Expect(tables).To(BeZero(), "a spec was handed a database another spec had already migrated")
	})
})
