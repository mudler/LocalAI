// SPDX-License-Identifier: MIT
package tablesexport_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTablesExport(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tables CSV export across replicas")
}

var (
	paths  cluster.Paths
	tables []byte
)

var _ = BeforeSuite(func() {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	scratch, err := os.MkdirTemp("", "tables-export-bin-*")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(scratch) })
	build := func(env, out, pkg string, cgo bool, tags ...string) string {
		if p := os.Getenv(env); p != "" {
			return p
		}
		target := filepath.Join(scratch, out)
		args := []string{"build", "-p", "2"}
		if len(tags) > 0 {
			args = append(args, "-tags", tags[0])
		}
		cmd := exec.Command("go", append(args, "-o", target, pkg)...)
		cmd.Dir = root
		if cgo {
			cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
		}
		b, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(b))
		return target
	}
	paths.Fixture = build("FOUNDATION_FIXTURE", "fixture-addon", "./tests/addons/fixture/cmd", false, "auth")
	paths.Server = build("FOUNDATION_SERVER", "ui-test-server", "./tests/e2e-ui", false, "auth")
	paths.Mock = os.Getenv("FOUNDATION_MOCK")
	if paths.Mock == "" {
		paths.Mock = filepath.Join(root, "tests/e2e/mock-backend/mock-backend")
	}
	Expect(paths.Mock).To(BeAnExistingFile(), "build tests/e2e/mock-backend first (scripts/addons/prepare.sh --integration)")
	exe := build("TABLES_EXECUTABLE", "tables", "./addons/tables/cmd/tables", true)
	tables, err = os.ReadFile(exe)
	Expect(err).NotTo(HaveOccurred())
})

// start brings a cluster up in a fresh directory and takes it down at the end
// of the spec.
func start() *cluster.Cluster {
	dir, err := os.MkdirTemp("", "tables-export-cluster-*")
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
