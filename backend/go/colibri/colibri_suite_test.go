//go:build !windows

package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestColibri(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "colibri backend test suite")
}
