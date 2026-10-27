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
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func bg() context.Context { return context.Background() }

// A command in the sandbox must not reach the agent socket or a port socket
// by the path, and not through the root and working folder links of the agent
// or of the shell itself (/proc/<pid>/root, /proc/<pid>/cwd). The port itself
// still works for the guest through 127.0.0.1. Every route is attempted and
// must fail to connect. (Paths of the form /proc/<pid>/fd/<n>/name fail with
// ENOTDIR whatever the agent does, so they are not listed: they would prove
// nothing.)
func TestACommandCannotReachTheControlSockets(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, sandbox.Spec{Env: sandbox.Env{Ports: map[int]sandbox.PortHandler{7000: sbtest.EchoHandler}}})
	got := sh(t, sb, `python3 - <<'EOF'
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
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// A command can make its own user and mount namespace (unshare -Urm). The
// copied mounts of that namespace must still not give it the control sockets,
// by umount of the cover on /mnt/ctl, by a bind of /mnt (which is no mount
// point), or by a recursive bind of /mnt. The control route shows that a
// socket outside /mnt/ctl is reachable through a recursive bind in such a
// namespace, so the other routes fail for the right reason. Every route must
// enter its namespace: a guest that cannot make one, or an image without
// unshare, fails the test instead of passing it vacuously.
func TestACommandCannotReachTheControlSocketsFromAUserNamespace(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, sandbox.Spec{Env: sandbox.Env{Ports: map[int]sandbox.PortHandler{7000: sbtest.EchoHandler}}})
	got := sh(t, sb, `command -v unshare >/dev/null || { echo no unshare binary in the image; exit 1; }
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
	for _, name := range []string{"umount", "bind", "rbind", "control"} {
		lines := strings.Join(routes[name], "\n")
		if !strings.Contains(lines, "entered") {
			t.Errorf("route %s did not enter its namespace:\n%s", name, lines)
		}
		reached := strings.Contains(lines, "reached ")
		if name == "control" && !reached {
			t.Errorf("the control route reached nothing, so the others prove nothing:\n%s", lines)
		}
		if name != "control" && reached {
			t.Errorf("route %s reached a control socket:\n%s", name, lines)
		}
	}
	if t.Failed() {
		t.Logf("full output:\n%s", got)
	}
}

// The commands get only CAP_DAC_OVERRIDE and CAP_FOWNER, as under chv. The
// ambient capabilities of the agent itself must not leak to them.
func TestCommandsKeepOnlyTwoCapabilities(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, sandbox.Spec{})
	got := sh(t, sb, `grep -E '^(CapInh|CapPrm|CapEff|CapAmb|NoNewPrivs)' /proc/self/status; id -u`)
	for _, want := range []string{"CapPrm:\t000000000000000a", "CapEff:\t000000000000000a", "CapAmb:\t000000000000000a", "NoNewPrivs:\t1", "\n1000\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestMemoryWatcherStopsASandboxOverItsLimit(t *testing.T) {
	b := testBackend(t)
	b.memEvery = 200 * time.Millisecond
	sb, _ := fixture(t, b, sandbox.Spec{Memory: 256 << 20})
	s := sb.(*sbx)
	p, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"python3", "-c", "b = b'x' * (700 << 20); import time; time.sleep(120)"}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		if _, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"true"}}); errors.Is(err, sandbox.ErrGone) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sandbox was not stopped")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := p.Wait(); !errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("wait after the stop: %v", err)
	}
	if log, _ := os.ReadFile(filepath.Join(s.dir, "console.log")); !strings.Contains(string(log), "memory limit") {
		t.Fatalf("the reason is not in the log:\n%s", log)
	}
	// What the guest wrote before is still there to checkpoint.
	if _, _, err := sb.Checkpoint(bg(), "after the stop"); err != nil {
		t.Fatal(err)
	}
}

