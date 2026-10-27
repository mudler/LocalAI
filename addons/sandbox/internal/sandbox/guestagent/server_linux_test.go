package guestagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// agent starts a Server on a Unix socket and returns a dial function.
func agent(t *testing.T, srv *Server) func() net.Conn {
	t.Helper()
	if srv.Spawner == nil {
		srv.Spawner = &LinuxSpawner{Env: []string{"PATH=" + os.Getenv("PATH")}, Dir: t.TempDir()}
	}
	sock := filepath.Join(t.TempDir(), "agent.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go srv.Serve(l)
	return func() net.Conn {
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
}

func run(t *testing.T, dial func() net.Conn, req ExecRequest, stdin io.Reader) (int, string, string) {
	t.Helper()
	var out, errb lockedBuffer
	p, err := Exec(context.Background(), dial(), req, stdin, &out, &errb)
	if err != nil {
		t.Fatal(err)
	}
	code, err := p.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

func sh(script string) ExecRequest { return ExecRequest{Args: []string{"sh", "-c", script}} }

func waitUntil(d time.Duration, cond func() bool) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

func groupAlive(pgid int) bool { return syscall.Kill(-pgid, 0) == nil }

func TestExecPipes(t *testing.T) {
	dial := agent(t, &Server{})
	code, out, errs := run(t, dial, sh("echo out; echo err >&2; exit 3"), nil)
	if code != 3 || out != "out\n" || errs != "err\n" {
		t.Fatalf("got %d %q %q", code, out, errs)
	}
	big := strings.Repeat("x", 3<<20)
	code, out, _ = run(t, dial, ExecRequest{Args: []string{"cat"}}, strings.NewReader(big))
	if code != 0 || out != big {
		t.Fatalf("cat returned %d bytes, code %d", len(out), code)
	}
	code, out, _ = run(t, dial, ExecRequest{Args: []string{"sh", "-c", `echo "$A $B"; pwd`}, Env: []string{"A=1", "B=2"}, Dir: "/"}, nil)
	if code != 0 || out != "1 2\n/\n" {
		t.Fatalf("env and dir: %q", out)
	}
}

func TestExecUnknownCommand(t *testing.T) {
	dial := agent(t, &Server{})
	_, err := Exec(context.Background(), dial(), ExecRequest{Args: []string{"no-such-command-here"}}, nil, nil, nil)
	var re *RemoteError
	if !errors.As(err, &re) || !strings.Contains(re.Message, "not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecSignal(t *testing.T) {
	dial := agent(t, &Server{})
	var out lockedBuffer
	p, err := Exec(context.Background(), dial(), sh("echo up; exec sleep 60"), nil, &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !waitUntil(5*time.Second, func() bool { return out.String() == "up\n" }) {
		t.Fatal("no output")
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code, err := p.Wait(); err != nil || code != 128+15 {
		t.Fatalf("wait = %d, %v", code, err)
	}
}

func TestExecTerminal(t *testing.T) {
	dial := agent(t, &Server{})
	var out lockedBuffer
	pr, pw := io.Pipe()
	req := ExecRequest{Args: []string{"sh", "-c", `test -t 0 && test -t 1 && echo is-a-tty; stty size; read -r x; stty size`}, TTY: &TTY{Rows: 24, Cols: 80}}
	p, err := Exec(context.Background(), dial(), req, pr, &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !waitUntil(5*time.Second, func() bool { return strings.Contains(out.String(), "24 80") }) {
		t.Fatalf("output = %q", out.String())
	}
	if err := p.Resize(30, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := pw.Write([]byte("go\n")); err != nil {
		t.Fatal(err)
	}
	code, err := p.Wait()
	pw.Close()
	if err != nil || code != 0 || !strings.Contains(out.String(), "is-a-tty") || !strings.Contains(out.String(), "30 100") {
		t.Fatalf("wait = %d, %v, output %q", code, err, out.String())
	}
	if err := (&Process{}).Resize(1, 1); !errors.Is(err, ErrNoTTY) {
		t.Fatalf("resize without a terminal: %v", err)
	}
}

func TestBackgroundChildDoesNotHoldExit(t *testing.T) {
	dial := agent(t, &Server{})
	start := time.Now()
	code, out, _ := run(t, dial, sh("sleep 30 & echo started"), nil)
	if code != 0 || out != "started\n" || time.Since(start) > 5*time.Second {
		t.Fatalf("got %d %q after %v", code, out, time.Since(start))
	}
}

// pgidOf starts a process that prints its process group and runs until it
// is killed.
func pgidOf(t *testing.T, dial func() net.Conn) (net.Conn, int) {
	t.Helper()
	c := dial()
	var out lockedBuffer
	if _, err := Exec(context.Background(), c, sh(`echo $$; sleep 60 & sleep 60`), nil, &out, nil); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(5*time.Second, func() bool { return strings.HasSuffix(out.String(), "\n") }) {
		t.Fatal("no pid")
	}
	pgid, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	return c, pgid
}

func TestCloseKillsTheGroup(t *testing.T) {
	dial := agent(t, &Server{})
	c, pgid := pgidOf(t, dial)
	c.Close()
	if !waitUntil(5*time.Second, func() bool { return !groupAlive(pgid) }) {
		t.Fatal("the process group survived the close")
	}
}

func TestDetachKeepsTheGroup(t *testing.T) {
	dial := agent(t, &Server{})
	c, pgid := pgidOf(t, dial)
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
	if err := Call(context.Background(), dial(), TypeDetach); err != nil {
		t.Fatal(err)
	}
	c.Close()
	time.Sleep(500 * time.Millisecond)
	if !groupAlive(pgid) {
		t.Fatal("a detached process group was killed")
	}
	_, after := pgidOf(t, dial)
	t.Cleanup(func() { _ = syscall.Kill(-after, syscall.SIGKILL) })
}

func TestContextEndKillsTheGroup(t *testing.T) {
	dial := agent(t, &Server{})
	ctx, cancel := context.WithCancel(context.Background())
	var out lockedBuffer
	p, err := Exec(ctx, dial(), sh(`echo $$; exec sleep 60`), nil, &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(5*time.Second, func() bool { return strings.HasSuffix(out.String(), "\n") })
	pgid, _ := strconv.Atoi(strings.TrimSpace(out.String()))
	cancel()
	if _, err := p.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait err = %v", err)
	}
	if !waitUntil(5*time.Second, func() bool { return !groupAlive(pgid) }) {
		t.Fatal("the process group survived the end of the context")
	}
}

func TestRequests(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	rec := func(name string, err error) func() error {
		return func() error { mu.Lock(); calls = append(calls, name); mu.Unlock(); return err }
	}
	dial := agent(t, &Server{Freeze: rec("freeze", nil), Thaw: rec("thaw", nil), Health: rec("health", nil), Shutdown: rec("shutdown", errors.New("no"))})
	for _, typ := range []string{TypeFreeze, TypeThaw, TypeHealth} {
		if err := Call(context.Background(), dial(), typ); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
	}
	var re *RemoteError
	if err := Call(context.Background(), dial(), TypeShutdown); !errors.As(err, &re) || re.Message != "no" {
		t.Fatalf("shutdown: %v", err)
	}
	if err := Call(context.Background(), dial(), "bogus"); !errors.As(err, &re) {
		t.Fatalf("bogus: %v", err)
	}
	if strings.Join(calls, ",") != "freeze,thaw,health,shutdown" {
		t.Fatalf("calls = %v", calls)
	}
}

func TestServerRefusesLargeFrame(t *testing.T) {
	dial := agent(t, &Server{})
	c := dial()
	defer c.Close()
	if _, err := c.Write([]byte{0x7f, 0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	f, err := ReadFrame(c)
	if err != nil || f.Type != TypeError {
		t.Fatalf("reply = %+v, %v", f, err)
	}
}

func TestReady(t *testing.T) {
	a, b := net.Pipe()
	go func() { _ = SendReady(a) }()
	if v, err := ReadReady(b, time.Second); err != nil || v != Version {
		t.Fatalf("ready = %q, %v", v, err)
	}
	go func() { _ = WriteFrame(a, Frame{Type: TypeReady, Version: "2.0"}) }()
	if _, err := ReadReady(b, time.Second); err == nil {
		t.Fatal("a different major version was accepted")
	}
}

// TestReapOrphans runs in a child process, because a reaping spawner takes
// the exit status of every child of the process.
func TestReapOrphans(t *testing.T) {
	if os.Getenv("GUESTAGENT_REAP_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestReapOrphans$", "-test.v")
		cmd.Env = append(os.Environ(), "GUESTAGENT_REAP_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child: %v\n%s", err, out)
		}
		return
	}
	const prSetChildSubreaper = 36
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0); e != 0 {
		t.Skipf("no subreaper: %v", e)
	}
	dial := agent(t, &Server{Spawner: &LinuxSpawner{Env: []string{"PATH=" + os.Getenv("PATH")}, Reap: true}})
	code, out, _ := run(t, dial, sh("sleep 0.2 >/dev/null & echo $!"), nil)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	orphan := "/proc/" + strings.TrimSpace(out)
	if !waitUntil(5*time.Second, func() bool { _, err := os.Stat(orphan); return err != nil }) {
		stat, _ := os.ReadFile(orphan + "/stat")
		t.Fatalf("the orphan was not reaped: %s", stat)
	}
}

func openFDs(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(ents)
}

// A child that never reads its stdin must not stall the frame loop: a
// signal still reaches the group, and a host close still kills it.
func TestStdinBlockedDoesNotStallSignals(t *testing.T) {
	dial := agent(t, &Server{})
	c := dial()
	var out lockedBuffer
	p, err := Exec(context.Background(), c, sh(`echo $$; exec sleep 60`), strings.NewReader(strings.Repeat("x", 1<<20)), &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !waitUntil(5*time.Second, func() bool { return strings.HasSuffix(out.String(), "\n") }) {
		t.Fatal("no pid")
	}
	pgid, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
	// SIGWINCH is ignored by default: it reaches the group without ending it.
	sent := make(chan error, 1)
	go func() { sent <- p.Signal(syscall.SIGWINCH) }()
	select {
	case err := <-sent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("signal blocked behind unread stdin")
	}
	if !groupAlive(pgid) {
		t.Fatal("the signal ended the group")
	}
	c.Close()
	if !waitUntil(5*time.Second, func() bool { return !groupAlive(pgid) }) {
		t.Fatal("host close did not kill the group")
	}
}

// Pipes and terminals, normal exits and host closes, must give back every
// descriptor they opened.
func TestNoLeakedFDs(t *testing.T) {
	dial := agent(t, &Server{})
	run(t, dial, sh("true"), nil)
	// Automatic GC would run the finalizers of leaked files and close them,
	// which would hide the leak. It is restored before the check's runtime.GC.
	oldGC := debug.SetGCPercent(-1)
	before := openFDs(t)
	for i := 0; i < 25; i++ {
		run(t, dial, sh("echo hi"), strings.NewReader("in"))
	}
	for i := 0; i < 25; i++ {
		run(t, dial, ExecRequest{Args: []string{"sh", "-c", "echo hi"}, TTY: &TTY{Rows: 24, Cols: 80}}, nil)
	}
	for i := 0; i < 10; i++ {
		c, pgid := pgidOf(t, dial)
		c.Close()
		if !waitUntil(5*time.Second, func() bool { return !groupAlive(pgid) }) {
			t.Fatal("host close did not kill the group")
		}
	}
	const margin = 4
	ok := waitUntil(10*time.Second, func() bool { return openFDs(t) <= before+margin })
	after := openFDs(t)
	debug.SetGCPercent(oldGC)
	runtime.GC()
	if !ok {
		t.Fatalf("open descriptors: %d before, %d after 60 execs", before, after)
	}
}

// A terminal without a stdin request must not get an end-of-file: cat stays
// up until it is signalled.
func TestTTYWithoutStdinSendsNoEOF(t *testing.T) {
	dial := agent(t, &Server{})
	var out lockedBuffer
	p, err := Exec(context.Background(), dial(), ExecRequest{Args: []string{"sh", "-c", "echo ready; exec cat"}, TTY: &TTY{Rows: 24, Cols: 80}}, nil, &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !waitUntil(5*time.Second, func() bool { return strings.Contains(out.String(), "ready") }) {
		t.Fatalf("output = %q", out.String())
	}
	type result struct {
		code int
		err  error
	}
	res := make(chan result, 1)
	go func() {
		code, err := p.Wait()
		res <- result{code, err}
	}()
	select {
	case r := <-res:
		t.Fatalf("the process ended without input: %+v", r)
	case <-time.After(500 * time.Millisecond):
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-res:
		if r.err != nil || r.code != 128+15 {
			t.Fatalf("wait = %d, %v", r.code, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no exit after SIGTERM")
	}
}

// A child that left the process group with setsid and keeps stdout open
// must not hold the exit frame.
func TestEscapedChildDoesNotHoldExit(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("no setsid")
	}
	dial := agent(t, &Server{})
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Cleanup(func() {
		if b, err := os.ReadFile(pidFile); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = syscall.Kill(n, syscall.SIGKILL)
			}
		}
	})
	start := time.Now()
	code, out, _ := run(t, dial, sh(`setsid sh -c 'echo $$ > `+pidFile+`; exec sleep 30' & echo started`), nil)
	if code != 0 || out != "started\n" || time.Since(start) > 5*time.Second {
		t.Fatalf("got %d %q after %v", code, out, time.Since(start))
	}
}

// pseudoRandom returns n deterministic bytes and their SHA-256 in hex.
func pseudoRandom(n int) ([]byte, string) {
	data := make([]byte, n)
	rand.New(rand.NewSource(1)).Read(data)
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:])
}

// A reader that is slower than the host writes must get all its input.
func TestLargeStdinSha256(t *testing.T) {
	dial := agent(t, &Server{})
	data, want := pseudoRandom(16 << 20)
	code, out, _ := run(t, dial, ExecRequest{Args: []string{"sha256sum"}}, bytes.NewReader(data))
	if code != 0 || out != want+"  -\n" {
		t.Fatalf("sha256sum: code %d, output %q, want %s", code, out, want)
	}
}

func TestLargeStdinCatToFile(t *testing.T) {
	dial := agent(t, &Server{})
	data, want := pseudoRandom(16 << 20)
	path := filepath.Join(t.TempDir(), "upload")
	code, _, _ := run(t, dial, sh("cat > "+path), bytes.NewReader(data))
	if code != 0 {
		t.Fatalf("cat: code %d", code)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(got)
	if len(got) != len(data) || hex.EncodeToString(sum[:]) != want {
		t.Fatalf("file has %d bytes, want %d with hash %s", len(got), len(data), want)
	}
}

// While the host waits for credit, the rest of the connection keeps working:
// a signal reaches the group, and a host close kills it.
func TestBlockedStdinKeepsSignals(t *testing.T) {
	dial := agent(t, &Server{})
	c := dial()
	var out lockedBuffer
	data, _ := pseudoRandom(8 << 20)
	p, err := Exec(context.Background(), c, sh(`echo $$; exec sleep 60`), bytes.NewReader(data), &out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !waitUntil(5*time.Second, func() bool { return strings.HasSuffix(out.String(), "\n") }) {
		t.Fatal("no pid")
	}
	pgid, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
	time.Sleep(200 * time.Millisecond) // let the feed reach the window
	sent := make(chan error, 1)
	go func() { sent <- p.Signal(syscall.SIGWINCH) }()
	select {
	case err := <-sent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("signal blocked by the stdin feed")
	}
	if !groupAlive(pgid) {
		t.Fatal("the signal ended the group")
	}
	c.Close()
	if !waitUntil(5*time.Second, func() bool { return !groupAlive(pgid) }) {
		t.Fatal("host close did not kill the group")
	}
}

// A reader that starts late, as one that loads something before it reads
// stdin does, must still get all of its input.
func TestSlowReaderGetsAllStdin(t *testing.T) {
	dial := agent(t, &Server{})
	data, want := pseudoRandom(16 << 20)
	code, out, _ := run(t, dial, sh("sleep 0.3; sha256sum"), bytes.NewReader(data))
	if code != 0 || out != want+"  -\n" {
		t.Fatalf("sha256sum: code %d, output %q, want %s", code, out, want)
	}
}

// chunkReader returns at most n bytes per read.
type chunkReader struct {
	r io.Reader
	n int
}

func (c chunkReader) Read(p []byte) (int, error) {
	if len(p) > c.n {
		p = p[:c.n]
	}
	return c.r.Read(p)
}

// A host whose reads are smaller than the 32 KiB frame must still stay
// within the byte window: the guest counts bytes, not frames.
func TestSmallReadsStayInWindow(t *testing.T) {
	dial := agent(t, &Server{})
	data, want := pseudoRandom(16 << 20)
	for _, size := range []int{1 << 10, 4 << 10, 16 << 10} {
		code, out, _ := run(t, dial, sh("sleep 0.3; sha256sum"), chunkReader{bytes.NewReader(data), size})
		if code != 0 || out != want+"  -\n" {
			t.Fatalf("reads of %d: code %d, output %q, want %s", size, code, out, want)
		}
	}
}
