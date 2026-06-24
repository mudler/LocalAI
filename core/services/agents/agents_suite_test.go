package agents

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAgents(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Agents test suite")
}
