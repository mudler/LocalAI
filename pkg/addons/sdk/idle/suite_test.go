// SPDX-License-Identifier: MIT
package idle

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestIdle(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Idle suite")
}
