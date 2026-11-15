// SPDX-License-Identifier: MIT
package search_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSearch(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Pirate Face search add-on")
}
