package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSandboxAgent(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sandbox Agent Suite")
}
