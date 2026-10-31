package sbtest

import (
	"context"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func testLifecycle(newBackend NewBackend) {
	It("refresh shows content another writer committed", func() {
		f := setup(newBackend, nil)
		f.mustRun("echo mine > proj/mine.txt")
		_, err := f.store.Put(ctx(), Admin, "/proj/fresh.txt", strings.NewReader("fresh\n"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(f.sb.Refresh(ctx())).To(Succeed())
		Expect(f.mustRun("cat proj/fresh.txt proj/mine.txt")).To(Equal("fresh\nmine\n"))
		got, _ := f.read("/proj/mine.txt")
		Expect(got).To(Equal("mine\n"), "refresh did not commit first")
		f.mustRun("echo again >> proj/mine.txt")
		_, c := f.checkpoint("after refresh")
		Expect(c).To(BeEmpty())
		got, _ = f.read("/proj/mine.txt")
		Expect(got).To(Equal("mine\nagain\n"))
	})
	It("suspend and resume keep a running process and its open files", func() {
		f := setup(newBackend, nil)
		_, err := f.sb.Exec(ctx(), sandbox.Cmd{Args: []string{"sh", "-c",
			`exec 3<proj/notes.txt 4>>proj/tick.txt; while :; do cat /dev/fd/3 >/dev/null && echo tick >&4; sleep 0.1; done`}})
		Expect(err).NotTo(HaveOccurred())
		f.mustRun(`while [ ! -s proj/tick.txt ]; do sleep 0.1; done`)
		ref, err := f.sb.Suspend(ctx())
		Expect(err).NotTo(HaveOccurred())
		_, err = f.sb.Exec(ctx(), sandbox.Cmd{Args: []string{"true"}})
		Expect(err).To(MatchError(sandbox.ErrGone), "exec after suspend")
		sb, err := f.b.Resume(ctx(), ref, sandbox.Env{Store: f.store, Actor: Agent})
		Expect(err).NotTo(HaveOccurred())
		f.sb = sb
		Expect(f.mustRun(`a=$(wc -l < proj/tick.txt); sleep 1; b=$(wc -l < proj/tick.txt); [ "$b" -gt "$a" ] && echo alive`)).To(Equal("alive\n"))
		_, c := f.checkpoint("after resume")
		Expect(c).To(BeEmpty())
		got, _ := f.read("/proj/tick.txt")
		Expect(got).To(HavePrefix("tick\n"))
	})
	It("destroy keeps committed changes", func() {
		f := setup(newBackend, nil)
		f.mustRun("echo kept > proj/kept.txt")
		f.checkpoint("keep")
		f.mustRun("echo lost > proj/lost.txt")
		Expect(f.sb.Destroy(ctx())).To(Succeed())
		_, err := f.sb.Exec(context.Background(), sandbox.Cmd{Args: []string{"true"}})
		Expect(err).To(MatchError(sandbox.ErrGone), "exec after destroy")
		got, _ := f.read("/proj/kept.txt")
		Expect(got).To(Equal("kept\n"))
		_, ok := f.read("/proj/lost.txt")
		Expect(ok).To(BeFalse(), "destroy committed an uncommitted file")
	})
	It("a process from before a suspend returns ErrGone", func() {
		f := setup(newBackend, nil)
		p, err := f.sb.Exec(ctx(), sandbox.Cmd{Args: []string{"sleep", "60"}})
		Expect(err).NotTo(HaveOccurred())
		_, err = f.sb.Suspend(ctx())
		Expect(err).NotTo(HaveOccurred())
		requireGone(p)
	})
	It("a process from before a destroy returns ErrGone", func() {
		f := setup(newBackend, nil)
		p, err := f.sb.Exec(ctx(), sandbox.Cmd{Args: []string{"sleep", "60"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(f.sb.Destroy(ctx())).To(Succeed())
		requireGone(p)
	})
}

// requireGone checks that a process that started before a Suspend or Destroy
// returns ErrGone from Wait, Signal and Resize. Wait has a timeout, so a
// backend that blocks fails the spec instead of hanging it.
func requireGone(p sandbox.Process) {
	waited := make(chan error, 1)
	go func() {
		_, err := p.Wait()
		waited <- err
	}()
	var err error
	Eventually(waited, 10*time.Second).Should(Receive(&err), "wait of a process from before a suspend or destroy blocked")
	Expect(err).To(MatchError(sandbox.ErrGone), "wait")
	Expect(p.Signal(syscall.SIGTERM)).To(MatchError(sandbox.ErrGone), "signal")
	Expect(p.Resize(10, 10)).To(MatchError(sandbox.ErrGone), "resize")
}
