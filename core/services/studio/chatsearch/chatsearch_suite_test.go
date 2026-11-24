package chatsearch_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestChatSearch(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Chat search suite")
}
