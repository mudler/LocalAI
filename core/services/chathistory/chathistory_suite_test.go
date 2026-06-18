package chathistory_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestChatHistory(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ChatHistory test suite")
}
