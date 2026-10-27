package main

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

type fakeCtl struct {
	events  []string
	hideErr error
}

func (f *fakeCtl) Hide() error { f.events = append(f.events, "hide"); return f.hideErr }
func (f *fakeCtl) Listen(name string) (net.Listener, error) {
	f.events = append(f.events, "listen "+name)
	return net.Listen("tcp", "127.0.0.1:0")
}
func (f *fakeCtl) Dial(name string) (net.Conn, error) {
	f.events = append(f.events, "dial "+name)
	return nil, errors.New("no dial in this test")
}

// The agent socket must exist only after the control folder is hidden.
func TestServeCtlHidesBeforeItListens(t *testing.T) {
	f := &fakeCtl{}
	sl, _, err := serveCtl(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sl.Stop() }()
	if got := strings.Join(f.events, "|"); got != "hide|listen agent.sock" {
		t.Fatalf("events = %s", got)
	}
}

func TestServeCtlDoesNotServeWhenHideFails(t *testing.T) {
	f := &fakeCtl{hideErr: errors.New("EPERM")}
	if _, _, err := serveCtl(f, nil); err == nil || !strings.Contains(err.Error(), "EPERM") {
		t.Fatalf("err = %v", err)
	}
	if got := strings.Join(f.events, "|"); got != "hide" {
		t.Fatalf("events = %s: the agent listened after a failed Hide", got)
	}
}

func TestRetryTriesAgainUntilItWorks(t *testing.T) {
	calls := 0
	err := retry(5, time.Millisecond, func() error {
		if calls++; calls < 3 {
			return errors.New("busy")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err = %v after %d calls", err, calls)
	}
	calls = 0
	err = retry(4, time.Millisecond, func() error { calls++; return errors.New("still busy") })
	if err == nil || err.Error() != "still busy" || calls != 4 {
		t.Fatalf("err = %v after %d calls", err, calls)
	}
}
