package agentworker_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAgentWorker(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Agent worker control plane")
}
