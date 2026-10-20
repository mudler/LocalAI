// SPDX-License-Identifier: MIT
package foundation_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFoundation(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Add-ons foundation acceptance")
}

// paths are the executables every cluster of this suite runs.
var paths cluster.Paths

// repoRoot is the root of this repository.
var repoRoot string

var _ = BeforeSuite(func() {
	_, file, _, _ := runtime.Caller(0)
	repoRoot = filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	scratch, err := os.MkdirTemp("", "foundation-bin-*")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(scratch) })
	build := func(env, out, pkg string) string {
		if p := os.Getenv(env); p != "" {
			return p
		}
		target := filepath.Join(scratch, out)
		cmd := exec.Command("go", "build", "-p", "2", "-tags", "auth", "-o", target, pkg)
		cmd.Dir = repoRoot
		b, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(b))
		return target
	}
	paths.Fixture = build("FOUNDATION_FIXTURE", "fixture-addon", "./tests/addons/fixture/cmd")
	paths.Server = build("FOUNDATION_SERVER", "ui-test-server", "./tests/e2e-ui")
	paths.Mock = os.Getenv("FOUNDATION_MOCK")
	if paths.Mock == "" {
		paths.Mock = filepath.Join(repoRoot, "tests/e2e/mock-backend/mock-backend")
	}
	Expect(paths.Mock).To(BeAnExistingFile(), "build tests/e2e/mock-backend first (scripts/addons/prepare.sh --integration)")
})

// start brings a cluster up in a fresh directory and takes it down at the end
// of the spec container. Package directories are read-only, so the cleanup
// restores permissions first.
func start() *cluster.Cluster {
	dir, err := os.MkdirTemp("", "foundation-cluster-*")
	Expect(err).NotTo(HaveOccurred())
	c, err := cluster.New(dir, paths)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		c.Down()
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
			if e == nil && d.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
		_ = os.RemoveAll(dir)
	})
	return c
}

// ---- small API helpers over the HTTP client ----

type apiError struct {
	Error struct {
		Type        string `json:"type"`
		Message     string `json:"message"`
		OperationID string `json:"operationId"`
	} `json:"error"`
}

var counter int

func nextID(prefix string) string {
	counter++
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano()%1_000_000, counter)
}

func dispatch(c cluster.Client, request map[string]any) *cluster.Response {
	if _, ok := request["operationId"]; !ok {
		request["operationId"] = nextID("op")
	}
	if _, ok := request["arguments"]; !ok {
		request["arguments"] = map[string]any{}
	}
	r, err := c.Do("POST", "/api/addons/dispatch", request, nil)
	Expect(err).NotTo(HaveOccurred())
	return r
}

type app struct {
	ID         string   `json:"id"`
	Generation uint64   `json:"generation"`
	Enabled    bool     `json:"enabled"`
	State      string   `json:"state"`
	Tools      []string `json:"tools"`
	HasUI      bool     `json:"hasUI"`
	UI         *struct {
		Entry  string   `json:"entry"`
		CSS    []string `json:"css"`
		Assets *struct {
			Digest string `json:"digest"`
			Base   string `json:"base"`
		} `json:"assets"`
	} `json:"ui"`
	Version string `json:"version"`
}

func apps(c cluster.Client) (int, []app) {
	r, err := c.Do("GET", "/api/addons/apps", nil, nil)
	Expect(err).NotTo(HaveOccurred())
	var out struct {
		Apps []app `json:"apps"`
	}
	if r.Status == 200 {
		Expect(r.JSON(&out)).To(Succeed(), r.String())
	}
	return r.Status, out.Apps
}

func appOf(c cluster.Client, id string) *app {
	status, list := apps(c)
	Expect(status).To(Equal(200))
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// settle polls a condition with a deadline; the condition is a fact of the
// system under test, never a stand-in for synchronisation.
func settle(what string, timeout time.Duration, f func() bool) {
	GinkgoHelper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	Fail("timed out waiting for " + what)
}

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// hashTree digests a file or directory so a spec can prove it did not change.
func hashTree(path string) string {
	h := sha256.New()
	info, err := os.Stat(path)
	Expect(err).NotTo(HaveOccurred())
	if !info.IsDir() {
		b, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		return sha(b)
	}
	Expect(filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
		if e != nil || d.IsDir() {
			return e
		}
		rel, _ := filepath.Rel(path, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00", rel, sha(b))
		return nil
	})).To(Succeed())
	return hex.EncodeToString(h.Sum(nil))
}
