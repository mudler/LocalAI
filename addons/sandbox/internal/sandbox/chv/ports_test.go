package chv

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
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

// TestServePortListenerRetriesTransientErrors checks that a transient Accept
// error does not stop the port server: the next connection is still handled.
func TestServePortListenerRetriesTransientErrors(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	l := &scriptedListener{results: []func() (net.Conn, error){
		func() (net.Conn, error) { return nil, errors.New("accept: too many open files") },
		func() (net.Conn, error) { return nil, errors.New("accept: connection aborted") },
		func() (net.Conn, error) { return a, nil },
	}}
	handled := make(chan struct{})
	servePortListener(context.Background(), l, func(ctx context.Context, c net.Conn) {
		c.Close()
		close(handled)
	}, make(chan struct{}, 4))
	<-handled
}

// TestServePortListenerCapsConnections checks that connections over the cap
// are closed at once, without calling the handler, and that a finished
// handler frees its slot.
func TestServePortListenerCapsConnections(t *testing.T) {
	const limit = 2
	var peers []net.Conn
	var results []func() (net.Conn, error)
	for range limit + 2 {
		a, b := net.Pipe()
		peers = append(peers, b)
		results = append(results, func() (net.Conn, error) { return a, nil })
		defer b.Close()
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
		c.Close()
	}, make(chan struct{}, limit))
	// The two connections over the cap are closed: a read on the peer ends.
	for _, b := range peers[limit:] {
		done := make(chan struct{})
		go func() { b.Read(make([]byte, 1)); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("a connection over the cap was not closed")
		}
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := calls
		mu.Unlock()
		if n == limit {
			break
		}
		if n > limit || time.Now().After(deadline) {
			t.Fatalf("handler ran %d times, want %d", n, limit)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
