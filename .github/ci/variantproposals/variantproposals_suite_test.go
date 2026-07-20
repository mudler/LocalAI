package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVariantProposals(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "gallery variant proposals")
}
