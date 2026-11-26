package toolmap_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestToolmap(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Plugins tool map suite")
}
