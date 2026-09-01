// SPDX-License-Identifier: MIT

package cluster

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// The framing every stream on a worker tunnel opens with.
//
// A yamux stream on its own carries no destination: the frontend opens one and
// the worker has to be told what it is for. So the first thing on every stream
// is a request frame naming a TAG (which local service) and a TARGET (which
// instance of it), and the worker answers with a reply frame before either side
// speaks the tunnelled protocol.
//
// The reply is not optional and is not sent only on failure, which is the part
// that is easy to get wrong. The protocols carried here are client-speaks-first
// (gRPC sends an HTTP/2 preface, HTTP sends a request line), so a reply sent
// only when the worker refuses would arrive interleaved with a response body on
// the streams that succeeded, and the frontend would have no safe moment to
// look for it. Always sending one costs a round trip per stream, which is paid
// once per pooled connection rather than once per request.
//
// Both frames are length-prefixed rather than newline-delimited so a reader
// consumes exactly the header and not one byte of what follows: the stream is
// handed to gRPC or net/http afterwards, and a buffered reader that over-read
// would eat the beginning of their conversation.

const (
	// StreamTagGRPC routes a stream to a backend process on the worker. Its
	// target is the address that backend listens on, which the worker resolves
	// itself; see the worker's tunnel services for what it will accept.
	StreamTagGRPC = "grpc"

	// StreamTagHTTP routes a stream to the worker's own HTTP server, the one
	// that serves file staging and backend logs. Its target is ignored: there
	// is exactly one such server per worker and only the worker knows where it
	// bound.
	StreamTagHTTP = "http"
)

// maxTunnelFrame bounds a header frame. It is a defence against a peer that
// declares a huge length and never sends it, not a size the protocol needs:
// the longest real frame is a tag plus a host:port, well under a hundred
// bytes. A reader that refuses early cannot be made to allocate on demand.
const maxTunnelFrame = 1024

// The reply codes. They travel on the wire, so they are strings rather than
// integers: a frontend reading a code from a worker it does not recognise can
// at least log something an operator can search for.
const (
	replyAccepted          = "ok"
	replyCodeUnknownTag    = "unknown-tag"
	replyCodeUnavailable   = "unavailable"
	replyCodeBadRequest    = "bad-request"
	replyPrefixRefused     = "err "
	streamRequestSeparator = " "
)

// The three refusals a worker can send, kept apart on purpose.
//
// This is the phase's standing rule in its wire form. An unknown tag is a fact
// about what this worker SERVES and will not change until the worker is
// upgraded; an unavailable target is an infrastructure failure that may well
// succeed on the next attempt; a bad request is this frontend's own bug. A
// caller retries the second, gives up on the first, and reports the third.
// Collapsing them into one error would make a frontend retry a stream that can
// never work, or abandon a backend that was merely restarting.
//
// None of them wraps a node-absence error, and none must ever be built over
// one: a refusal is proof the worker is CONNECTED and answered.
var (
	ErrStreamTagUnknown        = errors.New("cluster: the worker does not serve that stream tag")
	ErrStreamTargetUnavailable = errors.New("cluster: the worker could not reach the local service for that stream")
	ErrStreamRequestInvalid    = errors.New("cluster: the worker rejected the stream request as malformed")
)

// WriteStreamRequest sends the opening frame naming what the stream is for.
//
// An empty tag is refused here rather than on the wire, because the worker
// would answer it with ErrStreamRequestInvalid and the caller would learn a
// round trip later what it could have been told at once.
func WriteStreamRequest(w io.Writer, tag, target string) error {
	if tag == "" {
		return fmt.Errorf("writing a tunnel stream request: empty tag")
	}
	if strings.Contains(tag, streamRequestSeparator) {
		// The separator is a single space and the split is on the FIRST one, so
		// a tag containing a space would silently move part of itself into the
		// target.
		return fmt.Errorf("writing a tunnel stream request: tag %q contains a space", tag)
	}
	return writeFrame(w, tag+streamRequestSeparator+target)
}

// ReadStreamRequest reads the opening frame. The target is empty when the tag
// carries no argument.
//
// A malformed frame is returned as an ordinary error, NOT as
// ErrStreamRequestInvalid: that sentinel is what a worker SENDS to describe a
// refusal, and a reader that produced it here would leave a caller unable to
// tell "the peer refused my request" from "I could not read the peer's".
func ReadStreamRequest(r io.Reader) (tag, target string, err error) {
	payload, err := readFrame(r)
	if err != nil {
		return "", "", fmt.Errorf("reading a tunnel stream request: %w", err)
	}
	tag, target, _ = strings.Cut(payload, streamRequestSeparator)
	if tag == "" {
		return "", "", fmt.Errorf("reading a tunnel stream request: empty tag")
	}
	return tag, target, nil
}

// WriteStreamAccepted tells the frontend the stream is now carrying the
// tunnelled protocol. Everything after this frame belongs to that protocol.
func WriteStreamAccepted(w io.Writer) error {
	return writeFrame(w, replyAccepted)
}

