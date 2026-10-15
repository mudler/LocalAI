// SPDX-License-Identifier: MIT

// Package tablesacceptance_test is the end-to-end acceptance of the Tables
// add-on. It starts the foundation cluster (one owner and two forwarding
// frontends as separate server processes over the real mTLS carrier, one
// shared account store) and installs the real packaged Tables from a local
// catalog: the package is built by scripts/addons/package.sh from
// addons/tables, and its layers are the files the installer unpacks. Tests
// use real named sessions and API keys, the public HTTP routes only and the
// real SQLite databases of the add-on. Nothing is published anywhere.
package tablesacceptance_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	"github.com/mudler/LocalAI/tests/addons/tablesacceptance/tablespkg"
	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"
)

func TestTablesAcceptance(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tables acceptance")
}

var (
	repoRoot string
	paths    cluster.Paths
	packages = map[string]tablespkg.Release{}
	// commands records every command the suite itself ran, for the manifest.
	commandsMu sync.Mutex
	commands   [][]string
	// traces maps a failed spec to the retained log file of the cluster.
	tracesMu sync.Mutex
	traces   = map[string]string{}
)

func record(cmd ...string) {
	commandsMu.Lock()
	defer commandsMu.Unlock()
	commands = append(commands, cmd)
}

func run(dir string, env []string, name string, args ...string) {
	GinkgoHelper()
	record(append([]string{name}, args...)...)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	b, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(b))
}

var _ = BeforeSuite(func() {
	_, file, _, _ := runtime.Caller(0)
	repoRoot = filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	_, err := evidenceLocation(os.Getenv("TABLES_ACCEPTANCE_EVIDENCE"), repoRoot)
	Expect(err).NotTo(HaveOccurred())
	scratch, err := os.MkdirTemp("", "tables-acceptance-bin-*")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		_ = filepath.WalkDir(scratch, func(p string, d fs.DirEntry, e error) error {
			if e == nil && d.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
		_ = os.RemoveAll(scratch)
	})
	build := func(env, out, pkgPath string, extra ...string) string {
		if p := os.Getenv(env); p != "" {
			return p
		}
		target := filepath.Join(scratch, out)
		args := append([]string{"build", "-p", "2", "-tags", "auth"}, extra...)
		args = append(args, "-o", target, pkgPath)
		run(repoRoot, []string{"CGO_ENABLED=1"}, "go", args...)
		return target
	}
	paths.Server = build("TABLES_ACCEPTANCE_SERVER", "ui-test-server", "./tests/e2e-ui")
	paths.Mock = os.Getenv("TABLES_ACCEPTANCE_MOCK")
	if paths.Mock == "" {
		paths.Mock = filepath.Join(repoRoot, "tests/e2e/mock-backend/mock-backend")
	}
	Expect(paths.Mock).To(BeAnExistingFile(), "build tests/e2e/mock-backend first")
	exe := os.Getenv("TABLES_ACCEPTANCE_TABLES")
	if exe == "" {
		exe = filepath.Join(scratch, "tables")
		Expect(tablespkg.BuildExecutable(repoRoot, exe, func(argv ...string) { record(argv...) })).To(Succeed())
	}
	// Two releases of the same platform: the second one is what the explicit
	// upgrade installs. Both are built by the real packaging entry point.
	built, err := tablespkg.Build(repoRoot, scratch, exe, func(argv ...string) { record(argv...) }, "0.1.0", "0.2.0")
	Expect(err).NotTo(HaveOccurred())
	for v, r := range built {
		Expect(r.ImageDigest).To(MatchRegexp(`^sha256:[0-9a-f]{64}$`))
		packages[v] = r
	}
})

// start brings a cluster up in a fresh directory and takes it down at the end
// of the container. A failed spec keeps the logs of every node as a trace.
func start(versions ...string) *cluster.Cluster {
	GinkgoHelper()
	return startFrom(nil, packages, versions...)
}

// startFrom is start with releases of another build and a change of the
// cluster configuration that is saved before any process starts.
func startFrom(tune func(*cluster.Config), releases map[string]tablespkg.Release, versions ...string) *cluster.Cluster {
	GinkgoHelper()
	dir, err := os.MkdirTemp("", "tables-acceptance-*")
	Expect(err).NotTo(HaveOccurred())
	c, err := cluster.New(dir, paths)
	Expect(err).NotTo(HaveOccurred())
	if tune != nil {
		tune(c.Config)
		Expect(c.Config.Save()).To(Succeed())
	}
	for _, v := range versions {
		_, err := c.Library.AddArchive("tables", v, releases[v].Archive)
		Expect(err).NotTo(HaveOccurred())
	}
	DeferCleanup(func() {
		retainTrace(c)
		c.Down()
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
			if e == nil && d.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
		_ = os.RemoveAll(dir)
	})
	Expect(c.Up()).To(Succeed())
	return c
}

