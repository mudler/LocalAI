package guestagent

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGuestagent(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Guestagent Suite")
}
