// SPDX-License-Identifier: MIT
package tools

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTools(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Quantization tools suite")
}
