package guestagent

import (
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
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

func TestRetryListenerSurvivesTransientErrors(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	var slept []time.Duration
	rl := RetryListener{
		Listener: &scriptedListener{errs: []error{syscall.EMFILE, syscall.ECONNABORTED, syscall.ENOMEM}, conn: a},
		Sleep:    func(d time.Duration) { slept = append(slept, d) },
	}
	c, err := rl.Accept()
	if err != nil || c != a {
		t.Fatalf("accept = %v, %v; want the connection after the errors", c, err)
	}
	if len(slept) != 3 || slept[0] != retryMin || slept[1] != 2*retryMin || slept[2] != 4*retryMin {
		t.Fatalf("backoff = %v", slept)
	}
}

func TestRetryListenerBackoffIsCapped(t *testing.T) {
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
		if d > retryMax {
			t.Fatalf("backoff %v above the cap %v", d, retryMax)
		}
	}
}

func TestRetryListenerReturnsOnClose(t *testing.T) {
	for _, closed := range []error{net.ErrClosed, os.ErrClosed} {
		l := &scriptedListener{errs: []error{syscall.EMFILE, closed}}
		var slept int
		rl := RetryListener{Listener: l, Sleep: func(time.Duration) { slept++ }}
		_, err := rl.Accept()
		if !errors.Is(err, closed) {
			t.Fatalf("accept after close = %v", err)
		}
		if slept != 1 || l.n != 2 {
			t.Fatalf("closed listener: %d sleeps, %d accepts", slept, l.n)
		}
	}
}

func TestLogPanicRecovers(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer logPanic("test")
		panic("boom") // without the recover the test binary exits
	}()
	<-done
}
