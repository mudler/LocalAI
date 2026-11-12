// SPDX-License-Identifier: MIT
package clientdemo

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestClientDemo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Client example add-on")
}
