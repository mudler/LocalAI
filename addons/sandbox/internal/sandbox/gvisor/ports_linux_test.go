//go:build linux

package gvisor

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

func TestServePortsCapsConnectionsAndCloseConnsClosesThem(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "ctl"), 0o700); err != nil {
		t.Fatal(err)
	}
	held := make(chan net.Conn, maxPortConns+4)
	ps, err := servePorts(dir, map[int]sandbox.PortHandler{7000: func(ctx context.Context, c net.Conn) {
		held <- c
		// Returns when the host closes the connection.
		_, _ = io.Copy(io.Discard, c)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer ps.close()
	var guest []net.Conn
	for range maxPortConns + 2 {
		c, err := net.Dial("unix", portSock(dir, 7000))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		guest = append(guest, c)
	}
	// Wait until the host closed the two connections over the cap.
	deadline := time.Now().Add(10 * time.Second)
	closed := 0
	for closed != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("connections closed by the host = %d, want 2", closed)
		}
		closed = 0
		for _, c := range guest {
			_ = c.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
			if _, err := c.Read(make([]byte, 1)); err != nil && !isTimeout(err) {
				closed++
			}
		}
	}
	for len(held) < maxPortConns && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if len(held) != maxPortConns {
		t.Fatalf("handlers running = %d, want the cap %d", len(held), maxPortConns)
	}
	ps.closeConns()
	for _, c := range guest {
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := c.Read(make([]byte, 1)); err == nil || isTimeout(err) {
			t.Fatalf("a connection survived closeConns: %v", err)
		}
	}
	for len(held) > 0 {
		<-held
	}
	// New connections are served after closeConns: the handlers returned, so
	// slots free up. A slot may free a moment after the connection closed,
	// so try until a handler gets one.
	for {
		if time.Now().After(deadline.Add(10 * time.Second)) {
			t.Fatal("no connection was served after closeConns")
		}
		c, err := net.Dial("unix", portSock(dir, 7000))
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		select {
		case <-held:
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func TestConnSetRefusesConnectionsAfterShutdown(t *testing.T) {
	var set connSet
	a, b := net.Pipe()
	defer b.Close()
	tc := set.add(a)
	if tc == nil {
		t.Fatal("add refused before shutdown")
	}
	set.shutdown()
	if _, err := b.Read(make([]byte, 1)); err == nil {
		t.Fatal("a tracked connection survived shutdown")
	}
	c, d := net.Pipe()
	defer d.Close()
	if set.add(c) != nil {
		t.Fatal("add accepted a connection after shutdown")
	}
	if _, err := d.Read(make([]byte, 1)); err == nil {
		t.Fatal("the late connection was not closed")
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

func TestSamePorts(t *testing.T) {
	h := func(context.Context, net.Conn) {}
	if !samePorts([]int{3128, 7000}, map[int]sandbox.PortHandler{7000: h, 3128: h}) || samePorts([]int{3128}, map[int]sandbox.PortHandler{7000: h}) {
		t.Fatal("samePorts")
	}
}
