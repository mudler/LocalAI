package guestagent

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Version is the protocol version. The host refuses a guest agent with a
// different major version.
const Version = "1.0"

// MaxFrame is the largest frame body in bytes.
const MaxFrame = 1 << 20

// StdinWindow is how many stdin bytes the host may send before the guest
// acknowledges them with credit frames. A host that waits for credit keeps
// the guest's stdin queue bounded.
const StdinWindow = 2 << 20

// AgentPort is the guest vsock port of the agent and the host vsock port
// that receives the ready frame. HostCID is the vsock address of the host.
const (
	AgentPort = 1024
	HostCID   = 2
)

// Frame types.
const (
	TypeReady    = "ready"
	TypeExec     = "exec"
	TypeStdin    = "stdin"
	TypeSignal   = "signal"
	TypeResize   = "resize"
	TypeStdout   = "stdout"
	TypeStderr   = "stderr"
	TypeExit     = "exit"
	TypeFreeze   = "freeze"
	TypeThaw     = "thaw"
	TypeHealth   = "health"
	TypeShutdown = "shutdown"
	TypeDetach   = "detach"
	TypeCredit   = "credit"
	TypeOK       = "ok"
	TypeError    = "error"
)

// TypeCheckpoint asks the guest to get ready for a checkpoint of the whole
// sandbox: the host sends it before gVisor saves the state (Server.Checkpoint).
const TypeCheckpoint = "checkpoint"

// ErrFrameTooLarge means a frame body is larger than MaxFrame.
var ErrFrameTooLarge = errors.New("guestagent: frame too large")

// TTY is a terminal size.
type TTY struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

// Frame is one protocol message. Only the fields of its type are set.
type Frame struct {
	Type    string   `json:"type"`
	Version string   `json:"version,omitempty"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
	Dir     string   `json:"dir,omitempty"`
	TTY     *TTY     `json:"tty,omitempty"`
	Data    []byte   `json:"data,omitempty"` // base64 in JSON
	Signal  int      `json:"signal,omitempty"`
	Rows    uint16   `json:"rows,omitempty"`
	Cols    uint16   `json:"cols,omitempty"`
	N       int      `json:"n,omitempty"` // credit: stdin bytes written
	Code    int      `json:"code,omitempty"`
	Message string   `json:"message,omitempty"`
}

// WriteFrame writes f as one length-prefixed frame.
func WriteFrame(w io.Writer, f Frame) error {
	body, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(body) > MaxFrame {
		return ErrFrameTooLarge
	}
	buf := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(buf, uint32(len(body))) // #nosec G115 -- the body is at most MaxFrame bytes, checked above
	copy(buf[4:], body)
	_, err = w.Write(buf)
	return err
}

// ReadFrame reads one frame. It returns ErrFrameTooLarge without reading the
// body when the length is over MaxFrame, and io.EOF at a clean end of the
// stream.
func ReadFrame(r io.Reader) (Frame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxFrame {
		return Frame{}, ErrFrameTooLarge
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return Frame{}, err
	}
	var f Frame
	if err := json.Unmarshal(body, &f); err != nil {
		return Frame{}, fmt.Errorf("guestagent: bad frame: %w", err)
	}
	if f.Type == "" {
		return Frame{}, errors.New("guestagent: frame without a type")
	}
	return f, nil
}

// SameMajor reports whether two versions have the same major number.
func SameMajor(a, b string) bool {
	ma, _, _ := strings.Cut(a, ".")
	mb, _, _ := strings.Cut(b, ".")
	return ma != "" && ma == mb
}
