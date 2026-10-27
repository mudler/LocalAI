package guestagent

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	in := []Frame{
		{Type: TypeExec, Args: []string{"sh", "-c", "echo hi"}, Env: []string{"A=1"}, Dir: "/workspace", TTY: &TTY{Rows: 24, Cols: 80}},
		{Type: TypeStdin, Data: []byte{0, 1, 2, 255}},
		{Type: TypeExit, Code: 3},
		{Type: TypeExit, Signal: 15},
		{Type: TypeReady, Version: Version},
	}
	var buf bytes.Buffer
	for _, f := range in {
		if err := WriteFrame(&buf, f); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range in {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
	if _, err := ReadFrame(&buf); !errors.Is(err, io.EOF) {
		t.Fatalf("end: %v", err)
	}
}

func TestFrameLimits(t *testing.T) {
	if err := WriteFrame(io.Discard, Frame{Type: TypeStdout, Data: make([]byte, MaxFrame)}); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("write: %v", err)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], MaxFrame+1)
	if _, err := ReadFrame(bytes.NewReader(hdr[:])); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("read: %v", err)
	}
	for _, body := range []string{"{", `{"data":"x"}`, "[]"} {
		var buf bytes.Buffer
		binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
		buf.Write(hdr[:])
		buf.WriteString(body)
		if _, err := ReadFrame(&buf); err == nil {
			t.Fatalf("%q was accepted", body)
		}
	}
	short := append(binary.BigEndian.AppendUint32(nil, 10), []byte(`{"ty`)...)
	if _, err := ReadFrame(bytes.NewReader(short)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short body: %v", err)
	}
}

func TestSameMajor(t *testing.T) {
	for _, c := range []struct {
		a, b string
		ok   bool
	}{{"1.0", "1.3", true}, {"1.0", "2.0", false}, {"", "1.0", false}, {"1", "1.0", true}} {
		if SameMajor(c.a, c.b) != c.ok {
			t.Fatalf("SameMajor(%q, %q) != %v", c.a, c.b, c.ok)
		}
	}
	if !strings.HasPrefix(Version, "1.") {
		t.Fatal("update the tests for a new major version")
	}
}

func TestMergeEnv(t *testing.T) {
	got := mergeEnv([]string{"PATH=/bin", "HOME=/home/agent", "A=1"}, []string{"A=2", "B=3", "PATH=/opt/bin"})
	want := []string{"PATH=/opt/bin", "HOME=/home/agent", "A=2", "B=3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

// The host caps a credit at the bytes outstanding and ignores a negative one,
// so a buggy guest cannot drive the window out of range.
func TestCreditClamp(t *testing.T) {
	p := &Process{outstanding: 100}
	p.fcond = sync.NewCond(&p.fmu)
	for _, c := range []struct{ n, want int }{
		{-5, 100}, {0, 100}, {30, 70}, {500, 0}, {-1 << 30, 0}, {1 << 30, 0},
	} {
		p.credit(c.n)
		if p.outstanding != c.want {
			t.Fatalf("credit(%d): outstanding %d, want %d", c.n, p.outstanding, c.want)
		}
	}
}
