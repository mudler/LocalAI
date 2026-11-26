package fetch_test

import (
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFetch(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Plugins fetch suite")
}

// repo is a git repository fixture served over file://.
type repo struct{ dir string }

// gitIn runs git in dir with a clean configuration and returns its output.
func gitIn(dir string, env []string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0",
	}, env...)
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

func newRepo() *repo {
	dir := GinkgoT().TempDir()
	gitIn(dir, nil, "init", "-q", "-b", "main")
	// A server must allow partial clones and fetches by SHA; GitHub does.
	gitIn(dir, nil, "config", "uploadpack.allowFilter", "true")
	gitIn(dir, nil, "config", "uploadpack.allowAnySHA1InWant", "true")
	return &repo{dir: dir}
}

func (r *repo) url() string { return "file://" + r.dir }

// commit writes files (a nil value removes one) and commits them.
func (r *repo) commit(files map[string][]byte) string {
	for p, body := range files {
		full := filepath.Join(r.dir, filepath.FromSlash(p))
		if body == nil {
			Expect(os.Remove(full)).To(Succeed())
			continue
		}
		Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(Succeed())
		Expect(os.WriteFile(full, body, 0o644)).To(Succeed())
	}
	gitIn(r.dir, nil, "add", "-A")
	gitIn(r.dir, nil, "-c", "user.email=t@example.test", "-c", "user.name=t", "commit", "-q", "-m", "c")
	return gitIn(r.dir, nil, "rev-parse", "HEAD")
}

// blobsUnder lists the blob IDs of the tree of sha under prefix.
func (r *repo) blobsUnder(sha, prefix string) map[string]bool {
	out := map[string]bool{}
	for _, l := range strings.Split(gitIn(r.dir, nil, "ls-tree", "-r", sha, "--", prefix), "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 && f[1] == "blob" {
			out[f[2]] = true
		}
	}
	return out
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// scratchEntries lists what the fetches left in the scratch root, without
// the empty home folder of git.
func scratchEntries(root string) []string {
	entries, err := os.ReadDir(root)
	Expect(err).NotTo(HaveOccurred())
	var out []string
	for _, e := range entries {
		if e.Name() != ".home" {
			out = append(out, e.Name())
		}
	}
	return out
}
