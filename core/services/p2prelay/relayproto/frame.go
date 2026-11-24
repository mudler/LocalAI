package relayproto

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// MaxMessageSize is the largest JSON body of one message.
const MaxMessageSize = 1 << 20

// CallTimeout bounds one Call, connection included, when the context has no
// earlier deadline.
const CallTimeout = 20 * time.Second

// ErrTooLarge is returned by Read and Write for a message over MaxMessageSize.
var ErrTooLarge = errors.New("relayproto: message too large")

// Write sends v as one framed message.
func Write(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("relayproto: encode: %w", err)
	}
	if len(b) > MaxMessageSize {
		return ErrTooLarge
	}
	msg := binary.AppendUvarint(make([]byte, 0, len(b)+binary.MaxVarintLen64), uint64(len(b)))
	msg = append(msg, b...)
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("relayproto: write: %w", err)
	}
	return nil
}

// Read reads one framed message into v. It reads no byte past the message,
// so a stream can carry more data after it.
func Read(r io.Reader, v any) error {
	br, ok := r.(io.ByteReader)
	if !ok {
		br = &byteReader{r: r}
	}
	n, err := binary.ReadUvarint(br)
	if err != nil {
		return fmt.Errorf("relayproto: read length: %w", err)
	}
	if n > MaxMessageSize {
		return ErrTooLarge
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return fmt.Errorf("relayproto: read body: %w", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("relayproto: decode: %w", err)
	}
	return nil
}

// byteReader reads the varint one byte at a time, so that Read does not
// buffer bytes that belong to the caller (bufio would).
type byteReader struct {
	r   io.Reader
	buf [1]byte
}

func (b *byteReader) ReadByte() (byte, error) {
	if _, err := io.ReadFull(b.r, b.buf[:]); err != nil {
		return 0, err
	}
	return b.buf[0], nil
}

// Call opens one admin stream to relay, sends req and returns the response.
// It sets req.V. The host must already know an address of the relay or be
// connected to it. A response with OK false is returned together with an
// error that carries its Err text.
func Call(ctx context.Context, h host.Host, relay peer.ID, req Request) (Response, error) {
	var resp Response
	req.V = Version
	if err := roundTrip(ctx, h, relay, AdminID, req, &resp); err != nil {
		return resp, err
	}
	if resp.V != Version {
		return resp, fmt.Errorf("relayproto: unsupported response version %d", resp.V)
	}
	if !resp.OK {
		return resp, fmt.Errorf("relay %s: %s", relay.ShortString(), resp.Err)
	}
	return resp, nil
}

// UsePass presents a relay pass to relay. On success the relay admits the
// host as a pending member and returns how long that lasts. Every refusal
// carries the same error, whatever the reason.
func UsePass(ctx context.Context, h host.Host, relay peer.ID, code string) (time.Duration, error) {
	var resp PassResponse
	if err := roundTrip(ctx, h, relay, PassID, PassRequest{V: Version, Code: code}, &resp); err != nil {
		return 0, err
	}
	if !resp.OK {
		return 0, fmt.Errorf("relay %s: %s", relay.ShortString(), resp.Err)
	}
	return time.Duration(resp.TTLSeconds) * time.Second, nil
}

func roundTrip(ctx context.Context, h host.Host, relay peer.ID, proto protocol.ID, req, resp any) error {
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	s, err := h.NewStream(ctx, relay, proto)
	if err != nil {
		return fmt.Errorf("relayproto: open stream to %s: %w", relay.ShortString(), err)
	}
	defer func() { _ = s.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = s.SetDeadline(dl)
	}
	if err := Write(s, req); err != nil {
		_ = s.Reset()
		return err
	}
	if err := s.CloseWrite(); err != nil {
		_ = s.Reset()
		return fmt.Errorf("relayproto: close write: %w", err)
	}
	if err := Read(s, resp); err != nil {
		_ = s.Reset()
		return err
	}
	return nil
}
