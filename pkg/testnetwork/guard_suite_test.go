// SPDX-License-Identifier: MIT

package testnetwork_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestNetworkGuard(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Test network guard suite")
}
