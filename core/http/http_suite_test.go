package http_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	tmpdir   string
	modelDir string
)

func TestLocalAI(t *testing.T) {
	RegisterFailHandler(Fail)

	var err error
	tmpdir, err = os.MkdirTemp("", "")
	Expect(err).ToNot(HaveOccurred())
	modelDir = filepath.Join(tmpdir, "models")
	err = os.Mkdir(modelDir, 0750)
	Expect(err).ToNot(HaveOccurred())

	AfterSuite(func() {
		err := os.RemoveAll(tmpdir)
		Expect(err).ToNot(HaveOccurred())
	})

	RunSpecs(t, "LocalAI HTTP test suite")
}
