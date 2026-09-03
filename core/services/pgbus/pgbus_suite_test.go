// SPDX-License-Identifier: MIT

package pgbus_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPgbus(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pgbus Suite")
}
