package sqlitedb

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSQLiteDB(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SQLiteDB Suite")
}
