//go:build auth

package studio

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type seekFails struct{ *bytes.Reader }

func (seekFails) Seek(int64, int) (int64, error) { return 0, errors.New("seek failed") }

func TestSniffTypeReadsAShortFileAndRewinds(t *testing.T) {
	r := bytes.NewReader([]byte("hello"))
	ct, err := sniffType(r)
	if err != nil || ct != "text/plain" {
		t.Fatalf("sniffType = %q, %v", ct, err)
	}
	if rest, _ := io.ReadAll(r); string(rest) != "hello" {
		t.Fatalf("the reader was not rewound: %q", rest)
	}
}

func TestSniffTypeFailsWhenTheReaderCannotRewind(t *testing.T) {
	// A reader that stays at the end would be served from the wrong offset
	// with status 200, so the caller must see the failure.
	if _, err := sniffType(seekFails{bytes.NewReader([]byte(strings.Repeat("x", 600)))}); err == nil {
		t.Fatal("a failed rewind was ignored")
	}
}

type readFails struct{}

func (readFails) Read([]byte) (int, error)       { return 0, errors.New("disk error") }
func (readFails) Seek(int64, int) (int64, error) { return 0, nil }

func TestSniffTypeFailsWhenTheFileCannotBeRead(t *testing.T) {
	if _, err := sniffType(readFails{}); err == nil {
		t.Fatal("a failed read was ignored")
	}
}
