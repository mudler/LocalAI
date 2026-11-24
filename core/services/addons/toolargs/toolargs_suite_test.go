// SPDX-License-Identifier: MIT
package toolargs

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestToolargs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tool arguments suite")
}
