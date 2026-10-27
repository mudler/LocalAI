package guestagent

import (
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

// unixListener returns a listening unix socket wrapped the way ListenVsock
// wraps a vsock socket, so the accept path runs on a real kernel socket.
func unixListener(t *testing.T) (*vsockListener, string) {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/s"
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Listen(fd, 8); err != nil {
		t.Fatal(err)
	}
	// A unix socket has no CID, so the test accepts every peer.
	return &vsockListener{f: os.NewFile(uintptr(fd), "test-listener"), port: 1, allow: func(sockaddrVM) bool { return true }}, path
}

func TestVsockAcceptAfterCloseIsErrClosed(t *testing.T) {
	l, _ := unixListener(t)
	errc := make(chan error, 1)
	go func() {
		_, err := l.Accept() // blocks: nobody dials
		errc <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept after Close = %v, want net.ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Accept did not return after Close")
	}
	if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept on a closed listener = %v, want net.ErrClosed", err)
	}
}

func TestVsockAcceptStillAccepts(t *testing.T) {
	l, path := unixListener(t)
	defer func() { _ = l.Close() }()
	go func() {
		c, err := net.Dial("unix", path)
		if err == nil {
			c.Close()
		}
	}()
	c, err := l.Accept()
	if err != nil {
		t.Fatalf("Accept = %v", err)
	}
	c.Close()
}

func TestPeerAllowedOnlyTheHost(t *testing.T) {
	for cid, want := range map[uint32]bool{HostCID: true, 0: false, 1: false, 3: false, 4: false, cidAny: false} {
		if got := peerAllowed(cid); got != want {
			t.Errorf("peerAllowed(%d) = %v, want %v", cid, got, want)
		}
	}
}

// TestVsockAcceptDropsOtherPeers checks that Accept closes a connection from
// a peer the check refuses and returns the next one it accepts.
func TestVsockAcceptDropsOtherPeers(t *testing.T) {
	l, path := unixListener(t)
	defer l.Close()
	var seen int
	l.allow = func(sockaddrVM) bool { seen++; return seen > 1 }
	first, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	c, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if seen != 2 {
		t.Fatalf("the check ran %d times, want 2", seen)
	}
	// The refused connection is closed by the listener side.
	_ = first.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := first.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("the refused connection was not closed: %v", err)
	}
}
