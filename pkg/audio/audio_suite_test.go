package audio

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAudio(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Audio Suite")
}