func TestDiskWatcherRefusesCommandsUntilARefresh(t *testing.T) {
	b := testBackend(t)
	b.diskEvery = 200 * time.Millisecond
	sb, st := fixture(t, b, sandbox.Spec{Disk: 64 << 20})
	sh(t, sb, "head -c 40000000 /dev/zero > proj/a.bin && head -c 40000000 /dev/zero > proj/b.bin")
	deadline := time.Now().Add(60 * time.Second)
	var err error
	for {
		if _, err = sb.Exec(bg(), sandbox.Cmd{Args: []string{"true"}}); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Exec was not refused")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !errors.Is(err, ErrDiskFull) || err.Error() != "The sandbox disk is full." {
		t.Fatalf("err = %v", err)
	}
	// A checkpoint works, and measures the disk again. The upper layer keeps
	// the files, so the disk stays full until a refresh gives a new upper.
	if _, _, err := sb.Checkpoint(bg(), "full"); err != nil {
		t.Fatal(err)
	}
	if _, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"true"}}); !errors.Is(err, ErrDiskFull) {
		t.Fatalf("after a checkpoint: %v", err)
	}
	if err := sb.Refresh(bg()); err != nil {
		t.Fatal(err)
	}
	if got := sh(t, sb, "ls proj | tr '\\n' ' '"); got != "a.bin b.bin " {
		t.Fatalf("after the refresh: %q", got)
	}
	r, _, err := st.Open(bg(), sbtest.Admin, "/proj/b.bin")
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}

// A single file over the disk size stops at the size limit of the process.
func TestOneFileCannotPassTheDiskSize(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, sandbox.Spec{Disk: 64 << 20})
	var errb bytes.Buffer
	p, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"sh", "-c", "head -c 100000000 /dev/zero > proj/big.bin; echo rc=$?; ls -l proj/big.bin | cut -d' ' -f5"}, Stderr: &errb, Stdout: &errb})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errb.String(), "67108864") {
		t.Fatalf("the file is not cut at 64 MiB: %q", errb.String())
	}
}

const tickScript = `exec 3<proj/notes.txt 4>>proj/tick.txt; echo $$ > /tmp/tick.pid; while :; do cat /dev/fd/3 >/dev/null && echo tick >&4; sleep 0.1; done`

func resume(t *testing.T, b *Backend, sb sandbox.Sandbox, st workspace.Store) sandbox.Sandbox {
	t.Helper()
	ref, err := sb.Suspend(bg())
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}
	next, err := b.Resume(bg(), ref, sandbox.Env{Store: st, Actor: sbtest.Admin})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	t.Cleanup(func() { next.Destroy(bg()) })
	return next
}

func TestFiveSuspendsInARowKeepTheProcessAndItsFiles(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, sandbox.Spec{})
	sh(t, sb, "echo notes > proj/notes.txt")
	if _, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"sh", "-c", tickScript}}); err != nil {
		t.Fatal(err)
	}
	sh(t, sb, `i=0; while [ ! -s proj/tick.txt ]; do i=$((i+1)); [ $i -gt 300 ] && { echo "proj/tick.txt never appeared" >&2; exit 1; }; sleep 0.1; done`)
	for i := range 5 {
		sb = resume(t, b, sb, st)
		if got := sh(t, sb, `a=$(wc -l < proj/tick.txt); sleep 1; b=$(wc -l < proj/tick.txt); [ "$b" -gt "$a" ] && echo alive`); got != "alive\n" {
			t.Fatalf("cycle %d: %q", i, got)
		}
	}
	got := sh(t, sb, `ls -l /proc/$(cat /tmp/tick.pid)/fd | grep -c -E 'notes.txt|tick.txt'`)
	if strings.TrimSpace(got) != "2" {
		t.Fatalf("open files of the process after 5 cycles: %q", got)
	}
	if _, c, err := sb.Checkpoint(bg(), "after the cycles"); err != nil || len(c) != 0 {
		t.Fatalf("checkpoint: %v %+v", err, c)
	}
}

