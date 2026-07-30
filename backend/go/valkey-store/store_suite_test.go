package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestValkeyStore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "valkey-store test suite")
}
