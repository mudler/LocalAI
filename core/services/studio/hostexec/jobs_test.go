//go:build linux || darwin

package hostexec_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/hostexec"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("background jobs of the built-in bash", func() {
	var (
		e       *env
		ctx     = context.Background()
		mu      sync.Mutex
		changes []bgjob.Info
	)
	BeforeEach(func() {
		mu.Lock()
		changes = nil
		mu.Unlock()
		e = newEnv(func(_ *env, o *hostexec.Options) { o.Instance = "n1" })
		e.r.OnJob(nil, func(in bgjob.Info) { mu.Lock(); changes = append(changes, in); mu.Unlock() })
	})
	start := func(cmd string) bgjob.Info {
		in, err := e.r.StartJob(ctx, hostexec.JobSpec{Actor: e.a, Label: "t", Command: cmd})
		Expect(err).NotTo(HaveOccurred())
		return in
	}
	read := func(id string) bgjob.Observation {
		o, err := e.r.Jobs().Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: "c1", ID: id})
		Expect(err).NotTo(HaveOccurred())
		return o
	}
	waitEnd := func(id string) bgjob.Info {
		Eventually(func() bool { return read(id).State.Finished() }, "20s", "50ms").Should(BeTrue())
		return read(id).Info
	}
	events := func(kind string) []hostexec.Event {
		all, _ := e.audit.Events(ctx, 100)
		var out []hostexec.Event
		for _, ev := range all {
			if ev.Kind == kind {
				out = append(out, ev)
			}
		}
		return out
	}

	It("runs past the call, keeps the output and the exit status, and records the instance", func() {
		in := start("echo first; sleep 0.3; echo second; exit 3")
		Expect(in.State).To(Equal(bgjob.StateRunning))
		Expect(in.Instance).To(Equal("n1"))
		Expect(in.Provider).To(Equal("builtin"))
		fin := waitEnd(in.ID)
		Expect(fin.State).To(Equal(bgjob.StateExited))
		Expect(fin.Exit).To(Equal(3))
		Expect(read(in.ID).Output).To(Equal([]byte("first\nsecond\n")))
	})

	It("gives the command the fixed environment and keeps stdout and stderr in order", func() {
		GinkgoT().Setenv("LOCALAI_TEST_SECRET_TOKEN", "s3cr3t")
		in := start(`echo "tok=$LOCALAI_TEST_SECRET_TOKEN"; echo err >&2; echo "tz=$TZ"`)
		waitEnd(in.ID)
		out := string(read(in.ID).Output)
		Expect(out).To(Equal("tok=\nerr\ntz=UTC\n"))
	})

	It("keeps the job across the idle sweep of the sandbox and saves its files at the end", func() {
		in := start("sleep 0.5; echo made > job-made.txt")
		// A foreground call runs while the job runs.
		Expect(e.bash("echo hi", 0)).To(Equal("hi\n"))
		waitEnd(in.ID)
		Expect(e.read("/job-made.txt")).To(Equal("made\n"))
		hist, err := e.f.Store().History(ctx, e.f.Actor("alice"), workspace.HistoryQuery{Path: session.HomePath("alice"), Limit: 10})
		Expect(err).NotTo(HaveOccurred())
		Expect(hist[0].Title).To(ContainSubstring("Job " + in.ID + ": t"))
	})

	It("writes input, closes standard input and refuses input after the end", func() {
		in := start("read a; echo got-$a; cat; echo done")
		n, err := e.r.Jobs().Input("alice", "c1", in.ID, "one\n", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(4))
		_, err = e.r.Jobs().Input("alice", "c1", in.ID, "two\n", true)
		Expect(err).NotTo(HaveOccurred())
		fin := waitEnd(in.ID)
		Expect(fin.Exit).To(Equal(0))
		Expect(string(read(in.ID).Output)).To(Equal("got-one\ntwo\ndone\n"))
		_, err = e.r.Jobs().Input("alice", "c1", in.ID, "x", false)
		var ended *bgjob.EndedError
		Expect(errors.As(err, &ended)).To(BeTrue())
	})

	It("kills the whole process group, with term and then kill", func() {
		in := start(`trap '' TERM; sleep 300 & wait`)
		time.Sleep(200 * time.Millisecond)
		_, err := e.r.Jobs().Kill("alice", "c1", in.ID, bgjob.EndKilled, 15)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bgjob.State { return read(in.ID).State }, "3s").Should(Equal(bgjob.StateEnding))
		_, err = e.r.Jobs().Kill("alice", "c1", in.ID, bgjob.EndKilled, 9)
		Expect(err).NotTo(HaveOccurred())
		fin := waitEnd(in.ID)
		Expect(fin.State).To(Equal(bgjob.StateKilled))
		Expect(fin.Exit).To(Equal(128 + 9))
	})

	It("kills a background child of the job when the job ends, however it ends", func() {
		childOf := func(cmd string) (bgjob.Info, int) {
			in := start(cmd)
			var pid int
			Eventually(func() error {
				_, err := fmt.Sscanf(string(read(in.ID).Output), "%d", &pid)
				return err
			}, "5s", "20ms").Should(Succeed())
			DeferCleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			return in, pid
		}
		running := func(pid int) bool {
			if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
				return !strings.Contains(string(b), ") Z ")
			}
			return syscall.Kill(pid, 0) == nil
		}
		// The main process returns at once and leaves a server behind.
		in, pid := childOf("sleep 300 </dev/null >/dev/null 2>&1 & echo $!")
		waitEnd(in.ID)
		Eventually(func() bool { return running(pid) }, "5s", "20ms").Should(BeFalse())
		// The job is killed while the main process and its child run.
		in, pid = childOf("(trap '' TERM; sleep 300) </dev/null >/dev/null 2>&1 & echo $!; wait")
		_, err := e.r.Jobs().Kill("alice", "c1", in.ID, bgjob.EndKilled, 15)
		Expect(err).NotTo(HaveOccurred())
		waitEnd(in.ID)
		Eventually(func() bool { return running(pid) }, "15s", "50ms").Should(BeFalse())
		// The chat is deleted.
		in, pid = childOf("(trap '' TERM; sleep 300) </dev/null >/dev/null 2>&1 & echo $!; wait")
		e.r.Jobs().EndChat("alice", "c1", bgjob.EndChatDeleted)
		waitEnd(in.ID)
		Eventually(func() bool { return running(pid) }, "15s", "50ms").Should(BeFalse())
	})

	It("keeps the sandbox entry while a job runs and gives it back after", func() {
		in := start("sleep 5")
		chatN, userN, nodeN := e.r.Jobs().Counts("alice", "c1")
		Expect([]int{chatN, userN, nodeN}).To(Equal([]int{1, 1, 1}))
		_, err := e.r.Jobs().Kill("alice", "c1", in.ID, bgjob.EndKilled, 9)
		Expect(err).NotTo(HaveOccurred())
		waitEnd(in.ID)
	})

	It("audits the start, each input, the kill and the end, and caps the text at 4 KiB", func() {
		long := strings.Repeat("x", 6000)
		in := start("cat >/dev/null; echo " + long)
		_, err := e.r.Jobs().Input("alice", "c1", in.ID, long[:1900], false)
		Expect(err).NotTo(HaveOccurred())
		_, err = e.r.Jobs().Kill("alice", "c1", in.ID, bgjob.EndStopped, 9)
		Expect(err).NotTo(HaveOccurred())
		waitEnd(in.ID)
		Eventually(func() int { return len(events(hostexec.KindJobEnd)) }).Should(Equal(1))
		s := events(hostexec.KindJobStart)
		Expect(s).To(HaveLen(1))
		Expect(s[0].Job).To(Equal(in.ID))
		Expect(len(s[0].Command)).To(BeNumerically("<=", 4100))
		Expect(s[0].Command).To(HaveSuffix("..."))
		i := events(hostexec.KindJobInput)
		Expect(i).To(HaveLen(1))
		Expect(i[0].Command).To(HaveLen(1900))
		k := events(hostexec.KindJobKill)
		Expect(k).To(HaveLen(1))
		Expect(k[0].Command).To(Equal("kill stopped"))
		end := events(hostexec.KindJobEnd)[0]
		Expect(end.Exit).To(Equal(128 + 9))
	})

	It("reports the start and the end to the hook, never with the command", func() {
		in := start("echo secret-command-text")
		waitEnd(in.ID)
		Eventually(func() int { mu.Lock(); defer mu.Unlock(); return len(changes) }).Should(Equal(2))
		mu.Lock()
		defer mu.Unlock()
		Expect(changes[0].State).To(Equal(bgjob.StateRunning))
		Expect(changes[1].State).To(Equal(bgjob.StateExited))
		Expect(changes[1].Label).To(Equal("t"))
	})

	It("ends jobs the check names", func() {
		e.r.OnJob(func(_ context.Context, in bgjob.Info) bgjob.EndReason { return bgjob.EndGroupOff }, nil)
		in := start("sleep 30")
		e.r.Jobs().Sweep(ctx)
		fin := waitEnd(in.ID)
		Expect(fin.End).To(Equal(bgjob.EndGroupOff))
	})

	It("ends the jobs of a deleted chat and leaves the entry usable", func() {
		in := start("sleep 30")
		// A foreground call for the same chat still runs.
		Expect(e.bash("echo ok", 0)).To(Equal("ok\n"))
		e.r.Jobs().EndChat("alice", "c1", bgjob.EndChatDeleted)
		fin := waitEnd(in.ID)
		Expect(fin.End).To(Equal(bgjob.EndChatDeleted))
	})

	It("refuses a fifth job in a chat and a start without a command", func() {
		for range 4 {
			start("sleep 30")
		}
		_, err := e.r.StartJob(ctx, hostexec.JobSpec{Actor: e.a, Command: "true"})
		var le *bgjob.LimitError
		Expect(errors.As(err, &le)).To(BeTrue())
		_, err = e.r.StartJob(ctx, hostexec.JobSpec{Actor: e.a})
		Expect(err).To(HaveOccurred())
		e.r.Jobs().EndChat("alice", "c1", bgjob.EndChatDeleted)
	})

	It("leaves no failed entry for a start that was refused as busy", func() {
		for _, c := range []string{"c1", "c2", "c3", "c4"} {
			a := e.a
			a.ChatID = c
			_, err := e.r.StartJob(ctx, hostexec.JobSpec{Actor: a, Label: "t", Command: "sleep 30"})
			Expect(err).NotTo(HaveOccurred())
		}
		mu.Lock()
		before := len(changes)
		mu.Unlock()
		a := e.a
		a.ChatID = "c5"
		_, err := e.r.StartJob(ctx, hostexec.JobSpec{Actor: a, Label: "t", Command: "true"})
		Expect(errors.Is(err, runner.ErrBusy)).To(BeTrue())
		mu.Lock()
		Expect(changes).To(HaveLen(before), "no state change for a job that never began")
		mu.Unlock()
		l, _ := e.r.Jobs().List(ctx, "alice", "c5")
		Expect(l).To(BeEmpty())
		for _, c := range []string{"c1", "c2", "c3", "c4"} {
			e.r.Jobs().EndChat("alice", c, bgjob.EndChatDeleted)
		}
	})

	It("refuses a chat whose agent works in one folder", func() {
		e.put("/Proj/in.txt", "inside\n")
		n, err := e.f.Store().Stat(ctx, e.f.Actor("alice"), session.HomePath("alice")+"/Proj")
		Expect(err).NotTo(HaveOccurred())
		scoped := e.a.Fenced(workspace.FenceRoot{Node: n.ID, Mode: workspace.ModeWrite})
		_, err = e.r.StartJob(ctx, hostexec.JobSpec{Actor: scoped, Command: "true"})
		Expect(errors.Is(err, hostexec.ErrScoped)).To(BeTrue())
		chatN, _, _ := e.r.Jobs().Counts("alice", "c1")
		Expect(chatN).To(Equal(0))
	})

	It("keeps the sandbox of the chat when a fifth chat asks for one", func() {
		in := start("sleep 30")
		for _, c := range []string{"c2", "c3", "c4"} {
			a := e.a
			a.ChatID = c
			raw := []byte(`{"command":"true"}`)
			_, err := e.r.Run(ctx, a, raw)
			Expect(err).NotTo(HaveOccurred())
		}
		a := e.a
		a.ChatID = "c5"
		_, err := e.r.Run(ctx, a, []byte(`{"command":"true"}`))
		Expect(err).NotTo(HaveOccurred(), "the oldest idle entry without a job is taken")
		Expect(read(in.ID).State).To(Equal(bgjob.StateRunning))
		e.r.Jobs().EndChat("alice", "c1", bgjob.EndChatDeleted)
	})
})