func TestATerminalProcessSurvivesSuspendAndResume(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, sandbox.Spec{})
	script := `import os, time
f = open('proj/tty.log', 'a', buffering=1)
while True:
    f.write('tick tty=%s\n' % (os.isatty(0) and os.isatty(1)))
    time.sleep(0.1)`
	p, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"python3", "-c", script}, TTY: &sandbox.TTY{Rows: 24, Cols: 80}})
	if err != nil {
		t.Fatal(err)
	}
	sh(t, sb, `i=0; while [ ! -s proj/tty.log ]; do i=$((i+1)); [ $i -gt 300 ] && { echo "proj/tty.log never appeared" >&2; exit 1; }; sleep 0.1; done`)
	sb = resume(t, b, sb, st)
	if _, err := p.Wait(); !errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("the old handle: %v", err)
	}
	got := sh(t, sb, `a=$(wc -l < proj/tty.log); sleep 1; b=$(wc -l < proj/tty.log); [ "$b" -gt "$a" ] && echo alive; tail -n 1 proj/tty.log`)
	if got != "alive\ntick tty=True\n" {
		t.Fatalf("after resume: %q", got)
	}
}

// A checkpoint during a large copy-up commits the whole file or leaves it
// alone: the sandbox is paused until the system call that copies the file
// has finished. The test proves that the checkpoint overlapped the writer
// process: the writer had started (a marker in the upper layer) and had not
// finished (no end marker) when the sandbox was paused. It does not prove that
// the pause landed inside the copy itself; the content check (the exact old or
// the exact new bytes) is what catches a torn file. A run where the
// writer was already done does not count; the test tries again, a bounded
// number of times, and fails when no attempt overlapped.
func TestCheckpointDuringALargeCopyUpIsWholeOrNothing(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, sandbox.Spec{})
	s := sb.(*sbx)
	const size = 96 << 20
	big := bytes.Repeat([]byte("0123456789abcdef"), size/16)
	appended := append(append([]byte{}, big...), "x\n"...)
	content := func() []byte {
		r, _, err := st.Open(bg(), sbtest.Admin, "/proj/big.bin")
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		body, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	upperProj := filepath.Join(s.dir, "upper", "data", "proj")
	const attempts = 8
	overlapped := false
	for i := 0; i < attempts && !overlapped; i++ {
		if _, err := st.Put(bg(), sbtest.Admin, "/proj/big.bin", bytes.NewReader(big), workspace.PutOptions{Overwrite: true}); err != nil {
			t.Fatal(err)
		}
		if err := sb.Refresh(bg()); err != nil { // the guest sees the original big.bin, in the lower layer
			t.Fatal(err)
		}
		started, finished := "started"+strconv.Itoa(i), "finished"+strconv.Itoa(i)
		p, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"sh", "-c", "echo go > proj/" + started + "; echo x >> proj/big.bin; echo end > proj/" + finished}})
		if err != nil {
			t.Fatal(err)
		}
		// The upper layer is a host folder: the marker shows that the writer runs.
		deadline := time.Now().Add(30 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(upperProj, started)); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the writer did not start")
			}
			time.Sleep(2 * time.Millisecond)
		}
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
		if err != nil {
			t.Fatal(err)
		}
		if body := content(); !bytes.Equal(body, big) && !bytes.Equal(body, appended) {
			t.Fatalf("attempt %d: the store has %d bytes that are neither the old nor the new content: a torn copy", i, len(body))
		}
		if _, err := p.Wait(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := sb.Checkpoint(bg(), "after"); err != nil {
			t.Fatal(err)
		}
		if body := content(); !bytes.Equal(body, appended) {
			t.Fatalf("attempt %d: after the writer ended the store has %d bytes, not the old content plus \"x\\n\"", i, len(body))
		}
		if !sawEnd {
			overlapped = true
			t.Logf("attempt %d: the writer was running at the pause (upper big.bin was %d bytes)", i, upperSize)
		}
	}
	if !overlapped {
		t.Fatalf("no checkpoint overlapped the writer in %d attempts: the test proved nothing", attempts)
	}
}

