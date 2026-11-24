// SPDX-License-Identifier: MIT
package hostslots_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHostSlots(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Host UI slots contract")
}
