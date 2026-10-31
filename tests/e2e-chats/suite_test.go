package e2echats_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs drive the real local-ai binary over HTTP, the way a user does:
// the chats API, its stores on disk, the auth modes and the command line.
// Only the language model is faked (see fakellm_test.go). Run them with
// `make test-e2e-chats`.

func TestE2EChats(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Chats real-binary e2e suite")
}

var (
	binDir string // holds local-ai, mock-backend and cloud-proxy
	llm    *fakeLLM
)

// build compiles one binary unless an environment variable names a built one.
func build(env, name, pkg string, tags ...string) string {
	if p := os.Getenv(env); p != "" {
		return p
	}
	out := filepath.Join(binDir, name)
	args := []string{"build"}
	if len(tags) > 0 {
		args = append(args, "-tags", tags[0])
	}
	args = append(args, "-o", out, pkg)
	cmd := exec.Command("go", args...)
	cmd.Dir = filepath.Join("..", "..")
	cmd.Stderr = GinkgoWriter
	Expect(cmd.Run()).To(Succeed(), "go build "+pkg)
	return out
}

var (
	localAIBin, mockBackendBin, cloudProxyBin string
)

var _ = BeforeSuite(func() {
	var err error
	binDir, err = os.MkdirTemp("", "e2e-chats-bin-*")
	Expect(err).ToNot(HaveOccurred())
	// The auth tag is needed: the sqlite auth database is part of what is tested.
	localAIBin = build("E2E_LOCALAI_BIN", "local-ai", "./cmd/local-ai", "auth")
	mockBackendBin = build("E2E_MOCK_BACKEND", "mock-backend", "./tests/e2e/mock-backend")
	cloudProxyBin = build("E2E_CLOUD_PROXY", "cloud-proxy", "./backend/go/cloud-proxy")
	llm = newFakeLLM()
})

var _ = AfterSuite(func() {
	if llm != nil {
		llm.Close()
	}
	_ = os.RemoveAll(binDir)
})
