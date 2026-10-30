package guestagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
func agent(srv *Server) func() net.Conn {
	if srv.Spawner == nil {
		srv.Spawner = &LinuxSpawner{Env: []string{"PATH=" + os.Getenv("PATH")}, Dir: GinkgoT().TempDir()}
	}
	sock := filepath.Join(GinkgoT().TempDir(), "agent.sock")
	l, err := net.Listen("unix", sock)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = l.Close() })
	go func() { _ = srv.Serve(l) }()
	return func() net.Conn {
		c, err := net.Dial("unix", sock)
		Expect(err).ToNot(HaveOccurred())
		return c
	}
}

func run(dial func() net.Conn, req ExecRequest, stdin io.Reader) (int, string, string) {
	var out, errb lockedBuffer
	p, err := Exec(context.Background(), dial(), req, stdin, &out, &errb)
	Expect(err).ToNot(HaveOccurred())
	code, err := p.Wait()
	Expect(err).ToNot(HaveOccurred())
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

// pgidOf starts a process that prints its process group and runs until it
// is killed.
func pgidOf(dial func() net.Conn) (net.Conn, int) {
	c := dial()
	var out lockedBuffer
	_, err := Exec(context.Background(), c, sh(`echo $$; sleep 60 & sleep 60`), nil, &out, nil)
	Expect(err).ToNot(HaveOccurred())
	Expect(waitUntil(5*time.Second, func() bool { return strings.HasSuffix(out.String(), "\n") })).To(BeTrue(), "no pid")
	pgid, err := strconv.Atoi(strings.TrimSpace(out.String()))
	Expect(err).ToNot(HaveOccurred())
	return c, pgid
}

func openFDs() int {
	ents, err := os.ReadDir("/proc/self/fd")
	Expect(err).ToNot(HaveOccurred())
	return len(ents)
}

// pseudoRandom returns n deterministic bytes and their SHA-256 in hex.
func pseudoRandom(n int) ([]byte, string) {
	data := make([]byte, n)
	rand.New(rand.NewSource(1)).Read(data)
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:])
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

