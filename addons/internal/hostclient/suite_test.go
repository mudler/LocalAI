// SPDX-License-Identifier: MIT
package hostclient

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHostclient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Hostclient suite")
}
