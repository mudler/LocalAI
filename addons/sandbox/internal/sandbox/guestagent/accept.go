package guestagent

import (
	"errors"
	"log"
	"net"
	"os"
	"time"
)

// Backoff of RetryListener: it starts at retryMin, doubles after each error
// and stops at retryMax.
const (
	retryMin = 10 * time.Millisecond
	retryMax = time.Second
)

// RetryListener keeps a long-running accept loop alive. Serve and Forward
// return on the first Accept error, and a transient one (EMFILE, ENOMEM, a
// connection aborted in the queue) must not stop them. Accept retries every
// error with a backoff and returns only when the listener is closed.
type RetryListener struct {
	net.Listener
	// Sleep waits between retries. Nil means time.Sleep.
	Sleep func(time.Duration)
}

// Accept returns the next connection. It returns only net.ErrClosed or
// os.ErrClosed, which mean that the listener is closed.
func (l RetryListener) Accept() (net.Conn, error) {
	sleep := l.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	delay := time.Duration(0)
	for {
		c, err := l.Listener.Accept()
		if err == nil {
			return c, nil
		}
		if errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) {
			return nil, err
		}
		if delay == 0 {
			delay = retryMin
		} else {
			delay = min(2*delay, retryMax)
		}
		log.Printf("accept: %v; retrying in %v", err, delay)
		sleep(delay)
	}
}

// logPanic recovers a panic in a long-running goroutine and logs it, so one
// bad connection does not stop PID 1. Use it as: defer logPanic("name").
func logPanic(name string) {
	if r := recover(); r != nil {
		log.Printf("%s: recovered panic: %v", name, r)
	}
}