func TestCrashGivesErrGoneAndCheckpointStillWorks(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, sandbox.Spec{})
	sh(t, sb, "echo kept > proj/kept.txt")
	s := sb.(*sbx)
	if err := syscall.Kill(s.in.pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.in.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the watcher did not see the crash")
	}
	if _, err := sb.Exec(bg(), sandbox.Cmd{Args: []string{"true"}}); !errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("exec after a crash: %v", err)
	}
	if _, _, err := sb.Checkpoint(bg(), "after crash"); err != nil {
		t.Fatal(err)
	}
	r, _, err := st.Open(bg(), sbtest.Admin, "/proj/kept.txt")
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}

func TestBusy(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, sandbox.Spec{})
	s := sb.(*sbx)
	if !s.op.TryLock() {
		t.Fatal("the checkpoint lock was already taken")
	}
	defer s.op.Unlock()
	if _, _, err := sb.Checkpoint(bg(), "x"); !errors.Is(err, sandbox.ErrBusy) {
		t.Fatalf("checkpoint under the lock: %v", err)
	}
	if err := sb.Refresh(bg()); !errors.Is(err, sandbox.ErrBusy) {
		t.Fatalf("refresh under the lock: %v", err)
	}
	if _, err := sb.Suspend(bg()); !errors.Is(err, sandbox.ErrBusy) {
		t.Fatalf("suspend under the lock: %v", err)
	}
}

// A second Checkpoint, a Refresh and a Suspend that arrive while a checkpoint
// runs get ErrBusy at once, and the first checkpoint is not disturbed.
func TestConcurrentCheckpointIsBusy(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, sandbox.Spec{})
	s := sb.(*sbx)
	sh(t, sb, "echo one > proj/one.txt")
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
		_, _, err := sb.Checkpoint(bg(), "first")
		errc <- err
	}()
	select {
	case <-entered:
	case <-time.After(60 * time.Second):
		close(release)
		t.Fatal("the first checkpoint did not start")
	}
	_, _, err2 := sb.Checkpoint(bg(), "second")
	rerr := sb.Refresh(bg())
	_, serr := sb.Suspend(bg())
	close(release)
	if err := <-errc; err != nil {
		t.Fatalf("the first checkpoint: %v", err)
	}
	for name, err := range map[string]error{"checkpoint": err2, "refresh": rerr, "suspend": serr} {
		if !errors.Is(err, sandbox.ErrBusy) {
			t.Fatalf("%s during a checkpoint: %v", name, err)
		}
	}
	r, _, err := st.Open(bg(), sbtest.Admin, "/proj/one.txt")
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}

func TestReadyTimeoutCleansUp(t *testing.T) {
	b := testBackend(t)
	b.opts.ReadyTimeout = time.Nanosecond // over before the first try
	st := sbtest.OpenStore(t)
	if _, err := st.Mkdir(bg(), sbtest.Admin, "/proj"); err != nil {
		t.Fatal(err)
	}
	v, _ := st.View(bg(), sbtest.Admin, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}})
	sb, err := b.Create(bg(), sandbox.Spec{Env: sandbox.Env{Store: st, Actor: sbtest.Admin}, View: v, Memory: 1 << 30, Disk: 1 << 30})
	if err == nil {
		sb.Destroy(bg()) // do not leave a sandbox behind when the test fails
	}
	if err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("create = %v", err)
	}
	if ents := sessionFolders(b.opts.StateDir); len(ents) != 0 {
		t.Fatalf("a failed create left %d session folders", len(ents))
	}
}

