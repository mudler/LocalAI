package triggers_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTriggers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Triggers")
}