var _ = Describe("agent server", func() {
	It("pipes stdin, stdout, stderr, env and dir", func() {
		dial := agent(&Server{})
		code, out, errs := run(dial, sh("echo out; echo err >&2; exit 3"), nil)
		Expect(code).To(Equal(3))
		Expect(out).To(Equal("out\n"))
		Expect(errs).To(Equal("err\n"))
		big := strings.Repeat("x", 3<<20)
		code, out, _ = run(dial, ExecRequest{Args: []string{"cat"}}, strings.NewReader(big))
		Expect(code).To(Equal(0))
		Expect(out == big).To(BeTrue(), "cat returned %d bytes", len(out))
		code, out, _ = run(dial, ExecRequest{Args: []string{"sh", "-c", `echo "$A $B"; pwd`}, Env: []string{"A=1", "B=2"}, Dir: "/"}, nil)
		Expect(code).To(Equal(0))
		Expect(out).To(Equal("1 2\n/\n"), "env and dir")
	})

	It("reports an unknown command", func() {
		dial := agent(&Server{})
		_, err := Exec(context.Background(), dial(), ExecRequest{Args: []string{"no-such-command-here"}}, nil, nil, nil)
		var re *RemoteError
		Expect(errors.As(err, &re)).To(BeTrue(), "err = %v", err)
		Expect(re.Message).To(ContainSubstring("not found"))
	})

	It("signals a process", func() {
		dial := agent(&Server{})
		var out lockedBuffer
		p, err := Exec(context.Background(), dial(), sh("echo up; exec sleep 60"), nil, &out, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(waitUntil(5*time.Second, func() bool { return out.String() == "up\n" })).To(BeTrue(), "no output")
		Expect(p.Signal(syscall.SIGTERM)).To(Succeed())
		code, err := p.Wait()
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(128 + 15))
	})

	It("runs a terminal", func() {
		dial := agent(&Server{})
		var out lockedBuffer
		pr, pw := io.Pipe()
		req := ExecRequest{Args: []string{"sh", "-c", `test -t 0 && test -t 1 && echo is-a-tty; stty size; read -r x; stty size`}, TTY: &TTY{Rows: 24, Cols: 80}}
		p, err := Exec(context.Background(), dial(), req, pr, &out, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(waitUntil(5*time.Second, func() bool { return strings.Contains(out.String(), "24 80") })).To(BeTrue(), "output = %q", out.String())
		Expect(p.Resize(30, 100)).To(Succeed())
		_, err = pw.Write([]byte("go\n"))
		Expect(err).ToNot(HaveOccurred())
		code, err := p.Wait()
		_ = pw.Close()
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(0))
		Expect(out.String()).To(ContainSubstring("is-a-tty"))
		Expect(out.String()).To(ContainSubstring("30 100"))
		Expect((&Process{}).Resize(1, 1)).To(MatchError(ErrNoTTY), "resize without a terminal")
	})

	It("does not let a background child hold the exit", func() {
		dial := agent(&Server{})
		start := time.Now()
		code, out, _ := run(dial, sh("sleep 30 & echo started"), nil)
		Expect(code).To(Equal(0))
		Expect(out).To(Equal("started\n"))
		Expect(time.Since(start)).To(BeNumerically("<=", 5*time.Second))
	})

	It("kills the group on close", func() {
		dial := agent(&Server{})
		c, pgid := pgidOf(dial)
		_ = c.Close()
		Expect(waitUntil(5*time.Second, func() bool { return !groupAlive(pgid) })).To(BeTrue(), "the process group survived the close")
	})

	It("keeps the group on detach", func() {
		dial := agent(&Server{})
		c, pgid := pgidOf(dial)
		DeferCleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
		Expect(Call(context.Background(), dial(), TypeDetach)).To(Succeed())
		_ = c.Close()
		time.Sleep(500 * time.Millisecond)
		Expect(groupAlive(pgid)).To(BeTrue(), "a detached process group was killed")
		_, after := pgidOf(dial)
		DeferCleanup(func() { _ = syscall.Kill(-after, syscall.SIGKILL) })
	})

	It("kills the group when the context ends", func() {
		dial := agent(&Server{})
		ctx, cancel := context.WithCancel(context.Background())
		var out lockedBuffer
		p, err := Exec(ctx, dial(), sh(`echo $$; exec sleep 60`), nil, &out, nil)
		Expect(err).ToNot(HaveOccurred())
		waitUntil(5*time.Second, func() bool { return strings.HasSuffix(out.String(), "\n") })
		pgid, _ := strconv.Atoi(strings.TrimSpace(out.String()))
		cancel()
		_, err = p.Wait()
		Expect(err).To(MatchError(context.Canceled))
		Expect(waitUntil(5*time.Second, func() bool { return !groupAlive(pgid) })).To(BeTrue(), "the process group survived the end of the context")
	})

	It("answers the requests", func() {
		var calls []string
		var mu sync.Mutex
		rec := func(name string, err error) func() error {
			return func() error { mu.Lock(); calls = append(calls, name); mu.Unlock(); return err }
		}
		dial := agent(&Server{Freeze: rec("freeze", nil), Thaw: rec("thaw", nil), Health: rec("health", nil), Shutdown: rec("shutdown", errors.New("no"))})
		for _, typ := range []string{TypeFreeze, TypeThaw, TypeHealth} {
			Expect(Call(context.Background(), dial(), typ)).To(Succeed(), typ)
		}
		var re *RemoteError
		err := Call(context.Background(), dial(), TypeShutdown)
		Expect(errors.As(err, &re)).To(BeTrue(), "shutdown: %v", err)
		Expect(re.Message).To(Equal("no"))
		err = Call(context.Background(), dial(), "bogus")
		Expect(errors.As(err, &re)).To(BeTrue(), "bogus: %v", err)
		Expect(strings.Join(calls, ",")).To(Equal("freeze,thaw,health,shutdown"))
	})

	It("refuses a large frame", func() {
		dial := agent(&Server{})
		c := dial()
		defer func() { _ = c.Close() }()
		_, err := c.Write([]byte{0x7f, 0xff, 0xff, 0xff})
		Expect(err).ToNot(HaveOccurred())
		f, err := ReadFrame(c)
		Expect(err).ToNot(HaveOccurred())
		Expect(f.Type).To(Equal(TypeError))
	})

	It("sends the ready frame", func() {
		a, b := net.Pipe()
		go func() { _ = SendReady(a) }()
		v, err := ReadReady(b, time.Second)
		Expect(err).ToNot(HaveOccurred())
		Expect(v).To(Equal(Version))
		go func() { _ = WriteFrame(a, Frame{Type: TypeReady, Version: "2.0"}) }()
		_, err = ReadReady(b, time.Second)
		Expect(err).To(HaveOccurred(), "a different major version was accepted")
	})

	// This spec runs in a child process, because a reaping spawner takes
	// the exit status of every child of the process.
	It("reaps orphans", func() {
		if os.Getenv("GUESTAGENT_REAP_CHILD") != "1" {
			cmd := exec.Command(os.Args[0], "-ginkgo.focus=agent server reaps orphans$")
			cmd.Env = append(os.Environ(), "GUESTAGENT_REAP_CHILD=1")
			out, err := cmd.CombinedOutput()
			Expect(err).ToNot(HaveOccurred(), "child: %s", out)
			return
		}
		const prSetChildSubreaper = 36
		if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0); e != 0 {
			Skip("no subreaper: " + e.Error())
		}
		dial := agent(&Server{Spawner: &LinuxSpawner{Env: []string{"PATH=" + os.Getenv("PATH")}, Reap: true}})
		code, out, _ := run(dial, sh("sleep 0.2 >/dev/null & echo $!"), nil)
		Expect(code).To(Equal(0))
		orphan := "/proc/" + strings.TrimSpace(out)
		reaped := waitUntil(5*time.Second, func() bool { _, err := os.Stat(orphan); return err != nil })
		stat, _ := os.ReadFile(orphan + "/stat")
		Expect(reaped).To(BeTrue(), "the orphan was not reaped: %s", stat)
	})

	// A child that never reads its stdin must not stall the frame loop: a
	// signal still reaches the group, and a host close still kills it.
	It("does not let blocked stdin stall signals", func() {
		dial := agent(&Server{})
		c := dial()
		var out lockedBuffer
		p, err := Exec(context.Background(), c, sh(`echo $$; exec sleep 60`), strings.NewReader(strings.Repeat("x", 1<<20)), &out, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(waitUntil(5*time.Second, func() bool { return strings.HasSuffix(out.String(), "\n") })).To(BeTrue(), "no pid")
		pgid, err := strconv.Atoi(strings.TrimSpace(out.String()))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
		// SIGWINCH is ignored by default: it reaches the group without ending it.
		sent := make(chan error, 1)
		go func() { sent <- p.Signal(syscall.SIGWINCH) }()
		select {
		case err := <-sent:
			Expect(err).ToNot(HaveOccurred())
		case <-time.After(5 * time.Second):
			Fail("signal blocked behind unread stdin")
		}
		Expect(groupAlive(pgid)).To(BeTrue(), "the signal ended the group")
		_ = c.Close()
		Expect(waitUntil(5*time.Second, func() bool { return !groupAlive(pgid) })).To(BeTrue(), "host close did not kill the group")
	})

	// Pipes and terminals, normal exits and host closes, must give back every
	// descriptor they opened.
	It("leaks no file descriptors", func() {
		dial := agent(&Server{})
		run(dial, sh("true"), nil)
		// Automatic GC would run the finalizers of leaked files and close them,
		// which would hide the leak. It is restored before the check's runtime.GC.
		oldGC := debug.SetGCPercent(-1)
		before := openFDs()
		for i := 0; i < 25; i++ {
			run(dial, sh("echo hi"), strings.NewReader("in"))
		}
		for i := 0; i < 25; i++ {
			run(dial, ExecRequest{Args: []string{"sh", "-c", "echo hi"}, TTY: &TTY{Rows: 24, Cols: 80}}, nil)
		}
		for i := 0; i < 10; i++ {
			c, pgid := pgidOf(dial)
			_ = c.Close()
			Expect(waitUntil(5*time.Second, func() bool { return !groupAlive(pgid) })).To(BeTrue(), "host close did not kill the group")
		}
		const margin = 4
		ok := waitUntil(10*time.Second, func() bool { return openFDs() <= before+margin })
		after := openFDs()
		debug.SetGCPercent(oldGC)
		runtime.GC()
		Expect(ok).To(BeTrue(), "open descriptors: %d before, %d after 60 execs", before, after)
	})

	// A terminal without a stdin request must not get an end-of-file: cat stays
	// up until it is signalled.
	It("sends no EOF to a terminal without stdin", func() {
		dial := agent(&Server{})
		var out lockedBuffer
		p, err := Exec(context.Background(), dial(), ExecRequest{Args: []string{"sh", "-c", "echo ready; exec cat"}, TTY: &TTY{Rows: 24, Cols: 80}}, nil, &out, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(waitUntil(5*time.Second, func() bool { return strings.Contains(out.String(), "ready") })).To(BeTrue(), "output = %q", out.String())
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
			Fail(fmt.Sprintf("the process ended without input: %+v", r))
		case <-time.After(500 * time.Millisecond):
		}
		Expect(p.Signal(syscall.SIGTERM)).To(Succeed())
		select {
		case r := <-res:
			Expect(r.err).ToNot(HaveOccurred())
			Expect(r.code).To(Equal(128 + 15))
		case <-time.After(5 * time.Second):
			Fail("no exit after SIGTERM")
		}
	})

	// A child that left the process group with setsid and keeps stdout open
	// must not hold the exit frame.
	It("does not let an escaped child hold the exit", func() {
		if _, err := exec.LookPath("setsid"); err != nil {
			Skip("no setsid")
		}
		dial := agent(&Server{})
		pidFile := filepath.Join(GinkgoT().TempDir(), "pid")
		DeferCleanup(func() {
			if b, err := os.ReadFile(pidFile); err == nil {
				if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
					_ = syscall.Kill(n, syscall.SIGKILL)
				}
			}
		})
		start := time.Now()
		code, out, _ := run(dial, sh(`setsid sh -c 'echo $$ > `+pidFile+`; exec sleep 30' & echo started`), nil)
		Expect(code).To(Equal(0))
		Expect(out).To(Equal("started\n"))
		Expect(time.Since(start)).To(BeNumerically("<=", 5*time.Second))
	})

	// A reader that is slower than the host writes must get all its input.
	It("gives all of a large stdin to sha256sum", func() {
		dial := agent(&Server{})
		data, want := pseudoRandom(16 << 20)
		code, out, _ := run(dial, ExecRequest{Args: []string{"sha256sum"}}, bytes.NewReader(data))
		Expect(code).To(Equal(0))
		Expect(out).To(Equal(want + "  -\n"))
	})

	It("writes a large stdin to a file", func() {
		dial := agent(&Server{})
		data, want := pseudoRandom(16 << 20)
		path := filepath.Join(GinkgoT().TempDir(), "upload")
		code, _, _ := run(dial, sh("cat > "+path), bytes.NewReader(data))
		Expect(code).To(Equal(0))
		got, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		sum := sha256.Sum256(got)
		Expect(got).To(HaveLen(len(data)))
		Expect(hex.EncodeToString(sum[:])).To(Equal(want))
	})

	// While the host waits for credit, the rest of the connection keeps working:
	// a signal reaches the group, and a host close kills it.
	It("keeps signals working while stdin is blocked", func() {
		dial := agent(&Server{})
		c := dial()
		var out lockedBuffer
		data, _ := pseudoRandom(8 << 20)
		p, err := Exec(context.Background(), c, sh(`echo $$; exec sleep 60`), bytes.NewReader(data), &out, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(waitUntil(5*time.Second, func() bool { return strings.HasSuffix(out.String(), "\n") })).To(BeTrue(), "no pid")
		pgid, err := strconv.Atoi(strings.TrimSpace(out.String()))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
		time.Sleep(200 * time.Millisecond) // let the feed reach the window
		sent := make(chan error, 1)
		go func() { sent <- p.Signal(syscall.SIGWINCH) }()
		select {
		case err := <-sent:
			Expect(err).ToNot(HaveOccurred())
		case <-time.After(5 * time.Second):
			Fail("signal blocked by the stdin feed")
		}
		Expect(groupAlive(pgid)).To(BeTrue(), "the signal ended the group")
		_ = c.Close()
		Expect(waitUntil(5*time.Second, func() bool { return !groupAlive(pgid) })).To(BeTrue(), "host close did not kill the group")
	})

	// A reader that starts late, as one that loads something before it reads
	// stdin does, must still get all of its input.
	It("gives all stdin to a slow reader", func() {
		dial := agent(&Server{})
		data, want := pseudoRandom(16 << 20)
		code, out, _ := run(dial, sh("sleep 0.3; sha256sum"), bytes.NewReader(data))
		Expect(code).To(Equal(0))
		Expect(out).To(Equal(want + "  -\n"))
	})

	// A host whose reads are smaller than the 32 KiB frame must still stay
	// within the byte window: the guest counts bytes, not frames.
	It("stays in the window with small reads", func() {
		dial := agent(&Server{})
		data, want := pseudoRandom(16 << 20)
		for _, size := range []int{1 << 10, 4 << 10, 16 << 10} {
			code, out, _ := run(dial, sh("sleep 0.3; sha256sum"), chunkReader{bytes.NewReader(data), size})
			Expect(code).To(Equal(0), "reads of %d", size)
			Expect(out).To(Equal(want+"  -\n"), "reads of %d", size)
		}
	})
})
