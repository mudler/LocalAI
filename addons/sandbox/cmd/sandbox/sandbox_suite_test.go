// SPDX-License-Identifier: MIT
package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSandboxCmd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sandbox Add-on Command Suite")
}
