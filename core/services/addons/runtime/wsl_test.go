//go:build linux

// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/wsl"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// wslShim stands in for wsl.exe: "-d <distro> --cd <dir> --exec <program>
// <args>". Like WSL, it passes the program only the variables that WSLENV
// names, and it waits for the program instead of becoming it, so killing
// it leaves the program running, as killing wsl.exe does.
const wslShim = `#!/bin/sh
set -eu
dir=.
while [ $# -gt 0 ]; do
    case "$1" in
    -d) shift 2 ;;
    --cd) dir=$2; shift 2 ;;
    --exec) shift; break ;;
    *) echo "wsl shim: unknown argument $1" >&2; exit 64 ;;
    esac
done
cmd="env -i"
for name in $(printf '%s' "${WSLENV:-}" | tr ':' ' '); do
    name=${name%%/*}
    eval "val=\${$name-}"
    cmd="$cmd $name=$val"
done
cd "$dir"
$cmd "$@"
`

// shimWSL runs every add-on through the shim. The data directory is the same
// path for the add-on and the host, as if the distribution were this system.
type shimWSL struct {
	shim string
	root func() string
	// fail, when set, is the refusal of Prepare.
	fail error
}

func newShimWSL(root func() string) *shimWSL {
	shim := filepath.Join(realTempDir(), "wsl")
	Expect(os.WriteFile(shim, []byte(wslShim), 0o700)).To(Succeed())
	return &shimWSL{shim: shim, root: root}
}

func (w *shimWSL) Prepare(_ context.Context, pkg packages.PackageReceipt) (WSLLaunch, error) {
	if w.fail != nil {
		return WSLLaunch{}, w.fail
	}
	l := WSLLaunch{
		Path: w.shim,
		Args: []string{"-d", "Test", "--cd", pkg.Directory, "--exec", filepath.Join(pkg.Directory, pkg.Manifest.Executable), "--localai-addon-v1"},
		Env:  []string{"WSLENV=LANG:TZ:" + contract.ChannelEnv},
	}
	if root := w.root(); root != "" {
		l.DataDir = filepath.Join(root, pkg.Manifest.ID)
		l.HostDataDir = l.DataDir
	}
	return l, nil
}

// inWSL makes s run its add-ons through the shim.
func inWSL(s *Supervisor) *shimWSL {
	w := newShimWSL(func() string { return s.config.DataRoot })
	s.wslSpec = w
	return w
}

var _ = Describe("Add-ons that run in WSL 2 (through a wsl.exe stand-in)", func() {
	It("starts the add-on through wsl.exe, serves its endpoint on the owner's loopback and stops it gracefully", func() {
		s := supervisor()
		inWSL(s)
		p := fixture("graceful", service)
		i, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(i.process.cmd.Path).To(HaveSuffix("/wsl"))
		Expect(i.process.cmd.Env).To(ContainElement(contract.ChannelEnv + "=stdio"))
		Expect(i.process.registration.Endpoint).To(HavePrefix("http://127.0.0.1:"))
		r, e := s.Registry.Snapshot(context.Background())
		Expect(e).NotTo(HaveOccurred())
		Expect(r).To(HaveLen(1))
		Expect(s.State("sample", 1)).To(Equal(StateRunning))
		Expect(s.Stop(context.Background(), i)).To(Succeed())
		Expect(filepath.Join(p.Directory, "fixture.closed")).To(BeARegularFile(), "the close of the startup stream did not reach the add-on")
	})

	It("gives the add-on in WSL only LANG, TZ and the channel mode", func() {
		GinkgoT().Setenv("LOCALAI_RUNTIME_TEST_SECRET", "must-not-leak")
		s := supervisor()
		inWSL(s)
		i, e := s.Start(context.Background(), fixture("env-check", service), 1)
		Expect(e).NotTo(HaveOccurred())
		Expect(s.Stop(context.Background(), i)).To(Succeed())
	})

	It("sends what the add-on prints on its standard output to the log, never into the session", func() {
		var mu sync.Mutex
		var got []string
		s := supervisor()
		inWSL(s)
		s.config.Output = func(id, stream, line string) {
			mu.Lock()
			got = append(got, stream+"|"+line)
			mu.Unlock()
		}
		i, e := s.Start(context.Background(), fixture("log", service), 1)
		Expect(e).NotTo(HaveOccurred())
		Eventually(func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), got...) }, 3*time.Second).Should(ContainElements("stderr|hello from stdout", "stderr|hello from stderr"))
		Expect(s.State("sample", 1)).To(Equal(StateRunning), "the prints broke the session")
		Expect(s.Stop(context.Background(), i)).To(Succeed())
	})

	It("gives the add-on the data directory the launcher names and tells the host its own path", func() {
		root := filepath.Join(realTempDir(), "data")
		l := contract.DefaultLimits()
		l.ReadTimeout = 300 * time.Millisecond
		l.ShutdownTimeout = 100 * time.Millisecond
		s, e := NewSupervisor(Config{StateRoot: realTempDir(), DataRoot: root, Limits: l, ReadyTimeout: startBudget, StableHealth: time.Second})
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(s.Close(context.Background())).To(Succeed()) })
		inWSL(s)
		p := fixture("normal", service)
		i, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		Eventually(filepath.Join(root, "sample", "fixture.probe"), 2*time.Second).Should(BeARegularFile())
		Expect(s.HostDataDir("sample")).To(Equal(filepath.Join(root, "sample")))
		Expect(s.HostDataDir("other")).To(BeEmpty())
		Expect(s.Stop(context.Background(), i)).To(Succeed())
	})

	It("says why when the launcher refuses, and starts nothing", func() {
		s := supervisor()
		w := inWSL(s)
		w.fail = errors.New("WSL is not installed")
		_, e := s.Start(context.Background(), fixture("normal", service), 1)
		Expect(e).To(MatchError(ErrRuntime))
		Expect(e).To(MatchError(ContainSubstring("WSL is not installed")))
		r, _ := s.Registry.Snapshot(context.Background())
		Expect(r).To(BeEmpty())
	})

	It("refuses an executable that is not a Linux program", func() {
		s := supervisor()
		inWSL(s)
		p := fixture("normal", service)
		Expect(os.Chmod(filepath.Join(p.Directory, "fixture"), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(p.Directory, "fixture"), []byte("#!/bin/sh\nexit 0\n"), 0o700)).To(Succeed())
		_, e := s.Start(context.Background(), p, 1)
		Expect(e).To(MatchError(ErrRuntime))
	})

	It("stops an add-on in WSL that outlives its wsl.exe once the session ends", func() {
		s := supervisor()
		inWSL(s)
		p := fixture("orphan", service)
		i, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		var pid int
		Eventually(func() error {
			b, e := os.ReadFile(filepath.Join(p.Directory, "fixture.pid"))
			if e == nil {
				pid, e = strconv.Atoi(strings.TrimSpace(string(b)))
			}
			return e
		}, 2*time.Second).Should(Succeed())
		Expect(s.Stop(context.Background(), i)).To(Succeed())
		// The stand-in is gone and the add-on ignored the end of its startup
		// stream. The SDK still ends it: its listener closes with the
		// session, and at the latest its watchdog ends it after the shutdown
		// budget.
		Eventually(func() error { return syscall.Kill(pid, 0) }, 15*time.Second, 200*time.Millisecond).Should(HaveOccurred())
	})
})

