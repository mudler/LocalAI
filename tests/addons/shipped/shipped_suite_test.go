// SPDX-License-Identifier: MIT
package shipped_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestShippedAddons(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Shipped add-ons on the real server")
}
