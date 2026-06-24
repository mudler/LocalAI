package chat

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestChat(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Chat Suite")
}
