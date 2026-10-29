package studio

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStudioEndpoints(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Studio endpoints")
}
