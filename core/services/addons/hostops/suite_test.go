// SPDX-License-Identifier: MIT
package hostops

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHostops(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Add-on host operations suite")
}
