package credentials_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/credentials"
)

func TestCredentials(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Credentials test suite")
}

func noEnv(string) (string, bool) { return "", false }

func mustParse(doc string) *credentials.Store {
	s, err := credentials.Parse([]byte(doc), noEnv)
	Expect(err).NotTo(HaveOccurred())
	return s
}

// useStore installs a store as the process default for one spec.
func useStore(doc string) {
	prev := credentials.SetDefault(mustParse(doc))
	DeferCleanup(func() { credentials.SetDefault(prev) })
}

// isolateDockerConfig points every docker/podman config lookup at an empty
// temp dir, so credentials on the machine running the tests cannot satisfy or
// break a spec. It returns the directory that holds config.json.
func isolateDockerConfig() string {
	home := GinkgoT().TempDir()
	dockerDir := filepath.Join(home, ".docker")
	Expect(os.MkdirAll(dockerDir, 0o700)).To(Succeed())
	GinkgoT().Setenv("HOME", home)
	GinkgoT().Setenv("DOCKER_CONFIG", dockerDir)
	GinkgoT().Setenv("REGISTRY_AUTH_FILE", filepath.Join(home, "no-such-auth.json"))
	GinkgoT().Setenv("XDG_RUNTIME_DIR", home)
	return dockerDir
}
