package ttscoord

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTtscoord(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ttscoord (realtime M5) Suite")
}
