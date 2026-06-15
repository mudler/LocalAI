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

	// Backend directory and the mock-backend binary path. Resolved by
	// findMockBackendBinary() from the same prebuilt artifact that
	// tests/e2e uses, so the suite no longer needs llama-cpp / transformers
	// / whisper / piper / stablediffusion-ggml builds.
	backendDir      string
	mockBackendPath string
)

// findMockBackendBinary locates the mock-backend binary built by
// `make build-mock-backend`. Mirrors the lookup used by
// tests/e2e/e2e_suite_test.go so both suites consume the same artifact.
func findMockBackendBinary() (string, bool) {
	candidates := []string{
		filepath.Join("..", "..", "tests", "e2e", "mock-backend", "mock-backend"),
		filepath.Join("tests", "e2e", "mock-backend", "mock-backend"),
		filepath.Join("..", "e2e", "mock-backend", "mock-backend"),
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "..", "..", "tests", "e2e", "mock-backend", "mock-backend"),
		)
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			abs, absErr := filepath.Abs(p)
			if absErr == nil {
				return abs, true
			}
			return p, true
		}
	}
	return "", false
}

func TestLocalAI(t *testing.T) {
	RegisterFailHandler(Fail)

	var err error
	tmpdir, err = os.MkdirTemp("", "")
	Expect(err).ToNot(HaveOccurred())
	modelDir = filepath.Join(tmpdir, "models")
	err = os.Mkdir(modelDir, 0750)
	Expect(err).ToNot(HaveOccurred())

	backendDir = filepath.Join(tmpdir, "backends")
	Expect(os.Mkdir(backendDir, 0750)).To(Succeed())

	if p, ok := findMockBackendBinary(); ok {
		mockBackendPath = p
		// Make sure it's executable for the path the suite owns.
		_ = os.Chmod(mockBackendPath, 0755)
	}

	AfterSuite(func() {
		err := os.RemoveAll(tmpdir)
		Expect(err).ToNot(HaveOccurred())
	})

	RunSpecs(t, "LocalAI HTTP test suite")
}
