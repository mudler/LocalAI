// SPDX-License-Identifier: MIT
package mask_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMask(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kubernetes add-on masking")
}
