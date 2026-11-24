package tunnel

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// A worker that reaches its frontend over the peer-to-peer layer opens one
// stream for each lane instead of a websocket. There is no HTTP request on
// such a stream, so what the connect request carries in its query and its
// Authorization header travels in a first frame, the hello, and the answer
// that the status line gives travels in a second frame, the hello reply. The
// yamux session starts on the same stream after the reply.
//
// A frame is a uvarint length and a JSON object of at most HelloMaxBytes. The
// reader takes the frame byte by byte for the length and with one exact read
// for the payload, so it never reads into the session that follows.

// HelloVersion is the version of the hello that this package writes.
const HelloVersion = 1

// HelloMaxBytes bounds the JSON payload of a hello and of a hello reply.
const HelloMaxBytes = 4 << 10

// Hello is the first frame of a tunnel stream over the peer-to-peer layer.
type Hello struct {
	V      int    `json:"v"`
	NodeID string `json:"node_id"`
	Lane   Lane   `json:"lane"`
	// Token is the own tunnel credential of the node, as at the connect
	// endpoint.
	Token string `json:"tunnel_token"`
}

// HelloReply is the answer of the frontend to a hello. Status uses the codes of
// the connect endpoint: 200 accepts the stream, and 401, 403, 409 and 503 mean
// what they mean there, so that the worker handles both doors in one way.
type HelloReply struct {
	Status int    `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Accepted reports whether the reply accepts the stream.
func (r HelloReply) Accepted() bool { return r.Status == http.StatusOK }

// ErrHelloTooLarge is returned for a frame that declares more than
// HelloMaxBytes.
var ErrHelloTooLarge = errors.New("tunnel: the hello frame is too large")

// WriteHello writes h with one Write.
func WriteHello(w io.Writer, h Hello) error { return writeJSONFrame(w, h) }

// ReadHello reads a hello. A hello of another version, or one whose lane this
// package does not know, is an error.
func ReadHello(r io.Reader) (Hello, error) {
	var h Hello
	if err := readJSONFrame(r, &h); err != nil {
		return Hello{}, err
	}
	if h.V != HelloVersion {
		return Hello{}, fmt.Errorf("%w: hello version %d, want %d", ErrProtocol, h.V, HelloVersion)
	}
	if !h.Lane.Valid() {
		return Hello{}, fmt.Errorf("%w: unknown tunnel lane %q", ErrProtocol, h.Lane)
	}
	return h, nil
}

// WriteHelloReply writes r with one Write.
func WriteHelloReply(w io.Writer, r HelloReply) error { return writeJSONFrame(w, r) }

// ReadHelloReply reads the answer to a hello.
func ReadHelloReply(r io.Reader) (HelloReply, error) {
	var out HelloReply
	if err := readJSONFrame(r, &out); err != nil {
		return HelloReply{}, err
	}
	if out.Status == 0 {
		return HelloReply{}, fmt.Errorf("%w: the hello reply has no status", ErrProtocol)
	}
	return out, nil
}

func writeJSONFrame(w io.Writer, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(payload) > HelloMaxBytes {
		return fmt.Errorf("%w: %d bytes", ErrHelloTooLarge, len(payload))
	}
	buf := binary.AppendUvarint(make([]byte, 0, binary.MaxVarintLen64+len(payload)), uint64(len(payload)))
	_, err = w.Write(append(buf, payload...))
	return err
}

func readJSONFrame(r io.Reader, v any) error {
	n, err := binary.ReadUvarint(byteReader{r})
	if err != nil {
		if errors.Is(err, io.EOF) {
			return err
		}
		return fmt.Errorf("%w: reading the length of a hello frame: %v", ErrProtocol, err)
	}
	if n > HelloMaxBytes {
		return fmt.Errorf("%w: %d bytes declared", ErrHelloTooLarge, n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return err
	}
	if err := json.Unmarshal(payload, v); err != nil {
		return fmt.Errorf("%w: decoding a hello frame: %v", ErrProtocol, err)
	}
	return nil
}

// byteReader reads one byte at a time, with no buffer, so that nothing past
// the length is taken from the stream.
type byteReader struct{ r io.Reader }

func (b byteReader) ReadByte() (byte, error) {
	var one [1]byte
	if _, err := io.ReadFull(b.r, one[:]); err != nil {
		return 0, err
	}
	return one[0], nil
}
