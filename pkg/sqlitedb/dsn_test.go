package sqlitedb

import (
	"net/url"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// parseDSN splits a "base?query" DSN into its base and decoded query values so
// assertions don't depend on url.Values.Encode()'s key ordering.
func parseDSN(dsn string) (string, url.Values) {
	base := dsn
	rawQuery := ""
	if before, after, ok := strings.Cut(dsn, "?"); ok {
		base = before
		rawQuery = after
	}
	values, err := url.ParseQuery(rawQuery)
	Expect(err).ToNot(HaveOccurred())
	return base, values
}

var _ = Describe("buildSQLiteDSN", func() {
	It("adds busy_timeout and txlock to a plain file path", func() {
		base, values := parseDSN(buildSQLiteDSN("/data/database.db"))
		Expect(base).To(Equal("/data/database.db"))
		Expect(values["_pragma"]).To(ContainElement("busy_timeout(10000)"))
		Expect(values.Get("_txlock")).To(Equal("immediate"))
	})

	It("adds pragmas to an in-memory database", func() {
		base, values := parseDSN(buildSQLiteDSN(":memory:"))
		Expect(base).To(Equal(":memory:"))
		Expect(values["_pragma"]).To(ContainElement("busy_timeout(10000)"))
		Expect(values.Get("_txlock")).To(Equal("immediate"))
	})

	It("preserves an existing query string", func() {
		base, values := parseDSN(buildSQLiteDSN("/data/database.db?cache=shared"))
		Expect(base).To(Equal("/data/database.db"))
		Expect(values.Get("cache")).To(Equal("shared"))
		Expect(values["_pragma"]).To(ContainElement("busy_timeout(10000)"))
		Expect(values.Get("_txlock")).To(Equal("immediate"))
	})

	It("does not override a caller-supplied busy_timeout or txlock", func() {
		_, values := parseDSN(buildSQLiteDSN("/data/database.db?_busy_timeout=1000&_txlock=deferred"))
		Expect(values["_pragma"]).To(ContainElement("busy_timeout(1000)"))
		Expect(values["_pragma"]).NotTo(ContainElement("busy_timeout(10000)"), "busy_timeout should be set once, to the caller's value")
		Expect(values["_txlock"]).To(HaveLen(1), "_txlock should not be duplicated")
		Expect(values.Get("_txlock")).To(Equal("deferred"))
	})

	It("turns the options of the previous driver into pragmas", func() {
		_, values := parseDSN(buildSQLiteDSN("/data/database.db?_busy_timeout=2000&_foreign_keys=on"))
		Expect(values["_pragma"]).To(ContainElements("busy_timeout(2000)", "foreign_keys(on)"))
		Expect(values["_pragma"]).NotTo(ContainElement("foreign_keys(1)"))
		Expect(values.Has("_busy_timeout")).To(BeFalse())
	})

	It("turns on WAL, foreign keys and NORMAL synchronous for a file", func() {
		_, values := parseDSN(buildSQLiteDSN("/data/database.db"))
		Expect(values["_pragma"]).To(ContainElements("journal_mode(WAL)", "foreign_keys(1)", "synchronous(NORMAL)"))
		Expect(values["_pragma"][0]).To(Equal("busy_timeout(10000)"))
	})

	It("keeps busy_timeout first, also when the caller sets it", func() {
		_, values := parseDSN(buildSQLiteDSN("/data/database.db?_foreign_keys=on&_busy_timeout=2000"))
		Expect(values["_pragma"][0]).To(Equal("busy_timeout(2000)"))
	})

	It("does not ask WAL for an in-memory database", func() {
		_, values := parseDSN(buildSQLiteDSN(":memory:"))
		Expect(values["_pragma"]).NotTo(ContainElement("journal_mode(WAL)"))
	})

	It("lets the caller choose the journal mode", func() {
		_, values := parseDSN(buildSQLiteDSN("/data/database.db?_pragma=journal_mode(DELETE)"))
		Expect(values["_pragma"]).To(ContainElement("journal_mode(DELETE)"))
		Expect(values["_pragma"]).NotTo(ContainElement("journal_mode(WAL)"))
	})
})