func TestResumeRefusals(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, sandbox.Spec{Env: sandbox.Env{Ports: map[int]sandbox.PortHandler{7000: sbtest.EchoHandler}}})
	if _, err := b.Resume(bg(), sandbox.SnapshotRef{Backend: "chv", Path: sb.(*sbx).dir}, sandbox.Env{Store: st, Actor: sbtest.Admin}); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("snapshot of another backend: %v", err)
	}
	if _, err := b.Resume(bg(), sandbox.SnapshotRef{Backend: "gvisor", Path: "/etc"}, sandbox.Env{Store: st, Actor: sbtest.Admin}); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("a folder outside StateDir: %v", err)
	}
	ref, err := sb.Suspend(bg())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Resume(bg(), ref, sandbox.Env{Store: st, Actor: sbtest.Agent, Ports: map[int]sandbox.PortHandler{7000: sbtest.EchoHandler}}); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("another actor: %v", err)
	}
	if _, err := b.Resume(bg(), ref, sandbox.Env{Store: st, Actor: sbtest.Admin}); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("other ports: %v", err)
	}
	next, err := b.Resume(bg(), ref, sandbox.Env{Store: st, Actor: sbtest.Admin, Ports: map[int]sandbox.PortHandler{7000: sbtest.EchoHandler}})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Destroy(bg())
	if _, err := b.Resume(bg(), ref, sandbox.Env{Store: st, Actor: sbtest.Admin, Ports: map[int]sandbox.PortHandler{7000: sbtest.EchoHandler}}); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("a sandbox that already runs: %v", err)
	}
	// The old handle no longer owns the folder.
	if _, _, err := sb.Checkpoint(bg(), "stale"); !errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("stale handle: %v", err)
	}
}

// A host without the AppArmor profile gets ErrUnsupported with a reason that
// names the fix. The test needs a host where the profile for
// SANDBOX_TEST_RUNSC is not loaded: run it with SANDBOX_EXPECT_NO_PROFILE=1.
func TestNewWithoutTheProfileNamesTheFix(t *testing.T) {
	runsc := os.Getenv("SANDBOX_TEST_RUNSC")
	if runsc == "" || os.Getenv("SANDBOX_EXPECT_NO_PROFILE") != "1" {
		t.Skip("set SANDBOX_TEST_RUNSC and SANDBOX_EXPECT_NO_PROFILE=1 on a host where its AppArmor profile is not loaded")
	}
	_, err := New(Options{StateDir: shortDir(t), CacheDir: t.TempDir(), Runsc: runsc})
	if !errors.Is(err, sandbox.ErrUnsupported) || !strings.Contains(err.Error(), "gvisor.Profile") {
		t.Fatalf("New = %v", err)
	}
}

// Two sandboxes of one backend run at the same time, from one unpacked image,
// each with its own state, sockets and files.
func TestTwoSandboxesRunAtOnce(t *testing.T) {
	b := testBackend(t)
	sb1, _ := fixture(t, b, sandbox.Spec{})
	sb2, _ := fixture(t, b, sandbox.Spec{})
	var out [2]bytes.Buffer
	var procs [2]sandbox.Process
	for i, sb := range []sandbox.Sandbox{sb1, sb2} {
		p, err := sb.Exec(bg(), sandbox.Cmd{
			Args:   []string{"sh", "-c", "echo " + strconv.Itoa(i+1) + " > proj/who.txt; sleep 1; cat proj/who.txt"},
			Stdout: &out[i],
		})
		if err != nil {
			t.Fatal(err)
		}
		procs[i] = p
	}
	for i, p := range procs {
		if code, err := p.Wait(); err != nil || code != 0 {
			t.Fatalf("sandbox %d: %d %v", i+1, code, err)
		}
	}
	if out[0].String() != "1\n" || out[1].String() != "2\n" {
		t.Fatalf("got %q %q", out[0].String(), out[1].String())
	}
	if sb1.ID() == sb2.ID() {
		t.Fatal("two sandboxes have one ID")
	}
}

