package guestagent

import (
	"net"
	"os"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// scriptedListener returns the scripted Accept errors, then conn, then the
// closed-listener error.
type scriptedListener struct {
	net.Listener
	errs []error
	conn net.Conn
	n    int
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	l.n++
	if len(l.errs) > 0 {
		err := l.errs[0]
		l.errs = l.errs[1:]
		return nil, err
	}
	if l.conn != nil {
		c := l.conn
		l.conn = nil
		return c, nil
	}
	return nil, net.ErrClosed
}

var _ = Describe("RetryListener", func() {
	It("survives transient errors", func() {
		a, b := net.Pipe()
		defer func() { _ = b.Close() }()
		var slept []time.Duration
		rl := RetryListener{
			Listener: &scriptedListener{errs: []error{syscall.EMFILE, syscall.ECONNABORTED, syscall.ENOMEM}, conn: a},
			Sleep:    func(d time.Duration) { slept = append(slept, d) },
		}
		c, err := rl.Accept()
		Expect(err).ToNot(HaveOccurred())
		Expect(c).To(BeIdenticalTo(a), "want the connection after the errors")
		Expect(slept).To(Equal([]time.Duration{retryMin, 2 * retryMin, 4 * retryMin}))
	})

	It("caps the backoff", func() {
		var errs []error
		for range 20 {
			errs = append(errs, syscall.EMFILE)
		}
		var slept []time.Duration
		rl := RetryListener{
			Listener: &scriptedListener{errs: errs},
			Sleep:    func(d time.Duration) { slept = append(slept, d) },
		}
		_, _ = rl.Accept()
		for _, d := range slept {
			Expect(d).To(BeNumerically("<=", retryMax), "backoff above the cap")
		}
	})

	It("returns on close", func() {
		for _, closed := range []error{net.ErrClosed, os.ErrClosed} {
			l := &scriptedListener{errs: []error{syscall.EMFILE, closed}}
			var slept int
			rl := RetryListener{Listener: l, Sleep: func(time.Duration) { slept++ }}
			_, err := rl.Accept()
			Expect(err).To(MatchError(closed))
			Expect(slept).To(Equal(1))
			Expect(l.n).To(Equal(2))
		}
	})
})

var _ = Describe("logPanic", func() {
	It("recovers", func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer logPanic("test")
			panic("boom") // without the recover the test binary exits
		}()
		<-done
	})
})