// WriteStreamRefusal reports why a stream will not be served. The caller closes
// the stream afterwards; this only says why.
//
// An unrecognised reason is sent as bad-request with its text attached rather
// than being dropped, because a refusal a frontend cannot read is
// indistinguishable from a worker that hung up, and those are different
// problems.
func WriteStreamRefusal(w io.Writer, reason error) error {
	code := replyCodeBadRequest
	switch {
	case errors.Is(reason, ErrStreamTagUnknown):
		code = replyCodeUnknownTag
	case errors.Is(reason, ErrStreamTargetUnavailable):
		code = replyCodeUnavailable
	case errors.Is(reason, ErrStreamRequestInvalid):
		code = replyCodeBadRequest
	}

	text := ""
	if reason != nil {
		text = strings.Map(func(r rune) rune {
			// The frame is length-prefixed so a newline would not corrupt it,
			// but this text reaches a log line on the far side and a cause
			// spanning lines is what makes one unsearchable.
			if r == '\n' || r == '\r' {
				return ' '
			}
			return r
		}, reason.Error())
	}
	frame := replyPrefixRefused + code + streamRequestSeparator + text
	return writeFrame(w, truncateRunes(frame, maxTunnelFrame))
}

// ReadStreamReply reads the worker's answer. nil means the stream is now
// carrying the tunnelled protocol.
//
// A failure to READ the reply is returned as itself, never as one of the
// refusal sentinels. The distinction is the point of this function: a refusal
// means the worker is connected and said no, while a read failure means the
// tunnel broke, and a caller that treated the second as the first would report
// a dead link as a policy decision.
func ReadStreamReply(r io.Reader) error {
	payload, err := readFrame(r)
	if err != nil {
		return fmt.Errorf("reading a tunnel stream reply: %w", err)
	}
	if payload == replyAccepted {
		return nil
	}
	rest, ok := strings.CutPrefix(payload, replyPrefixRefused)
	if !ok {
		return fmt.Errorf("reading a tunnel stream reply: unrecognised reply %q", payload)
	}
	code, text, _ := strings.Cut(rest, streamRequestSeparator)
	switch code {
	case replyCodeUnknownTag:
		return fmt.Errorf("%w: %s", ErrStreamTagUnknown, text)
	case replyCodeUnavailable:
		return fmt.Errorf("%w: %s", ErrStreamTargetUnavailable, text)
	case replyCodeBadRequest:
		return fmt.Errorf("%w: %s", ErrStreamRequestInvalid, text)
	default:
		// A code from a newer worker. Reported as an error carrying the code
		// rather than mapped onto the nearest known one, so a frontend does not
		// retry forever against a refusal that means something else entirely.
		return fmt.Errorf("tunnel stream refused with unrecognised code %q: %s", code, text)
	}
}

// truncateRunes cuts s to at most limit BYTES, on a rune boundary.
//
// A plain slice would cut mid-rune and put a lone continuation byte on the
// wire. Nothing breaks: the frame is length-prefixed so the framing survives,
// and the reader's string() tolerates invalid UTF-8. What it costs is the
// far side's log line ending in a replacement character, and a refusal reason
// exists to be read by a person, so it should not arrive damaged.
//
// The code that reaches this is always short; only a cause from a local service
// can be long enough to matter.
func truncateRunes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	// utf8.RuneStart finds the first byte of a rune. Walking back from the
	// limit lands on the start of the rune that would have been split, and at
	// most 3 steps are needed since a UTF-8 rune is at most 4 bytes.
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// writeFrame writes one length-prefixed frame in a single Write.
//
// One Write, not two: the underlying stream is a yamux stream whose writes
// become discrete data frames, and splitting the length from the payload would
// put the reader one frame away from a header for no reason. It also keeps the
// adapter in wsconn.go to one WebSocket message per frame.
func writeFrame(w io.Writer, payload string) error {
	if len(payload) > maxTunnelFrame {
		return fmt.Errorf("tunnel frame is %d bytes, over the %d-byte limit", len(payload), maxTunnelFrame)
	}
	buf := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(buf[:2], uint16(len(payload)))
	copy(buf[2:], payload)
	_, err := w.Write(buf)
	return err
}

// readFrame reads one length-prefixed frame.
//
// io.ReadFull rather than Read: a yamux stream returns whatever has arrived,
// and a header split across two data frames is ordinary rather than
// exceptional. It also converts a truncated frame into io.ErrUnexpectedEOF,
// which is what a peer that hung up mid-header should look like.
func readFrame(r io.Reader) (string, error) {
	var size [2]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return "", err
	}
	n := binary.BigEndian.Uint16(size[:])
	if int(n) > maxTunnelFrame {
		return "", fmt.Errorf("tunnel frame declares %d bytes, over the %d-byte limit", n, maxTunnelFrame)
	}
	if n == 0 {
		return "", nil
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return "", err
	}
	return string(payload), nil
}
