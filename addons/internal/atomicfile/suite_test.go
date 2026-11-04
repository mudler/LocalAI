// SPDX-License-Identifier: MIT
package atomicfile

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAtomicfile(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Atomicfile suite")
}
