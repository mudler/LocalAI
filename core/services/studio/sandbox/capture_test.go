package sandbox_test

import (
	"context"
	"errors"
	"strings"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
)

// echoSandbox runs nothing: Exec writes out to stdout in chunks of chunk
// bytes and stops writing when the context of Exec ends.
type echoSandbox struct {
	sandbox.Sandbox
	out   string
	chunk int
	code  int
	ctxOK bool // the Exec context was still live after the writes
}

type doneProc struct {
	code int
	err  error
}

func (p doneProc) Wait() (int, error)           { return p.code, p.err }
func (doneProc) Signal(syscall.Signal) error    { return nil }
func (doneProc) Resize(rows, cols uint16) error { return errors.New("no tty") }

func (s *echoSandbox) Exec(ctx context.Context, c sandbox.Cmd) (sandbox.Process, error) {
	for rest := s.out; rest != "" && ctx.Err() == nil; {
		n := min(s.chunk, len(rest))
		_, _ = c.Stdout.Write([]byte(rest[:n]))
		rest = rest[n:]
	}
	s.ctxOK = ctx.Err() == nil
	if ctx.Err() != nil {
		return doneProc{code: 137, err: ctx.Err()}, nil
	}
	return doneProc{code: s.code}, nil
}

var _ = Describe("CapWriter", func() {
	It("keeps the output under the cap as it is", func() {
		w := &sandbox.CapWriter{Max: 10}
		_, _ = w.Write([]byte("hello"))
		Expect(w.Cut()).To(BeFalse())
		Expect(w.String()).To(Equal("hello"))
	})

	It("cuts at the cap, counts the rest and calls Full once", func() {
		calls := 0
		w := &sandbox.CapWriter{Max: 4, Full: func() { calls++ }}
		n, err := w.Write([]byte("abcdef"))
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(6))
		_, _ = w.Write([]byte("gh"))
		Expect(calls).To(Equal(1))
		Expect(w.Cut()).To(BeTrue())
		Expect(w.String()).To(Equal("abcd\n[output cut: 4 more bytes]"))
	})

	It("replaces invalid UTF-8", func() {
		w := &sandbox.CapWriter{Max: 10}
		_, _ = w.Write([]byte{'a', 0xff, 'b'})
		Expect(w.String()).To(Equal("a�b"))
	})
})

var _ = Describe("RunCapped", func() {
	It("returns the output and the exit code", func() {
		sb := &echoSandbox{out: "done", chunk: 2, code: 3}
		out, code, err := sandbox.RunCapped(context.Background(), sb, []string{"x"}, nil, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(3))
		Expect(out).To(Equal("done"))
	})

	It("stops the process at the cap and reports no error", func() {
		sb := &echoSandbox{out: strings.Repeat("x", 50), chunk: 4}
		out, code, err := sandbox.RunCapped(context.Background(), sb, []string{"x"}, nil, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(0))
		Expect(sb.ctxOK).To(BeFalse(), "the process was not stopped")
		Expect(out).To(HavePrefix(strings.Repeat("x", 10) + "\n[output cut: "))
	})

	It("passes on the error of a stopped caller", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		sb := &echoSandbox{out: "never", chunk: 1}
		_, _, err := sandbox.RunCapped(ctx, sb, []string{"x"}, nil, 10)
		Expect(err).To(MatchError(context.Canceled))
	})
})
