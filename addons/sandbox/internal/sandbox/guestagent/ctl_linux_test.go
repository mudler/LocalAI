package guestagent

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCtlDirListenAndDial(t *testing.T) {
	dir := t.TempDir()
	c, err := OpenCtlDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.Path("agent.sock"), "/proc/self/fd/") || !strings.HasSuffix(c.Path("agent.sock"), "/agent.sock") {
		t.Fatalf("path = %q", c.Path("agent.sock"))
	}
	l, err := c.Listen("agent.sock")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent.sock")); err != nil {
		t.Fatalf("the socket is not in the folder: %v", err)
	}
	go func() {
		conn, err := l.Accept()
		if err == nil {
			conn.Close()
		}
	}()
	conn, err := c.Dial("agent.sock")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	l.Close()
	if _, err := os.Stat(filepath.Join(dir, "agent.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("closing the listener left the socket file: %v", err)
	}
	// A stale file from a sandbox that was killed does not block a new listener.
	if err := os.WriteFile(filepath.Join(dir, "agent.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	l2, err := c.Listen("agent.sock")
	if err != nil {
		t.Fatalf("listen over a stale file: %v", err)
	}
	l2.Close()
	if _, err := OpenCtlDir(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("OpenCtlDir accepted a missing folder")
	}
}

func TestSwitchListenerStopsAndStartsAgain(t *testing.T) {
	c, err := OpenCtlDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := 0
	srv := &Server{}
	sl := &SwitchListener{Listen: func() (net.Listener, error) { return c.Listen("agent.sock") }}
	srv.Checkpoint = func() error { checkpoints++; return sl.Stop() }
	if err := sl.Start(srv); err != nil {
		t.Fatal(err)
	}
	if err := sl.Start(srv); err != nil { // already listening: no second listener
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	call := func() error {
		conn, err := c.Dial("agent.sock")
		if err != nil {
			return err
		}
		return Call(ctx, conn, TypeHealth)
	}
	if err := call(); err != nil {
		t.Fatal(err)
	}
	conn, err := c.Dial("agent.sock")
	if err != nil {
		t.Fatal(err)
	}
	if err := Call(ctx, conn, TypeCheckpoint); err != nil || checkpoints != 1 {
		t.Fatalf("checkpoint request: %v, hook calls %d", err, checkpoints)
	}
	if _, err := c.Dial("agent.sock"); err == nil {
		t.Fatal("the agent still listens after the checkpoint request")
	}
	if err := sl.Stop(); err != nil { // stop twice is fine
		t.Fatal(err)
	}
	if err := sl.Start(srv); err != nil {
		t.Fatal(err)
	}
	if err := call(); err != nil {
		t.Fatalf("after listening again: %v", err)
	}
	sl.Stop()
}

func TestCheckpointFrameAnswersOKWithoutAHook(t *testing.T) {
	dial := agent(t, &Server{})
	if err := Call(context.Background(), dial(), TypeCheckpoint); err != nil {
		t.Fatal(err)
	}
}

func TestLoopbackFallback(t *testing.T) {
	boom := errors.New("ENOTTY")
	if err := loopbackFallback(boom, func() bool { return true }); err != nil {
		t.Fatalf("lo is up, the ioctl error must not matter: %v", err)
	}
	if err := loopbackFallback(boom, func() bool { return false }); !errors.Is(err, boom) {
		t.Fatalf("lo is down: %v", err)
	}
	if err := loopbackFallback(nil, func() bool { t.Fatal("asked without an error"); return false }); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownFrameTypeAnswersAnError(t *testing.T) {
	dial := agent(t, &Server{})
	if err := Call(context.Background(), dial(), "no-such-frame"); err == nil || !strings.Contains(err.Error(), "unknown frame type") {
		t.Fatalf("unknown frame type: %v", err)
	}
	if err := Call(context.Background(), dial(), TypeCheckpoint); err != nil {
		t.Fatalf("checkpoint is not handled: %v", err)
	}
}
