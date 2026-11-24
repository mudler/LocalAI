package sound

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSound(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sound Suite")
}
