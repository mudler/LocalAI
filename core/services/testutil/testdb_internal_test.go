package testutil

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// These are white-box on purpose: the property is about the connection this
// package makes for itself, which no caller can reach.
var _ = Describe("the maintenance connection", func() {
	It("cannot be bounded by a timeout set on the maintenance database", func() {
		// The leak this pins is not hypothetical. Two advisory-lock specs named
		// a database by literal, and once the helper started handing out
		// per-spec databases those ALTERs landed on the maintenance database
		// instead, so every CREATE DATABASE and every DROP ... WITH (FORCE) ran
		// under a 300ms bound. A CREATE that trips it fails another spec's
		// setup; a DROP that trips it is swallowed and leaks a database. Both
		// are load-dependent single-spec failures, which is the exact shape
		// this helper was rewritten to remove.
		dsn := sharedPostgres()

		var maintenance string
		func() {
			probe := openPool(dsn)
			defer closePool(probe)
			Expect(probe.Raw("SELECT current_database()").Scan(&maintenance).Error).To(Succeed())
		}()
		Expect(maintenance).ToNot(BeEmpty())

		// Impose the leak, then assert a fresh maintenance connection is
		// unaffected. Reset first so a failure below cannot leave the bound in
		// place for the rest of the suite.
		DeferCleanup(func() {
			reset := openPool(dsn)
			defer closePool(reset)
			Expect(reset.Exec(fmt.Sprintf("ALTER DATABASE %q RESET statement_timeout", maintenance)).Error).To(Succeed())
			Expect(reset.Exec(fmt.Sprintf("ALTER DATABASE %q RESET lock_timeout", maintenance)).Error).To(Succeed())
		})
		func() {
			impose := openPool(dsn)
			defer closePool(impose)
			Expect(impose.Exec(fmt.Sprintf("ALTER DATABASE %q SET statement_timeout = '1ms'", maintenance)).Error).To(Succeed())
			Expect(impose.Exec(fmt.Sprintf("ALTER DATABASE %q SET lock_timeout = '1ms'", maintenance)).Error).To(Succeed())
		}()

		// The bound is delivered before the first statement, so the check
		// below is also the connection's first statement. That ordering is the
		// point: clearing the bound with a SET would be circular, because the
		// clearing statement inherits the bound it is clearing and can be
		// aborted by it with 57014. There is no such bootstrap statement now.
		fresh := openPool(dsn)
		defer closePool(fresh)
		var statementTimeout, lockTimeout string
		Expect(fresh.Raw("SHOW statement_timeout").Scan(&statementTimeout).Error).To(Succeed())
		Expect(fresh.Raw("SHOW lock_timeout").Scan(&lockTimeout).Error).To(Succeed())
		Expect(statementTimeout).To(Equal("0"),
			"a statement_timeout on the maintenance database reached the helper's own connection, so CREATE and DROP DATABASE are bounded by whatever a spec configured")
		Expect(lockTimeout).To(Equal("0"),
			"a lock_timeout on the maintenance database reached the helper's own connection")

		// A control, and the reason this spec is not a race. Clearing the bound
		// with a statement is circular: the clearing statement runs on a
		// connection that has already inherited the bound. Whether that
		// particular statement exceeds 1ms is a matter of load, which makes the
		// defect an intermittent one; whether the FIRST statement on a plain
		// connection is bounded at all is not. So the control asks the
		// deterministic question, with a first statement that certainly exceeds
		// the bound.
		func() {
			plain, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
			Expect(err).ToNot(HaveOccurred())
			defer closePool(plain)
			err = plain.Exec("SELECT pg_sleep(0.05)").Error
			Expect(err).To(HaveOccurred(),
				"the imposed bound does not reach a fresh connection's first statement, so this spec's subject is not actually under test")
			Expect(err.Error()).To(ContainSubstring("57014"),
				"expected the imposed statement_timeout to abort this, got something else")
		}()

		// The same first statement on a maintenance connection is unbounded.
		Expect(fresh.Exec("SELECT pg_sleep(0.05)").Error).To(Succeed())

		// And this is the assertion that says WHY, which is the part a
		// statement-based clearing cannot satisfy. reset_val is the value the
		// session would fall back to, that is, the value that was in force when
		// the connection started, before it could run anything. Clearing the
		// bound with `SET statement_timeout = 0` leaves reset_val at the
		// database's 1ms: the session is unbounded only because a statement
		// said so, and that statement ran under the 1ms bound and can be
		// aborted by it. Delivering it as a startup option makes the connection
		// unbounded with no statement in between, which is the difference
		// between a fix and a wider margin.
		var resetVal string
		Expect(fresh.Raw(
			"SELECT reset_val FROM pg_settings WHERE name = 'statement_timeout'",
		).Scan(&resetVal).Error).To(Succeed())
		Expect(resetVal).To(Equal("0"),
			"the maintenance connection started under a %s bound and cleared it with a statement, so the clearing statement itself runs under the bound it is clearing", resetVal)

		// And the operation the bound would abort still works while it is in
		// force. 1ms is far below the 14-26ms a CREATE DATABASE takes here, so
		// this cannot pass by being fast.
		Expect(SetupTestDB()).ToNot(BeNil())
	})
})
