package chv

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// scriptedListener returns the given results from Accept, then net.ErrClosed.
type scriptedListener struct {
	net.Listener
	mu      sync.Mutex
	results []func() (net.Conn, error)
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.results) == 0 {
		return nil, net.ErrClosed
	}
	r := l.results[0]
	l.results = l.results[1:]
	return r()
}

func (l *scriptedListener) Close() error { return nil }

var _ = Describe("servePortListener", func() {
	// It checks that a transient Accept error does not stop the port server:
	// the next connection is still handled.
	It("retries transient Accept errors", func() {
		a, b := net.Pipe()
		defer func() { _ = b.Close() }()
		l := &scriptedListener{results: []func() (net.Conn, error){
			func() (net.Conn, error) { return nil, errors.New("accept: too many open files") },
			func() (net.Conn, error) { return nil, errors.New("accept: connection aborted") },
			func() (net.Conn, error) { return a, nil },
		}}
		handled := make(chan struct{})
		servePortListener(context.Background(), l, func(ctx context.Context, c net.Conn) {
			_ = c.Close()
			close(handled)
		}, make(chan struct{}, 4))
		<-handled
	})

	// It checks that connections over the cap are closed at once, without
	// calling the handler, and that a finished handler frees its slot.
	It("closes connections over the cap without calling the handler", func() {
		const limit = 2
		var peers []net.Conn
		var results []func() (net.Conn, error)
		for range limit + 2 {
			a, b := net.Pipe()
			peers = append(peers, b)
			results = append(results, func() (net.Conn, error) { return a, nil })
			defer func() { _ = b.Close() }()
		}
		l := &scriptedListener{results: results}
		release := make(chan struct{})
		var mu sync.Mutex
		calls := 0
		servePortListener(context.Background(), l, func(ctx context.Context, c net.Conn) {
			mu.Lock()
			calls++
			mu.Unlock()
			<-release
			_ = c.Close()
		}, make(chan struct{}, limit))
		// The two connections over the cap are closed: a read on the peer ends.
		for _, b := range peers[limit:] {
			done := make(chan struct{})
			go func() { _, _ = b.Read(make([]byte, 1)); close(done) }()
			Eventually(done, 2*time.Second).Should(BeClosed(), "a connection over the cap was not closed")
		}
		close(release)
		Eventually(func() int {
			mu.Lock()
			defer mu.Unlock()
			return calls
		}, 2*time.Second, 10*time.Millisecond).Should(Equal(limit), "handler ran the wrong number of times")
		Consistently(func() int {
			mu.Lock()
			defer mu.Unlock()
			return calls
		}, 100*time.Millisecond, 10*time.Millisecond).Should(Equal(limit), "handler ran more than %d times", limit)
	})
})
