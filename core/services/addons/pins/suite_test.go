// SPDX-License-Identifier: MIT
package pins

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPins(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Add-on pins suite")
}
