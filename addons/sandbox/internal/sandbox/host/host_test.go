//go:build linux || darwin

package host_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/host"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func TestTheHostBackendPassesTheConformanceSuite(t *testing.T) {
	sbtest.RunBackendSuite(t, func(t *testing.T) sandbox.Backend {
		return newBackend(t, host.Options{Root: t.TempDir()})
	})
}

// newBackend returns the backend. The only construction error that may skip a
// test is the refusal of root, which TestItNeverRunsAsRoot checks for
// real; any other error is a failure.
func newBackend(t *testing.T, o host.Options) *host.Backend {
	t.Helper()
	b, err := host.New(o)
	if err == nil {
		return b
	}
	if os.Geteuid() == 0 && errors.Is(err, sandbox.ErrUnsupported) {
		t.Skipf("the host backend refuses root: %v", err) //nolint:forbidigo // plain testing.T test; this package has no Ginkgo suite
	}
	t.Fatalf("host.New: %v", err)
	return nil
}

func newSandbox(t *testing.T, b sandbox.Backend) sandbox.Sandbox {
	t.Helper()
	st := sbtest.OpenStore(t)
	ctx := context.Background()
	if _, err := st.Mkdir(ctx, sbtest.Admin, "/proj"); err != nil {
		t.Fatal(err)
	}
	if err := st.Grant(ctx, sbtest.Admin, sbtest.Agent.Principal, "/proj", workspace.ModeWrite); err != nil {
		t.Fatal(err)
	}
	v, err := st.View(ctx, sbtest.Agent, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Agent}, View: v})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sb.Destroy(context.Background()) })
	return sb
}

func TestEveryCommandIsLoggedWithTheUser(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	b := newBackend(t, host.Options{Root: t.TempDir(), Log: func(who, cmd string) {
		mu.Lock()
		lines = append(lines, who+": "+cmd)
		mu.Unlock()
	}})
	sb := newSandbox(t, b)
	p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", "echo hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 || !strings.HasPrefix(lines[0], sbtest.Agent.Principal+": sh -c echo hi") {
		t.Fatalf("log = %v", lines)
	}
}

func TestACommandSeesNothingOfTheAddonEnvironment(t *testing.T) {
	t.Setenv("LOCALAI_TEST_SECRET", "do-not-leak")
	b := newBackend(t, host.Options{Root: t.TempDir()})
	sb := newSandbox(t, b)
	var out strings.Builder
	p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", "env"}, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "do-not-leak") || strings.Contains(out.String(), "LOCALAI_TEST_SECRET") {
		t.Fatalf("the command saw the environment of the add-on:\n%s", out.String())
	}
}

func TestLeftoversOfAnEarlierRunAreRemoved(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(root+"/sb-old/work", 0o700); err != nil {
		t.Fatal(err)
	}
	newBackend(t, host.Options{Root: root})
	if _, err := os.Stat(root + "/sb-old"); !os.IsNotExist(err) {
		t.Errorf("the leftover is still there: %v", err)
	}
}

func TestACommandRunsWhenTheAddonHasNoPath(t *testing.T) {
	t.Setenv("PATH", "")
	b := newBackend(t, host.Options{Root: t.TempDir()})
	sb := newSandbox(t, b)
	var out strings.Builder
	p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", "echo ran"}, Stdout: &out})
	if err != nil {
		t.Fatalf("a command did not start with no PATH in the add-on: %v", err)
	}
	if code, err := p.Wait(); err != nil || code != 0 || out.String() != "ran\n" {
		t.Fatalf("code %d, err %v, out %q", code, err, out.String())
	}
}

func suspendedWithABackgroundProcess(t *testing.T) (*host.Backend, sandbox.SnapshotRef, string, int) {
	t.Helper()
	root := t.TempDir()
	b := newBackend(t, host.Options{Root: root})
	sb := newSandbox(t, b)
	var out strings.Builder
	p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", "sleep 300 </dev/null >/dev/null 2>&1 & echo $!"}, Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatalf("pid %q: %v", out.String(), err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	ref, err := sb.Suspend(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return b, ref, root, pid
}

