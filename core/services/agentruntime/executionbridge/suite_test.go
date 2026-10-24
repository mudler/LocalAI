// SPDX-License-Identifier: MIT
package executionbridge

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestExecutionBridge(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Execution bridge model")
}
