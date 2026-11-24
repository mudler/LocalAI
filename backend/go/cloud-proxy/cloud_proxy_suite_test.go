package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Ginkgo bootstrap. The other Test* functions in this package use
// raw testing.T and run independently; they coexist with Ginkgo
// specs registered via Describe / Context.
func TestCloudProxySpecs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cloud-proxy specs")
}
