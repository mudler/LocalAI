package textindex_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTextIndex(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Studio file text index")
}
