package contextfiles_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestContextFiles(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Studio context files")
}
