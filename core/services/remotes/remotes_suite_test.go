package remotes_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRemotes(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Remotes test suite")
}
