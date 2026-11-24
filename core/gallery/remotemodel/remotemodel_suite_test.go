package remotemodel_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRemoteModel(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Remote model suite")
}
