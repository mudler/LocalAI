// SPDX-License-Identifier: MIT
package kvmd_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestKvmd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PiKVM add-on kvmd")
}