var _ = Describe("A package with execution wsl2", func() {
	It("is refused on a host without WSL, with the reason", func() {
		s := supervisor()
		p := fixture("normal", service)
		p.Manifest.OSSettings = &contract.OSSettings{Windows: &contract.WindowsSettings{Execution: contract.ExecutionWSL2}}
		_, e := s.launch(context.Background(), p, 1)
		Expect(e).To(HaveOccurred())
		// The manifest names Windows, which this host is not, so the
		// platform check refuses it first. On Windows the reason is ErrNoWSL
		// (see supervisor_windows_test.go).
		Expect(e).To(MatchError(ErrRuntime))
	})
})

func wslSpecMode(s *Supervisor) { inWSL(s) }

var _ = Describe("The WSL launcher with the supervisor (through a wsl.exe stand-in)", func() {
	It("checks WSL, copies the package into the distribution, runs it there and gives it its data directory there", func() {
		home := realTempDir()
		shim := newShimWSL(func() string { return "" }).shim
		run := func(ctx context.Context, exe string, args ...string) ([]byte, error) {
			switch args[0] {
			case "--status":
				return nil, nil
			case "--list":
				return []byte("  NAME STATE VERSION\n* Test Running 2\n"), nil
			}
			if len(args) > 3 && args[3] == "/bin/sh" {
				return []byte(home), nil
			}
			return exec.CommandContext(ctx, exe, args...).CombinedOutput()
		}
		launcher := wsl.New(wsl.Options{Exe: shim, Run: run, Env: []string{}, HostPath: func(_, p string) string { return p }})
		l := contract.DefaultLimits()
		l.ReadTimeout = 300 * time.Millisecond
		l.ShutdownTimeout = 100 * time.Millisecond
		s, e := NewSupervisor(Config{StateRoot: realTempDir(), DataRoot: filepath.Join(realTempDir(), "data"), Limits: l, ReadyTimeout: startBudget, StableHealth: time.Second})
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(s.Close(context.Background())).To(Succeed()) })
		s.wslSpec = launcher
		p := fixture("graceful", service)
		i, e := s.Start(context.Background(), p, 1)
		Expect(e).NotTo(HaveOccurred())
		copied := filepath.Join(home, wsl.Base, "packages", "sample", "fixture")
		Expect(filepath.Join(copied, "fixture")).To(BeARegularFile())
		data := filepath.Join(home, wsl.Base, "data", "sample")
		Eventually(filepath.Join(data, "fixture.probe"), 2*time.Second).Should(BeARegularFile())
		Expect(s.HostDataDir("sample")).To(Equal(data))
		Expect(s.Stop(context.Background(), i)).To(Succeed())
		// The add-on ran in its copy: that is where it closed.
		Expect(filepath.Join(copied, "fixture.closed")).To(BeARegularFile())
		Expect(filepath.Join(p.Directory, "fixture.closed")).NotTo(BeAnExistingFile())
	})
})
