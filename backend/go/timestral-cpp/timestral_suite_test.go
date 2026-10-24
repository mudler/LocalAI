package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTimestralCpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "timestral-cpp Backend Suite")
}
