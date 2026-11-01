package workspace_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWorkspace(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Workspace Suite")
}
