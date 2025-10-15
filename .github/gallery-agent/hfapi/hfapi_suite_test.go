package hfapi_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHfapi(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HuggingFace API Suite")
}
