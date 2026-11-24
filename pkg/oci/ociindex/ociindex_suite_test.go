package ociindex_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOCIIndex(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "OCI index fetch suite")
}
