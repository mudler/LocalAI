//go:build auth

package auth

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
	if i := strings.IndexByte(dsn, '?'); i >= 0 {
		base = dsn[:i]
		rawQuery = dsn[i+1:]
	}
	values, err := url.ParseQuery(rawQuery)
	Expect(err).ToNot(HaveOccurred())
	return base, values
}

var _ = Describe("buildSQLiteDSN", func() {
	It("adds busy_timeout and txlock to a plain file path", func() {
		base, values := parseDSN(buildSQLiteDSN("/data/database.db"))
		Expect(base).To(Equal("/data/database.db"))
		Expect(values.Get("_busy_timeout")).To(Equal("5000"))
		Expect(values.Get("_txlock")).To(Equal("immediate"))
	})

	It("adds pragmas to an in-memory database", func() {
		base, values := parseDSN(buildSQLiteDSN(":memory:"))
		Expect(base).To(Equal(":memory:"))
		Expect(values.Get("_busy_timeout")).To(Equal("5000"))
		Expect(values.Get("_txlock")).To(Equal("immediate"))
	})

	It("preserves an existing query string", func() {
		base, values := parseDSN(buildSQLiteDSN("/data/database.db?cache=shared"))
		Expect(base).To(Equal("/data/database.db"))
		Expect(values.Get("cache")).To(Equal("shared"))
		Expect(values.Get("_busy_timeout")).To(Equal("5000"))
		Expect(values.Get("_txlock")).To(Equal("immediate"))
	})

	It("does not override a caller-supplied busy_timeout or txlock", func() {
		_, values := parseDSN(buildSQLiteDSN("/data/database.db?_busy_timeout=1000&_txlock=deferred"))
		Expect(values["_busy_timeout"]).To(HaveLen(1), "_busy_timeout should not be duplicated")
		Expect(values.Get("_busy_timeout")).To(Equal("1000"))
		Expect(values["_txlock"]).To(HaveLen(1), "_txlock should not be duplicated")
		Expect(values.Get("_txlock")).To(Equal("deferred"))
	})
})