func alive(pid int) bool {
	// A killed child of init may stay a zombie for a moment.
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	return err == nil && !strings.Contains(string(b), ") Z ")
}

func TestDiscardingASnapshotKillsItsProcessesAndRemovesItsFolder(t *testing.T) {
	b, ref, root, pid := suspendedWithABackgroundProcess(t)
	if !alive(pid) {
		t.Fatal("the background process did not survive the suspend")
	}
	if err := b.Discard(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50 && alive(pid); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(pid) {
		t.Error("the process of the discarded snapshot still runs")
	}
	if left, _ := filepath.Glob(filepath.Join(root, "sb-*")); len(left) != 0 {
		t.Errorf("folders left: %v", left)
	}
	if _, err := b.Resume(context.Background(), ref, sandbox.Env{}); err == nil {
		t.Error("a discarded snapshot was resumed")
	}
	// A second discard, or one of another backend's snapshot, is harmless.
	if err := b.Discard(context.Background(), ref); err != nil {
		t.Error(err)
	}
	if err := b.Discard(context.Background(), sandbox.SnapshotRef{Backend: "gvisor", Path: "x"}); err != nil {
		t.Error(err)
	}
}

func TestClosingTheBackendDiscardsWhatItHolds(t *testing.T) {
	b, _, root, pid := suspendedWithABackgroundProcess(t)
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50 && alive(pid); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(pid) {
		t.Error("the process of a held sandbox still runs after Close")
	}
	if left, _ := filepath.Glob(filepath.Join(root, "sb-*")); len(left) != 0 {
		t.Errorf("folders left: %v", left)
	}
}

func TestARelativeRootIsRefused(t *testing.T) {
	if _, err := host.New(host.Options{Root: "relative/dir"}); err == nil {
		t.Error("a relative root was accepted")
	}
	_ = os.Getenv
	_ = errors.New
}

func TestTheWorkingFolderStaysInsideTheWorkspace(t *testing.T) {
	b := newBackend(t, host.Options{Root: t.TempDir()})
	sb := newSandbox(t, b)
	for _, dir := range []string{"/workspace/../..", "/workspace/proj/../../..", "/workspacefoo", "/tmp", "/"} {
		if _, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}, Dir: dir}); err == nil {
			t.Errorf("a command ran in %q", dir)
		}
	}
	for _, dir := range []string{"/workspace", "/workspace/", "/workspace/proj", "/workspace/proj/../proj"} {
		p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}, Dir: dir})
		if err != nil {
			t.Errorf("a command did not run in %q: %v", dir, err)
			continue
		}
		if _, err := p.Wait(); err != nil {
			t.Errorf("wait in %q: %v", dir, err)
		}
	}
}

// failingMaterialize is a store whose Materialize fails, as a full disk would.
type failingMaterialize struct{ workspace.Store }

func (failingMaterialize) Materialize(context.Context, workspace.View, string) (int64, error) {
	return 0, errors.New("disk full")
}

func TestAFailedCreateLeavesNoFolderBehind(t *testing.T) {
	root := t.TempDir()
	b := newBackend(t, host.Options{Root: root})
	st := sbtest.OpenStore(t)
	ctx := context.Background()
	if _, err := st.Mkdir(ctx, sbtest.Admin, "/proj"); err != nil {
		t.Fatal(err)
	}
	v, err := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: failingMaterialize{st}, Actor: sbtest.Admin}, View: v})
	if err == nil {
		t.Fatal("Create succeeded although the store failed")
	}
	if left, _ := filepath.Glob(filepath.Join(root, "sb-*")); len(left) != 0 {
		t.Fatalf("a failed Create left %v", left)
	}
}