// retainTrace copies the node logs of a failed spec to the log directory of
// the run, so the evidence of a failure outlives the scratch directory.
func retainTrace(c *cluster.Cluster) {
	if !CurrentSpecReport().Failed() {
		return
	}
	out := filepath.Join(repoRoot, ".cache/addons/logs/tables-acceptance-traces")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return
	}
	name := sanitize(CurrentSpecReport().LeafNodeText) + ".log"
	var b strings.Builder
	for _, n := range cluster.Instances {
		fmt.Fprintf(&b, "==== %s ====\n%s\n", n, c.Nodes[n].Tail(200))
	}
	path := filepath.Join(out, name)
	if os.WriteFile(path, []byte(b.String()), 0o644) == nil {
		tracesMu.Lock()
		traces[CurrentSpecReport().LeafNodeText] = path
		tracesMu.Unlock()
	}
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := b.String()
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

// ---- evidence manifest ----

// evidencePath is where the suite writes the acceptance evidence.
func evidencePath() string {
	p, err := evidenceLocation(os.Getenv("TABLES_ACCEPTANCE_EVIDENCE"), repoRoot)
	if err != nil {
		panic(err)
	}
	return p
}

// evidenceLocation resolves where the manifest goes. A relative path would
// resolve against the package directory of the test binary and leave the
// manifest inside the tree, so it is refused.
func evidenceLocation(env, root string) (string, error) {
	if env == "" {
		return filepath.Join(root, ".cache/addons/tables-acceptance-evidence.json"), nil
	}
	if !filepath.IsAbs(env) {
		return "", fmt.Errorf("TABLES_ACCEPTANCE_EVIDENCE must be an absolute path, got %q", env)
	}
	return env, nil
}

type evidenceCase struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Millis int64  `json:"millis"`
	Trace  string `json:"trace,omitempty"`
	Error  string `json:"error,omitempty"`
}

type evidence struct {
	Schema   int            `json:"schema"`
	Suite    string         `json:"suite"`
	Written  string         `json:"written"`
	Platform platform       `json:"platform"`
	Commands [][]string     `json:"commands"`
	Artifact artifact       `json:"artifact"`
	Cases    []evidenceCase `json:"cases"`
	// Browser holds the Playwright cases; the runner adds them after the
	// browser run, from its report.
	Browser     []evidenceCase `json:"browser,omitempty"`
	Publication string         `json:"publication"`
}

type platform struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Native bool   `json:"native"`
}

type artifact struct {
	Addon    string            `json:"addon"`
	Versions map[string]digest `json:"versions"`
}

type digest struct {
	Archive string `json:"archive"`
	Image   string `json:"image"`
}

var _ = ReportAfterSuite("write the acceptance evidence manifest", func(report Report) {
	ev := evidence{
		Schema: 1, Suite: "tables-acceptance", Written: time.Now().UTC().Format(time.RFC3339),
		Platform:    platform{OS: runtime.GOOS, Arch: runtime.GOARCH, Native: true},
		Artifact:    artifact{Addon: "tables", Versions: map[string]digest{}},
		Publication: "none: the packages are built and installed locally and nothing is pushed, tagged or signed",
	}
	commandsMu.Lock()
	ev.Commands = append(ev.Commands, commands...)
	commandsMu.Unlock()
	ev.Commands = append(ev.Commands, []string{"go", "test", "-count=1", "-tags", "auth", "./tests/addons/tablesacceptance"})
	for v, p := range packages {
		ev.Artifact.Versions[v] = digest{Archive: p.ArchiveDigest, Image: p.ImageDigest}
	}
	tracesMu.Lock()
	defer tracesMu.Unlock()
	for _, s := range report.SpecReports {
		if s.LeafNodeType != types.NodeTypeIt {
			continue
		}
		c := evidenceCase{Name: s.LeafNodeText, State: s.State.String(), Millis: s.RunTime.Milliseconds(), Trace: traces[s.LeafNodeText]}
		if s.Failed() {
			c.Error = s.Failure.Message
		}
		ev.Cases = append(ev.Cases, c)
	}
	b, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(evidencePath()), 0o755)
	tmp := evidencePath() + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, evidencePath())
	}
})
