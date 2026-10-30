//go:build linux

package gvisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func bg() context.Context { return context.Background() }

const tickScript = `exec 3<proj/notes.txt 4>>proj/tick.txt; echo $$ > /tmp/tick.pid; while :; do cat /dev/fd/3 >/dev/null && echo tick >&4; sleep 0.1; done`

func resume(b *Backend, sb sandbox.Sandbox, st workspace.Store) sandbox.Sandbox {
	ref, err := sb.Suspend(bg())
	Expect(err).ToNot(HaveOccurred(), "suspend")
	next, err := b.Resume(bg(), ref, sandbox.Env{Store: st, Actor: sbtest.Admin})
	Expect(err).ToNot(HaveOccurred(), "resume")
	DeferCleanup(func() { next.Destroy(bg()) })
	return next
}

var _ = Describe("gVisor backend", func() {
	// A command in the sandbox must not reach the agent socket or a port socket
	// by the path, and not through the root and working folder links of the agent
	// or of the shell itself (/proc/<pid>/root, /proc/<pid>/cwd). The port itself
	// still works for the guest through 127.0.0.1. Every route is attempted and
	// must fail to connect. (Paths of the form /proc/<pid>/fd/<n>/name fail with
	// ENOTDIR whatever the agent does, so they are not listed: they would prove
	// nothing.)
	It("does not let a command reach the control sockets", func() {
		b := testBackend()
		sb, _ := fixture(b, sandbox.Spec{Env: sandbox.Env{Ports: map[int]sandbox.PortHandler{7000: sbtest.EchoHandler}}})
		got := sh(sb, `python3 - <<'EOF'
import os, socket
pids, found = ['1', 'self'], False
for pid in os.listdir('/proc'):
    if pid.isdigit():
        try:
            if open('/proc/%s/comm' % pid).read().strip() == 'sandbox-agent':
                found = True
                if pid not in pids:
                    pids.append(pid)
        except OSError:
            pass
paths = ['/mnt/ctl/agent.sock', '/mnt/ctl/port-7000.sock', '/mnt/ctl/../ctl/agent.sock']
for pid in pids:
    for base in ('root/mnt/ctl', 'cwd/ctl', 'root/mnt/ctl/..' + '/ctl'):
        for n in ('agent.sock', 'port-7000.sock'):
            paths.append('/proc/%s/%s/%s' % (pid, base, n))
ok, tried = [], 0
for p in paths:
    s = socket.socket(socket.AF_UNIX)
    tried += 1
    try:
        s.connect(p)
        ok.append(p)
    except OSError:
        pass
    s.close()
print('reached', ok)
print('tried', tried)
print('agent found', found)
print('listing', sorted(os.listdir('/mnt/ctl')))
c = socket.create_connection(('127.0.0.1', 7000), 5)
c.sendall(b'ping')
print('echo', c.recv(4).decode())
EOF`)
		for _, want := range []string{"reached []\n", "tried 15\n", "agent found True\n", "listing []\n", "echo ping\n"} {
			Expect(got).To(ContainSubstring(want), "missing %q in:\n%s", want, got)
		}
	})

	// A command can make its own user and mount namespace (unshare -Urm). The
	// copied mounts of that namespace must still not give it the control sockets,
	// by umount of the cover on /mnt/ctl, by a bind of /mnt (which is no mount
	// point), or by a recursive bind of /mnt. The control route shows that a
	// socket outside /mnt/ctl is reachable through a recursive bind in such a
	// namespace, so the other routes fail for the right reason. Every route must
	// enter its namespace: a guest that cannot make one, or an image without
	// unshare, fails the test instead of passing it vacuously.
	It("does not let a command reach the control sockets from a user namespace", func() {
		b := testBackend()
		sb, _ := fixture(b, sandbox.Spec{Env: sandbox.Env{Ports: map[int]sandbox.PortHandler{7000: sbtest.EchoHandler}}})
		got := sh(sb, `command -v unshare >/dev/null || { echo no unshare binary in the image; exit 1; }
cat > /tmp/connect.py <<'EOF2'
import socket, sys
for p in sys.argv[1:]:
    s = socket.socket(socket.AF_UNIX)
    try:
        s.connect(p)
        print('reached', p)
    except OSError as e:
        print('refused', p, e.errno)
    s.close()
EOF2
cat > /tmp/listen.py <<'EOF2'
import socket
s = socket.socket(socket.AF_UNIX)
s.bind('/tmp/control.sock')
s.listen(8)
s.settimeout(60)
try:
    while True:
        s.accept()[0].close()
except OSError:
    pass
EOF2
python3 /tmp/listen.py &
i=0; while [ ! -S /tmp/control.sock ]; do i=$((i+1)); [ $i -gt 100 ] && { echo no control socket; exit 1; }; sleep 0.1; done
echo ROUTE umount
unshare -Urm sh -c 'echo entered; umount /mnt/ctl 2>&1; ls /mnt/ctl 2>&1; python3 /tmp/connect.py /mnt/ctl/agent.sock /mnt/ctl/port-7000.sock' 2>&1
echo ROUTE bind
unshare -Urm sh -c 'echo entered; mkdir -p /tmp/m1 && mount --bind /mnt /tmp/m1 2>&1; ls /tmp/m1/ctl 2>&1; python3 /tmp/connect.py /tmp/m1/ctl/agent.sock /tmp/m1/ctl/port-7000.sock' 2>&1
echo ROUTE rbind
unshare -Urm sh -c 'echo entered; mkdir -p /tmp/m2 && mount --rbind /mnt /tmp/m2 2>&1; ls /tmp/m2/ctl 2>&1; python3 /tmp/connect.py /tmp/m2/ctl/agent.sock /tmp/m2/ctl/port-7000.sock' 2>&1
echo ROUTE control
unshare -Urm sh -c 'echo entered; mkdir -p /tmp/m3 && mount --rbind /tmp /tmp/m3 2>&1; python3 /tmp/connect.py /tmp/m3/control.sock' 2>&1
echo ROUTE end
true`)
		routes := map[string][]string{}
		var cur string
		for _, line := range strings.Split(got, "\n") {
			if name, ok := strings.CutPrefix(line, "ROUTE "); ok {
				cur = name
				continue
			}
			if cur != "" {
				routes[cur] = append(routes[cur], line)
			}
		}
		var problems []string
		for _, name := range []string{"umount", "bind", "rbind", "control"} {
			lines := strings.Join(routes[name], "\n")
			if !strings.Contains(lines, "entered") {
				problems = append(problems, fmt.Sprintf("route %s did not enter its namespace:\n%s", name, lines))
			}
			reached := strings.Contains(lines, "reached ")
			if name == "control" && !reached {
				problems = append(problems, fmt.Sprintf("the control route reached nothing, so the others prove nothing:\n%s", lines))
			}
			if name != "control" && reached {
				problems = append(problems, fmt.Sprintf("route %s reached a control socket:\n%s", name, lines))
			}
		}
		Expect(problems).To(BeEmpty(), "full output:\n%s", got)
	})

	// The commands get only CAP_DAC_OVERRIDE and CAP_FOWNER, as under chv. The
	// ambient capabilities of the agent itself must not leak to them.
	It("keeps only two capabilities for commands", func() {
		b := testBackend()
		sb, _ := fixture(b, sandbox.Spec{})
		got := sh(sb, `grep -E '^(CapInh|CapPrm|CapEff|CapAmb|NoNewPrivs)' /proc/self/status; id -u`)
		for _, want := range []string{"CapPrm:\t000000000000000a", "CapEff:\t000000000000000a", "CapAmb:\t000000000000000a", "NoNewPrivs:\t1", "\n1000\n"} {
			Expect(got).To(ContainSubstring(want), "missing %q in:\n%s", want, got)
		}
	})

	It("stops a sandbox over its memory limit with the memory watcher", func() {
		b := testBackend()
		b.memEvery = 200 * time.Millisecond
		sb, _ := fixture(b, sandbox.Spec{Memory: 256 << 20})
		s := sb.(*sbx)
		p, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"python3", "-c", "b = b'x' * (700 << 20); import time; time.sleep(120)"}})
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() error {
			_, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"true"}})
			return err
		}, 90*time.Second, 200*time.Millisecond).Should(MatchError(sandbox.ErrGone), "the sandbox was not stopped")
		_, err = p.Wait()
		Expect(err).To(MatchError(sandbox.ErrGone), "wait after the stop")
		log, _ := os.ReadFile(filepath.Join(s.dir, "console.log"))
		Expect(string(log)).To(ContainSubstring("memory limit"), "the reason is not in the log")
		// What the guest wrote before is still there to checkpoint.
		_, _, err = sb.Checkpoint(bg(), "after the stop")
		Expect(err).ToNot(HaveOccurred())
	})

	It("refuses commands until a refresh when the disk watcher sees a full disk", func() {
		b := testBackend()
		b.diskEvery = 200 * time.Millisecond
		sb, st := fixture(b, sandbox.Spec{Disk: 64 << 20})
		sh(sb, "head -c 40000000 /dev/zero > proj/a.bin && head -c 40000000 /dev/zero > proj/b.bin")
		var err error
		Eventually(func() error {
			_, err = sb.Exec(bg(), sandbox.Cmd{Args: []string{"true"}})
			return err
		}, 60*time.Second, 100*time.Millisecond).Should(HaveOccurred(), "Exec was not refused")
		Expect(err).To(MatchError(ErrDiskFull))
		Expect(err.Error()).To(Equal("The sandbox disk is full."))
		// A checkpoint works, and measures the disk again. The upper layer keeps
		// the files, so the disk stays full until a refresh gives a new upper.
		_, _, err = sb.Checkpoint(bg(), "full")
		Expect(err).ToNot(HaveOccurred())
		_, err = sb.Exec(bg(), sandbox.Cmd{Args: []string{"true"}})
		Expect(err).To(MatchError(ErrDiskFull), "after a checkpoint")
		Expect(sb.Refresh(bg())).To(Succeed())
		Expect(sh(sb, "ls proj | tr '\\n' ' '")).To(Equal("a.bin b.bin "), "after the refresh")
		r, _, err := st.Open(bg(), sbtest.Admin, "/proj/b.bin")
		Expect(err).ToNot(HaveOccurred())
		r.Close()
	})

	// A single file over the disk size stops at the size limit of the process.
	It("does not let one file pass the disk size", func() {
		b := testBackend()
		sb, _ := fixture(b, sandbox.Spec{Disk: 64 << 20})
		var errb bytes.Buffer
		p, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"sh", "-c", "head -c 100000000 /dev/zero > proj/big.bin; echo rc=$?; ls -l proj/big.bin | cut -d' ' -f5"}, Stderr: &errb, Stdout: &errb})
		Expect(err).ToNot(HaveOccurred())
		_, err = p.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(errb.String()).To(ContainSubstring("67108864"), "the file is not cut at 64 MiB")
	})

	It("keeps the process and its files over five suspends in a row", func() {
		b := testBackend()
		sb, st := fixture(b, sandbox.Spec{})
		sh(sb, "echo notes > proj/notes.txt")
		_, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"sh", "-c", tickScript}})
		Expect(err).ToNot(HaveOccurred())
		sh(sb, `i=0; while [ ! -s proj/tick.txt ]; do i=$((i+1)); [ $i -gt 300 ] && { echo "proj/tick.txt never appeared" >&2; exit 1; }; sleep 0.1; done`)
		for i := range 5 {
			sb = resume(b, sb, st)
			Expect(sh(sb, `a=$(wc -l < proj/tick.txt); sleep 1; b=$(wc -l < proj/tick.txt); [ "$b" -gt "$a" ] && echo alive`)).To(Equal("alive\n"), "cycle %d", i)
		}
		got := sh(sb, `ls -l /proc/$(cat /tmp/tick.pid)/fd | grep -c -E 'notes.txt|tick.txt'`)
		Expect(strings.TrimSpace(got)).To(Equal("2"), "open files of the process after 5 cycles")
		_, c, err := sb.Checkpoint(bg(), "after the cycles")
		Expect(err).ToNot(HaveOccurred())
		Expect(c).To(BeEmpty())
	})

	It("keeps a terminal process over suspend and resume", func() {
		b := testBackend()
		sb, st := fixture(b, sandbox.Spec{})
		script := `import os, time
f = open('proj/tty.log', 'a', buffering=1)
while True:
    f.write('tick tty=%s\n' % (os.isatty(0) and os.isatty(1)))
    time.sleep(0.1)`
		p, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"python3", "-c", script}, TTY: &sandbox.TTY{Rows: 24, Cols: 80}})
		Expect(err).ToNot(HaveOccurred())
		sh(sb, `i=0; while [ ! -s proj/tty.log ]; do i=$((i+1)); [ $i -gt 300 ] && { echo "proj/tty.log never appeared" >&2; exit 1; }; sleep 0.1; done`)
		sb = resume(b, sb, st)
		_, err = p.Wait()
		Expect(err).To(MatchError(sandbox.ErrGone), "the old handle")
		got := sh(sb, `a=$(wc -l < proj/tty.log); sleep 1; b=$(wc -l < proj/tty.log); [ "$b" -gt "$a" ] && echo alive; tail -n 1 proj/tty.log`)
		Expect(got).To(Equal("alive\ntick tty=True\n"), "after resume")
	})

	// A checkpoint during a large copy-up commits the whole file or leaves it
	// alone: the sandbox is paused until the system call that copies the file
	// has finished. The test proves that the checkpoint overlapped the writer
	// process: the writer had started (a marker in the upper layer) and had not
	// finished (no end marker) when the sandbox was paused. It does not prove that
	// the pause landed inside the copy itself; the content check (the exact old or
	// the exact new bytes) is what catches a torn file. A run where the
	// writer was already done does not count; the test tries again, a bounded
	// number of times, and fails when no attempt overlapped.
	It("commits a checkpoint during a large copy-up whole or not at all", func() {
		b := testBackend()
		sb, st := fixture(b, sandbox.Spec{})
		s := sb.(*sbx)
		const size = 96 << 20
		big := bytes.Repeat([]byte("0123456789abcdef"), size/16)
		appended := append(append([]byte{}, big...), "x\n"...)
		content := func() []byte {
			r, _, err := st.Open(bg(), sbtest.Admin, "/proj/big.bin")
			Expect(err).ToNot(HaveOccurred())
			defer r.Close()
			body, err := io.ReadAll(r)
			Expect(err).ToNot(HaveOccurred())
			return body
		}
		upperProj := filepath.Join(s.dir, "upper", "data", "proj")
		const attempts = 8
		overlapped := false
		for i := 0; i < attempts && !overlapped; i++ {
			_, err := st.Put(bg(), sbtest.Admin, "/proj/big.bin", bytes.NewReader(big), workspace.PutOptions{Overwrite: true})
			Expect(err).ToNot(HaveOccurred())
			Expect(sb.Refresh(bg())).To(Succeed()) // the guest sees the original big.bin, in the lower layer
			started, finished := "started"+strconv.Itoa(i), "finished"+strconv.Itoa(i)
			p, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"sh", "-c", "echo go > proj/" + started + "; echo x >> proj/big.bin; echo end > proj/" + finished}})
			Expect(err).ToNot(HaveOccurred())
			// The upper layer is a host folder: the marker shows that the writer runs.
			Eventually(func() error {
				_, err := os.Stat(filepath.Join(upperProj, started))
				return err
			}, 30*time.Second, 2*time.Millisecond).Should(Succeed(), "the writer did not start")
			var sawEnd bool
			var upperSize int64 = -1
			s.hooks.afterRead = func() { // the sandbox is paused here
				_, err := os.Stat(filepath.Join(upperProj, finished))
				sawEnd = err == nil
				if fi, err := os.Stat(filepath.Join(upperProj, "big.bin")); err == nil {
					upperSize = fi.Size()
				}
			}
			_, _, err = sb.Checkpoint(bg(), "mid copy-up")
			s.hooks.afterRead = nil
			Expect(err).ToNot(HaveOccurred())
			body := content()
			Expect(bytes.Equal(body, big) || bytes.Equal(body, appended)).To(BeTrue(), "attempt %d: the store has %d bytes that are neither the old nor the new content: a torn copy", i, len(body))
			_, err = p.Wait()
			Expect(err).ToNot(HaveOccurred())
			_, _, err = sb.Checkpoint(bg(), "after")
			Expect(err).ToNot(HaveOccurred())
			body = content()
			Expect(bytes.Equal(body, appended)).To(BeTrue(), "attempt %d: after the writer ended the store has %d bytes, not the old content plus \"x\\n\"", i, len(body))
			if !sawEnd {
				overlapped = true
				fmt.Fprintf(GinkgoWriter, "attempt %d: the writer was running at the pause (upper big.bin was %d bytes)\n", i, upperSize)
			}
		}
		Expect(overlapped).To(BeTrue(), "no checkpoint overlapped the writer in %d attempts: the test proved nothing", attempts)
	})

	It("gives ErrGone after a crash and the checkpoint still works", func() {
		b := testBackend()
		sb, st := fixture(b, sandbox.Spec{})
		sh(sb, "echo kept > proj/kept.txt")
		s := sb.(*sbx)
		Expect(syscall.Kill(s.in.pid, syscall.SIGKILL)).To(Succeed())
		Eventually(s.in.done, 30*time.Second).Should(BeClosed(), "the watcher did not see the crash")
		_, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"true"}})
		Expect(err).To(MatchError(sandbox.ErrGone), "exec after a crash")
		_, _, err = sb.Checkpoint(bg(), "after crash")
		Expect(err).ToNot(HaveOccurred())
		r, _, err := st.Open(bg(), sbtest.Admin, "/proj/kept.txt")
		Expect(err).ToNot(HaveOccurred())
		r.Close()
	})

	It("is busy while the operation lock is taken", func() {
		b := testBackend()
		sb, _ := fixture(b, sandbox.Spec{})
		s := sb.(*sbx)
		Expect(s.op.TryLock()).To(BeTrue(), "the checkpoint lock was already taken")
		defer s.op.Unlock()
		_, _, err := sb.Checkpoint(bg(), "x")
		Expect(err).To(MatchError(sandbox.ErrBusy), "checkpoint under the lock")
		Expect(sb.Refresh(bg())).To(MatchError(sandbox.ErrBusy), "refresh under the lock")
		_, err = sb.Suspend(bg())
		Expect(err).To(MatchError(sandbox.ErrBusy), "suspend under the lock")
	})

	// A second Checkpoint, a Refresh and a Suspend that arrive while a checkpoint
	// runs get ErrBusy at once, and the first checkpoint is not disturbed.
	It("is busy for a concurrent checkpoint", func() {
		b := testBackend()
		sb, st := fixture(b, sandbox.Spec{})
		s := sb.(*sbx)
		sh(sb, "echo one > proj/one.txt")
		entered, release := make(chan struct{}), make(chan struct{})
		s.hooks.afterRead = func() { // the first checkpoint is running here
			close(entered)
			select {
			case <-release:
			case <-time.After(60 * time.Second):
			}
		}
		errc := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, _, err := sb.Checkpoint(bg(), "first")
			errc <- err
		}()
		select {
		case <-entered:
		case <-time.After(60 * time.Second):
			close(release)
			Fail("the first checkpoint did not start")
		}
		_, _, err2 := sb.Checkpoint(bg(), "second")
		rerr := sb.Refresh(bg())
		_, serr := sb.Suspend(bg())
		close(release)
		Expect(<-errc).ToNot(HaveOccurred(), "the first checkpoint")
		for name, err := range map[string]error{"checkpoint": err2, "refresh": rerr, "suspend": serr} {
			Expect(err).To(MatchError(sandbox.ErrBusy), "%s during a checkpoint", name)
		}
		r, _, err := st.Open(bg(), sbtest.Admin, "/proj/one.txt")
		Expect(err).ToNot(HaveOccurred())
		r.Close()
	})

	It("cleans up when the ready timeout passes", func() {
		b := testBackend()
		b.opts.ReadyTimeout = time.Nanosecond // over before the first try
		st := sbtest.OpenStore()
		_, err := st.Mkdir(bg(), sbtest.Admin, "/proj")
		Expect(err).ToNot(HaveOccurred())
		v, _ := st.View(bg(), sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
		sb, err := b.Create(bg(), sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v, Memory: 1 << 30, Disk: 1 << 30})
		if err == nil {
			sb.Destroy(bg()) // do not leave a sandbox behind when the test fails
		}
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not ready"))
		Expect(sessionFolders(b.opts.StateDir)).To(BeEmpty(), "a failed create left session folders")
	})

	It("refuses the resumes it must refuse", func() {
		b := testBackend()
		sb, st := fixture(b, sandbox.Spec{Env: sandbox.Env{Ports: map[int]sandbox.PortHandler{7000: sbtest.EchoHandler}}})
		_, err := b.Resume(bg(), sandbox.SnapshotRef{Backend: "chv", Path: sb.(*sbx).dir}, sandbox.Env{Store: st, Actor: sbtest.Admin})
		Expect(err).To(MatchError(workspace.ErrInvalid), "snapshot of another backend")
		_, err = b.Resume(bg(), sandbox.SnapshotRef{Backend: "gvisor", Path: "/etc"}, sandbox.Env{Store: st, Actor: sbtest.Admin})
		Expect(err).To(MatchError(workspace.ErrInvalid), "a folder outside StateDir")
		ref, err := sb.Suspend(bg())
		Expect(err).ToNot(HaveOccurred())
		_, err = b.Resume(bg(), ref, sandbox.Env{Store: st, Actor: sbtest.Agent, Ports: map[int]sandbox.PortHandler{7000: sbtest.EchoHandler}})
		Expect(err).To(MatchError(workspace.ErrInvalid), "another actor")
		_, err = b.Resume(bg(), ref, sandbox.Env{Store: st, Actor: sbtest.Admin})
		Expect(err).To(MatchError(workspace.ErrInvalid), "other ports")
		next, err := b.Resume(bg(), ref, sandbox.Env{Store: st, Actor: sbtest.Admin, Ports: map[int]sandbox.PortHandler{7000: sbtest.EchoHandler}})
		Expect(err).ToNot(HaveOccurred())
		defer next.Destroy(bg())
		_, err = b.Resume(bg(), ref, sandbox.Env{Store: st, Actor: sbtest.Admin, Ports: map[int]sandbox.PortHandler{7000: sbtest.EchoHandler}})
		Expect(err).To(MatchError(workspace.ErrInvalid), "a sandbox that already runs")
		// The old handle no longer owns the folder.
		_, _, err = sb.Checkpoint(bg(), "stale")
		Expect(err).To(MatchError(sandbox.ErrGone), "stale handle")
	})

	// A host without the AppArmor profile gets ErrUnsupported with a reason that
	// names the fix. The test needs a host where the profile for
	// SANDBOX_TEST_RUNSC is not loaded: run it with SANDBOX_EXPECT_NO_PROFILE=1.
	It("names the fix when New runs without the profile", func() {
		runsc := os.Getenv("SANDBOX_TEST_RUNSC")
		if runsc == "" || os.Getenv("SANDBOX_EXPECT_NO_PROFILE") != "1" {
			Skip("set SANDBOX_TEST_RUNSC and SANDBOX_EXPECT_NO_PROFILE=1 on a host where its AppArmor profile is not loaded")
		}
		_, err := New(Options{StateDir: shortDir(), CacheDir: GinkgoT().TempDir(), Runsc: runsc})
		Expect(err).To(MatchError(sandbox.ErrUnsupported))
		Expect(err.Error()).To(ContainSubstring("gvisor.Profile"))
	})

	// Two sandboxes of one backend run at the same time, from one unpacked image,
	// each with its own state, sockets and files.
	It("runs two sandboxes at once", func() {
		b := testBackend()
		sb1, _ := fixture(b, sandbox.Spec{})
		sb2, _ := fixture(b, sandbox.Spec{})
		var out [2]bytes.Buffer
		var procs [2]sandbox.Process
		for i, sb := range []sandbox.Sandbox{sb1, sb2} {
			p, err := sb.Exec(bg(), sandbox.Cmd{
				Args:   []string{"sh", "-c", "echo " + strconv.Itoa(i+1) + " > proj/who.txt; sleep 1; cat proj/who.txt"},
				Stdout: &out[i],
			})
			Expect(err).ToNot(HaveOccurred())
			procs[i] = p
		}
		for i, p := range procs {
			code, err := p.Wait()
			Expect(err).ToNot(HaveOccurred(), "sandbox %d", i+1)
			Expect(code).To(BeZero(), "sandbox %d", i+1)
		}
		Expect(out[0].String()).To(Equal("1\n"))
		Expect(out[1].String()).To(Equal("2\n"))
		Expect(sb1.ID()).ToNot(Equal(sb2.ID()), "two sandboxes have one ID")
	})

	// A restore that fails leaves the checkpoint and the suspended state where
	// they are, so the caller can retry.
	It("keeps the checkpoint after a failed resume", func() {
		b := testBackend()
		sb, st := fixture(b, sandbox.Spec{})
		s := sb.(*sbx)
		ref, err := sb.Suspend(bg())
		Expect(err).ToNot(HaveOccurred())
		snap := filepath.Join(s.dir, "checkpoint")
		files, err := os.ReadDir(snap)
		Expect(err).ToNot(HaveOccurred(), "the checkpoint folder")
		Expect(files).ToNot(BeEmpty(), "the checkpoint folder")
		var saved []string
		for _, f := range files {
			p := filepath.Join(snap, f.Name())
			raw, err := os.ReadFile(p)
			Expect(err).ToNot(HaveOccurred())
			saved = append(saved, p, string(raw))
			Expect(os.WriteFile(p, nil, 0o600)).To(Succeed())
		}
		_, err = b.Resume(bg(), ref, sandbox.Env{Store: st, Actor: sbtest.Admin})
		Expect(err).To(HaveOccurred(), "a resume from a broken checkpoint worked")
		left, _ := os.ReadDir(snap)
		Expect(left).To(HaveLen(len(files)), "the failed resume changed the checkpoint folder")
		got, err := loadState(s.dir)
		Expect(err).ToNot(HaveOccurred(), "state after the failed resume")
		Expect(got.Suspended).To(BeTrue(), "state after the failed resume: %+v", got)
		// With the files back, the retry works.
		for i := 0; i < len(saved); i += 2 {
			Expect(os.WriteFile(saved[i], []byte(saved[i+1]), 0o600)).To(Succeed())
		}
		next, err := b.Resume(bg(), ref, sandbox.Env{Store: st, Actor: sbtest.Admin})
		Expect(err).ToNot(HaveOccurred(), "retry")
		defer next.Destroy(bg())
		sh(next, "echo back > proj/back.txt")
	})

	// The files of the workspace come from read-only blobs (mode 0444). A command
	// has CAP_DAC_OVERRIDE, so it can change them in every way a program does:
	// truncate and write, write in place, replace through a rename. The copy of
	// the file in the upper layer is a host file, and the host must let the
	// sandbox write it (runsc runs with --directfs=false for this).
	It("allows every way of editing a read-only workspace file", func() {
		b := testBackend()
		sb, st := fixture(b, sandbox.Spec{})
		for _, name := range []string{"a", "b", "c", "d", "e"} {
			_, err := st.Put(bg(), sbtest.Admin, "/proj/"+name+".txt", strings.NewReader("old "+name+"\n"), workspace.PutOptions{Overwrite: true})
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(sb.Refresh(bg())).To(Succeed()) // the guest sees the files
		got := sh(sb, `cd proj
echo new > a.txt
python3 -c "open('b.txt', 'w').write('new b\n')"
sed -i 's/old/new/' c.txt
: > d.txt && echo new d >> d.txt
cp /dev/null e.txt && echo new e > e.txt
cat a.txt b.txt c.txt d.txt e.txt`)
		Expect(got).To(Equal("new\nnew b\nnew c\nnew d\nnew e\n"), "after the edits")
		_, c, err := sb.Checkpoint(bg(), "edits")
		Expect(err).ToNot(HaveOccurred())
		Expect(c).To(BeEmpty())
		r, _, err := st.Open(bg(), sbtest.Admin, "/proj/c.txt")
		Expect(err).ToNot(HaveOccurred())
		defer r.Close()
		body, _ := io.ReadAll(r)
		Expect(string(body)).To(Equal("new c\n"), "c.txt in the store")
	})

	// While a Suspend runs, the port listeners refuse new connections: a guest
	// process must not open one between the close of the old ones and the
	// checkpoint of runsc. When the Suspend fails, the ports work again.
	It("refuses port connections during a failing suspend", func() {
		b := testBackend()
		sb, _ := fixture(b, sandbox.Spec{Env: sandbox.Env{Ports: map[int]sandbox.PortHandler{7000: sbtest.EchoHandler}}})
		s := sb.(*sbx)
		const probe = `python3 -c "
import socket
c = socket.create_connection(('127.0.0.1', 7000), 5)
c.sendall(b'ping')
print('echo', c.recv(4).decode())"`
		Expect(sh(sb, probe)).To(Equal("echo ping\n"), "before the suspend")
		var during string
		s.hooks.suspendSave = func() error { // the ports are gated here
			c, err := net.Dial("unix", portSock(s.dir, 7000))
			if err != nil {
				during = "dial: " + err.Error()
				return errors.New("injected save failure")
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(10 * time.Second))
			_, _ = c.Write([]byte("ping"))
			buf := make([]byte, 4)
			n, err := c.Read(buf)
			during = "read " + strconv.Itoa(n) + " bytes: " + fmt.Sprint(err)
			if n == 0 && err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
				during = "refused"
			}
			return errors.New("injected save failure")
		}
		_, err := sb.Suspend(bg())
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, sandbox.ErrGone)).To(BeFalse(), "Suspend = %v, want the injected error and no ErrGone", err)
		s.hooks.suspendSave = nil
		Expect(during).To(Equal("refused"), "a connection during the suspend")
		Expect(sh(sb, probe)).To(Equal("echo ping\n"), "after the failed suspend")
	})
})
