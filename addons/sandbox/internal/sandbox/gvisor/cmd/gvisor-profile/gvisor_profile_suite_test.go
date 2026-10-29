package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGvisorProfile(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "gvisor-profile suite")
}
