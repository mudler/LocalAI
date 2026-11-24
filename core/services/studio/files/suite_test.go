package files_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFiles(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Studio files")
}
