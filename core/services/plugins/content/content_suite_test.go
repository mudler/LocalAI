package content_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/plugins/content"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestContent(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Plugins content suite")
}

// tree writes files under a new folder and returns it. A value of "+x" at
// the start of a body makes the file executable; the marker is not written.
func tree(files map[string]string) string {
	dir := GinkgoT().TempDir()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(Succeed())
		mode := os.FileMode(0o644)
		if len(body) >= 2 && body[:2] == "+x" {
			mode, body = 0o755, body[2:]
		}
		Expect(os.WriteFile(full, []byte(body), mode)).To(Succeed())
		Expect(os.Chmod(full, mode)).To(Succeed())
	}
	return dir
}

func checkErr(dir string, lim content.Limits) error {
	_, err := content.Check(dir, lim)
	return err
}

func stringsReader(s string) *strings.Reader { return strings.NewReader(s) }
