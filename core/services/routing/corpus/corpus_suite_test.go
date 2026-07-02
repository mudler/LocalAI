package corpus_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCorpus(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Router corpus manager suite")
}
