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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/host"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// newBackend returns the backend. The only construction error that may skip a
// spec is the refusal of root, which the "never runs as root" spec checks for
// real; any other error is a failure.
func newBackend(o host.Options) *host.Backend {
	b, err := host.New(o)
	if err == nil {
		return b
	}
	if os.Geteuid() == 0 && errors.Is(err, sandbox.ErrUnsupported) {
		Skip("the host backend refuses root: " + err.Error())
	}
	Expect(err).ToNot(HaveOccurred(), "host.New")
	return nil
}

func newSandbox(b sandbox.Backend) sandbox.Sandbox {
	st := sbtest.OpenStore()
	ctx := context.Background()
	_, err := st.Mkdir(ctx, sbtest.Admin, "/proj")
	Expect(err).ToNot(HaveOccurred())
	Expect(st.Grant(ctx, sbtest.Admin, sbtest.Agent.Principal, "/proj", workspace.ModeWrite)).To(Succeed())
	v, err := st.View(ctx, sbtest.Agent, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	Expect(err).ToNot(HaveOccurred())
	sb, err := b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Agent}, View: v})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = sb.Destroy(context.Background()) })
	return sb
}

func suspendedWithABackgroundProcess() (*host.Backend, sandbox.SnapshotRef, string, int) {
	root := GinkgoT().TempDir()
	b := newBackend(host.Options{Root: root})
	sb := newSandbox(b)
	var out strings.Builder
	p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", "sleep 300 </dev/null >/dev/null 2>&1 & echo $!"}, Stdout: &out})
	Expect(err).ToNot(HaveOccurred())
	_, err = p.Wait()
	Expect(err).ToNot(HaveOccurred())
	pid, err := strconv.Atoi(strings.TrimSpace(out.String()))
	Expect(err).ToNot(HaveOccurred(), "pid %q", out.String())
	DeferCleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	ref, err := sb.Suspend(context.Background())
	Expect(err).ToNot(HaveOccurred())
	return b, ref, root, pid
}

func alive(pid int) bool {
	// A killed child of init may stay a zombie for a moment.
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	return err == nil && !strings.Contains(string(b), ") Z ")
}

// failingMaterialize is a store whose Materialize fails, as a full disk would.
type failingMaterialize struct{ workspace.Store }

func (failingMaterialize) Materialize(context.Context, workspace.View, string) (int64, error) {
	return 0, errors.New("disk full")
}

