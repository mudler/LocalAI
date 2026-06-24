package modelartifacts_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestModelArtifacts(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Model Artifacts Suite")
}
