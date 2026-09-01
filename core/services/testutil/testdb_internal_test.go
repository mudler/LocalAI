package testutil

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

		fresh := openPool(dsn)
		defer closePool(fresh)
		var statementTimeout, lockTimeout string
		Expect(fresh.Raw("SHOW statement_timeout").Scan(&statementTimeout).Error).To(Succeed())
		Expect(fresh.Raw("SHOW lock_timeout").Scan(&lockTimeout).Error).To(Succeed())
		Expect(statementTimeout).To(Equal("0"),
			"a statement_timeout on the maintenance database reached the helper's own connection, so CREATE and DROP DATABASE are bounded by whatever a spec configured")
		Expect(lockTimeout).To(Equal("0"),
			"a lock_timeout on the maintenance database reached the helper's own connection")

		// And the thing the timeouts would actually abort still works while the
		// bound is in force. A 1ms statement_timeout is far below the 14-26ms a
		// CREATE DATABASE takes here, so this could not pass by being fast.
		Expect(SetupTestDB()).ToNot(BeNil())
	})
})