var _ = Describe("the host backend", func() {
	Describe("the conformance suite", func() {
		sbtest.RunBackendSuite(func() sandbox.Backend {
			return newBackend(host.Options{Root: GinkgoT().TempDir()})
		})
	})

	It("logs every command with the user", func() {
		var mu sync.Mutex
		var lines []string
		b := newBackend(host.Options{Root: GinkgoT().TempDir(), Log: func(who, cmd string) {
			mu.Lock()
			lines = append(lines, who+": "+cmd)
			mu.Unlock()
		}})
		sb := newSandbox(b)
		p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", "echo hi"}})
		Expect(err).ToNot(HaveOccurred())
		_, err = p.Wait()
		Expect(err).ToNot(HaveOccurred())
		mu.Lock()
		defer mu.Unlock()
		Expect(lines).To(HaveLen(1))
		Expect(lines[0]).To(HavePrefix(sbtest.Agent.Principal + ": sh -c echo hi"))
	})

	It("shows a command nothing of the add-on environment", func() {
		GinkgoT().Setenv("LOCALAI_TEST_SECRET", "do-not-leak")
		b := newBackend(host.Options{Root: GinkgoT().TempDir()})
		sb := newSandbox(b)
		var out strings.Builder
		p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", "env"}, Stdout: &out})
		Expect(err).ToNot(HaveOccurred())
		_, err = p.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(out.String()).ToNot(ContainSubstring("do-not-leak"), "the command saw the environment of the add-on")
		Expect(out.String()).ToNot(ContainSubstring("LOCALAI_TEST_SECRET"), "the command saw the environment of the add-on")
	})

	It("removes the leftovers of an earlier run", func() {
		root := GinkgoT().TempDir()
		Expect(os.MkdirAll(root+"/sb-old/work", 0o700)).To(Succeed())
		newBackend(host.Options{Root: root})
		_, err := os.Stat(root + "/sb-old")
		Expect(os.IsNotExist(err)).To(BeTrue(), "the leftover is still there: %v", err)
	})

	It("runs a command when the add-on has no PATH", func() {
		GinkgoT().Setenv("PATH", "")
		b := newBackend(host.Options{Root: GinkgoT().TempDir()})
		sb := newSandbox(b)
		var out strings.Builder
		p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"sh", "-c", "echo ran"}, Stdout: &out})
		Expect(err).ToNot(HaveOccurred(), "a command did not start with no PATH in the add-on")
		code, err := p.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(0))
		Expect(out.String()).To(Equal("ran\n"))
	})

	It("kills the processes of a discarded snapshot and removes its folder", func() {
		b, ref, root, pid := suspendedWithABackgroundProcess()
		Expect(alive(pid)).To(BeTrue(), "the background process did not survive the suspend")
		Expect(b.Discard(context.Background(), ref)).To(Succeed())
		Eventually(func() bool { return alive(pid) }, time.Second, 20*time.Millisecond).Should(BeFalse(), "the process of the discarded snapshot still runs")
		left, _ := filepath.Glob(filepath.Join(root, "sb-*"))
		Expect(left).To(BeEmpty(), "folders left")
		_, err := b.Resume(context.Background(), ref, sandbox.Env{})
		Expect(err).To(HaveOccurred(), "a discarded snapshot was resumed")
		// A second discard, or one of another backend's snapshot, is harmless.
		Expect(b.Discard(context.Background(), ref)).To(Succeed())
		Expect(b.Discard(context.Background(), sandbox.SnapshotRef{Backend: "gvisor", Path: "x"})).To(Succeed())
	})

	It("discards what it holds when it closes", func() {
		b, _, root, pid := suspendedWithABackgroundProcess()
		Expect(b.Close()).To(Succeed())
		Eventually(func() bool { return alive(pid) }, time.Second, 20*time.Millisecond).Should(BeFalse(), "the process of a held sandbox still runs after Close")
		left, _ := filepath.Glob(filepath.Join(root, "sb-*"))
		Expect(left).To(BeEmpty(), "folders left")
	})

	It("refuses a relative root", func() {
		_, err := host.New(host.Options{Root: "relative/dir"})
		Expect(err).To(HaveOccurred(), "a relative root was accepted")
	})

	It("keeps the working folder inside the workspace", func() {
		b := newBackend(host.Options{Root: GinkgoT().TempDir()})
		sb := newSandbox(b)
		for _, dir := range []string{"/workspace/../..", "/workspace/proj/../../..", "/workspacefoo", "/tmp", "/"} {
			_, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}, Dir: dir})
			Expect(err).To(HaveOccurred(), "a command ran in %q", dir)
		}
		for _, dir := range []string{"/workspace", "/workspace/", "/workspace/proj", "/workspace/proj/../proj"} {
			p, err := sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}, Dir: dir})
			Expect(err).ToNot(HaveOccurred(), "a command did not run in %q", dir)
			_, err = p.Wait()
			Expect(err).ToNot(HaveOccurred(), "wait in %q", dir)
		}
	})

	It("leaves no folder behind after a failed create", func() {
		root := GinkgoT().TempDir()
		b := newBackend(host.Options{Root: root})
		st := sbtest.OpenStore()
		ctx := context.Background()
		_, err := st.Mkdir(ctx, sbtest.Admin, "/proj")
		Expect(err).ToNot(HaveOccurred())
		v, err := st.View(ctx, sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
		Expect(err).ToNot(HaveOccurred())
		_, err = b.Create(ctx, sandbox.Spec{Env: sandbox.Env{Store: failingMaterialize{st}, Actor: sbtest.Admin}, View: v})
		Expect(err).To(HaveOccurred(), "Create succeeded although the store failed")
		left, _ := filepath.Glob(filepath.Join(root, "sb-*"))
		Expect(left).To(BeEmpty(), "a failed Create left folders")
	})
})
