// SPDX-License-Identifier: MIT
package outputstore

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
)

func TestOutputStore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Durable music output store")
}
