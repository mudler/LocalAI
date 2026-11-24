package pairclient_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPairclient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Pairclient Suite")
}