// A restore that fails leaves the checkpoint and the suspended state where
// they are, so the caller can retry.
func TestFailedResumeKeepsTheCheckpoint(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, sandbox.Spec{})
	s := sb.(*sbx)
	ref, err := sb.Suspend(bg())
	if err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(s.dir, "checkpoint")
	files, err := os.ReadDir(snap)
	if err != nil || len(files) == 0 {
		t.Fatalf("the checkpoint folder: %v %v", files, err)
	}
	var saved []string
	for _, f := range files {
		p := filepath.Join(snap, f.Name())
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		saved = append(saved, p, string(raw))
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Resume(bg(), ref, sandbox.Env{Store: st, Actor: sbtest.Admin}); err == nil {
		t.Fatal("a resume from a broken checkpoint worked")
	}
	if left, _ := os.ReadDir(snap); len(left) != len(files) {
		t.Fatalf("the failed resume changed the checkpoint folder: %d files, was %d", len(left), len(files))
	}
	if got, err := loadState(s.dir); err != nil || !got.Suspended {
		t.Fatalf("state after the failed resume: %+v %v", got, err)
	}
	// With the files back, the retry works.
	for i := 0; i < len(saved); i += 2 {
		if err := os.WriteFile(saved[i], []byte(saved[i+1]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	next, err := b.Resume(bg(), ref, sandbox.Env{Store: st, Actor: sbtest.Admin})
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	defer next.Destroy(bg())
	sh(t, next, "echo back > proj/back.txt")
}

// The files of the workspace come from read-only blobs (mode 0444). A command
// has CAP_DAC_OVERRIDE, so it can change them in every way a program does:
// truncate and write, write in place, replace through a rename. The copy of
// the file in the upper layer is a host file, and the host must let the
// sandbox write it (runsc runs with --directfs=false for this).
func TestEveryWayOfEditingAReadOnlyWorkspaceFile(t *testing.T) {
	b := testBackend(t)
	sb, st := fixture(t, b, sandbox.Spec{})
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		if _, err := st.Put(bg(), sbtest.Admin, "/proj/"+name+".txt", strings.NewReader("old "+name+"\n"), workspace.PutOptions{Overwrite: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sb.Refresh(bg()); err != nil { // the guest sees the files
		t.Fatal(err)
	}
	got := sh(t, sb, `cd proj
echo new > a.txt
python3 -c "open('b.txt', 'w').write('new b\n')"
sed -i 's/old/new/' c.txt
: > d.txt && echo new d >> d.txt
cp /dev/null e.txt && echo new e > e.txt
cat a.txt b.txt c.txt d.txt e.txt`)
	if want := "new\nnew b\nnew c\nnew d\nnew e\n"; got != want {
		t.Fatalf("after the edits: %q, want %q", got, want)
	}
	if _, c, err := sb.Checkpoint(bg(), "edits"); err != nil || len(c) != 0 {
		t.Fatalf("checkpoint: %v %+v", err, c)
	}
	r, _, err := st.Open(bg(), sbtest.Admin, "/proj/c.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if body, _ := io.ReadAll(r); string(body) != "new c\n" {
		t.Fatalf("c.txt in the store: %q", body)
	}
}

// While a Suspend runs, the port listeners refuse new connections: a guest
// process must not open one between the close of the old ones and the
// checkpoint of runsc. When the Suspend fails, the ports work again.
func TestPortGateRefusesConnectionsDuringAFailingSuspend(t *testing.T) {
	b := testBackend(t)
	sb, _ := fixture(t, b, sandbox.Spec{Env: sandbox.Env{Ports: map[int]sandbox.PortHandler{7000: sbtest.EchoHandler}}})
	s := sb.(*sbx)
	const probe = `python3 -c "
import socket
c = socket.create_connection(('127.0.0.1', 7000), 5)
c.sendall(b'ping')
print('echo', c.recv(4).decode())"`
	if got := sh(t, sb, probe); got != "echo ping\n" {
		t.Fatalf("before the suspend: %q", got)
	}
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
	if _, err := sb.Suspend(bg()); err == nil || errors.Is(err, sandbox.ErrGone) {
		t.Fatalf("Suspend = %v, want the injected error and no ErrGone", err)
	}
	s.hooks.suspendSave = nil
	if during != "refused" {
		t.Fatalf("a connection during the suspend: %s", during)
	}
	if got := sh(t, sb, probe); got != "echo ping\n" {
		t.Fatalf("after the failed suspend: %q", got)
	}
}
