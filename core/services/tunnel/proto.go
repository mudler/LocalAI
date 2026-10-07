// Package tunnel carries the traffic between frontends and a worker over one
// outbound connection of the worker.
//
// The worker dials a frontend and opens a websocket. A yamux session runs on
// that websocket. The frontend opens a stream for every request and the worker
// answers it, so the worker needs no inbound port. This package holds the wire
// format, the session, the registry of the sessions a frontend holds, and the
// helpers that join two streams. It is the only package, with its callers in the
// connect endpoint and the worker, that imports the multiplexer.
package tunnel

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// The first thing on every stream is a request frame. A yamux stream carries no
// destination, so the frame names a tag (which local service of the worker) and
// a target (which instance of it). The worker answers with a reply frame before
// either side speaks the carried protocol.
//
// The reply is always sent, not only on failure. The carried protocols speak
// from the client side first (gRPC sends a preface, HTTP sends a request line),
// so a reply sent only on failure would arrive between the bytes of a response
// on the streams that worked, and the frontend would not know when to read it.
//
// A frame has a 2-byte length. The reader takes exactly the frame and nothing
// after it, because the stream goes to gRPC or net/http next and a reader that
// buffered too much would take the first bytes of their conversation.

const (
	// StreamTagGRPC sends a stream to a backend process of the worker. The
	// target is the address of the backend. The worker keeps only the port.
	StreamTagGRPC = "grpc"

	// StreamTagHTTP sends a stream to the HTTP server of the worker, which
	// serves file staging and backend logs. The target is ignored: a worker has
	// one such server and only the worker knows where it listens.
	StreamTagHTTP = "http"
)

// maxFrame bounds a frame. The longest real frame is a tag and an address, under
// a hundred bytes. The bound stops a peer that declares a large length and then
// sends nothing from making the reader allocate.
const maxFrame = 1024

// The reply codes go on the wire as strings, so a frontend that reads a code it
// does not know can still log something an operator can search for.
const (
	replyAccepted          = "ok"
	replyCodeUnknownTag    = "unknown-tag"
	replyCodeUnavailable   = "unavailable"
	replyCodeBadRequest    = "bad-request"
	replyCodeNotServed     = "not-served"
	replyPrefixRefused     = "err "
	streamRequestSeparator = " "
)

// The four refusals a worker can send. They stay apart because a caller acts on
// each in a different way. A caller gives up on an unknown tag, because it does
// not change until the worker is upgraded. It acts on an unavailable target,
// which is what a backend that died looks like from inside the worker. It
// reports a bad request, which is a bug of the frontend. It does nothing about
// "not served", which says that the worker learned nothing.
//
// None of them wraps an error about absence of the node. A refusal proves that
// the worker is connected and that it answered.
var (
	ErrStreamTagUnknown        = errors.New("tunnel: the worker does not serve that stream tag")
	ErrStreamTargetUnavailable = errors.New("tunnel: the worker could not reach the local service for that stream")
	ErrStreamRequestInvalid    = errors.New("tunnel: the worker rejected the stream request as malformed")

	// ErrStreamNotServed means that the worker could not serve the stream for a
	// reason of its own, and that the reason says nothing about the backend that
	// the stream named. IsWorkerAnswer does not count it as evidence, so it
	// reaches a consumer as a routing failure and nothing is reaped.
	//
	// A frontend that does not know this code reads it as an unrecognised reply,
	// which ReadStreamReply returns as a plain error. A mixed deployment is
	// therefore safe without extra work.
	ErrStreamNotServed = errors.New("tunnel: the worker could not serve that stream, for a reason that is not about the backend")
)

// streamRefusals is the whole refusal vocabulary in one table. The writer, the
// reader, IsWorkerAnswer and IsStreamRefusal all read it, so a new refusal
// cannot be added to some of them and forgotten in the others.
//
// The writer takes the first entry that matches with errors.Is, so a reason that
// wraps two sentinels gives the same code every time.
//
// evidence says if a consumer may act on the code. Three codes are statements
// about a backend. ErrStreamNotServed is the worker saying that it learned
// nothing, and counting it as evidence would turn every worker-side failure that
// is temporary into an eviction.
var streamRefusals = []struct {
	sentinel error
	code     string
	evidence bool
}{
	{ErrStreamTagUnknown, replyCodeUnknownTag, true},
	{ErrStreamTargetUnavailable, replyCodeUnavailable, true},
	{ErrStreamRequestInvalid, replyCodeBadRequest, true},
	{ErrStreamNotServed, replyCodeNotServed, false},
}

// IsWorkerAnswer reports whether err is a refusal that a consumer may act on.
// It is true for the three codes that are statements about a backend and false
// for everything else, including ErrStreamNotServed, a failure to read the
// reply and an error of the transport.
func IsWorkerAnswer(err error) bool {
	for _, r := range streamRefusals {
		if r.evidence && errors.Is(err, r.sentinel) {
			return true
		}
	}
	return false
}

// IsStreamRefusal reports whether err already carries a classification of this
// vocabulary. It is a different question from IsWorkerAnswer. A worker that
// classifies an error again would overwrite a decision that was made closer to
// the failure.
func IsStreamRefusal(err error) bool {
	for _, r := range streamRefusals {
		if errors.Is(err, r.sentinel) {
			return true
		}
	}
	return false
}

// WriteStreamRequest sends the opening frame that names what the stream is for.
// An empty tag, or a tag with a space, is refused here. The worker would refuse
// it too, but the caller learns a round trip later.
func WriteStreamRequest(w io.Writer, tag, target string) error {
	if tag == "" {
		return errors.New("writing a tunnel stream request: empty tag")
	}
	if strings.Contains(tag, streamRequestSeparator) {
		// The split is on the first space, so a space in the tag would move a
		// part of the tag into the target.
		return fmt.Errorf("writing a tunnel stream request: tag %q contains a space", tag)
	}
	return writeFrame(w, tag+streamRequestSeparator+target)
}

// ReadStreamRequest reads the opening frame. The target is empty when the tag
// has no argument.
//
// A malformed frame is a plain error and not ErrStreamRequestInvalid. That
// sentinel is what a worker sends to describe a refusal, and a caller must be
// able to tell "the peer refused my request" from "I could not read the request
// of the peer".
func ReadStreamRequest(r io.Reader) (tag, target string, err error) {
	payload, err := readFrame(r)
	if err != nil {
		return "", "", fmt.Errorf("reading a tunnel stream request: %w", err)
	}
	tag, target, _ = strings.Cut(payload, streamRequestSeparator)
	if tag == "" {
		return "", "", errors.New("reading a tunnel stream request: empty tag")
	}
	return tag, target, nil
}

// WriteStreamAccepted tells the frontend that the stream now carries the
// protocol. Everything after this frame belongs to that protocol.
func WriteStreamAccepted(w io.Writer) error {
	return writeFrame(w, replyAccepted)
}

// WriteStreamRefusal says why a stream is not served. The caller closes the
// stream after it.
//
// A reason with no known classification is sent as "not served" with its text,
// and not as a bad request. The other three codes are evidence that a frontend
// acts on, up to the removal of a model row. An error that arrives here with no
// classification was not classified by anyone, and it must not become a verdict
// by default.
func WriteStreamRefusal(w io.Writer, reason error) error {
	code := replyCodeNotServed
	for _, r := range streamRefusals {
		if errors.Is(reason, r.sentinel) {
			code = r.code
			break
		}
	}

	text := ""
	if reason != nil {
		text = strings.Map(func(r rune) rune {
			// A new line would not break the frame, which has a length. The
			// text goes to a log line on the other side, and a cause on several
			// lines is hard to search.
			if r == '\n' || r == '\r' {
				return ' '
			}
			return r
		}, reason.Error())
	}
	frame := replyPrefixRefused + code + streamRequestSeparator + text
	return writeFrame(w, truncateRunes(frame, maxFrame))
}

// ReadStreamReply reads the answer of the worker. A nil result means that the
// stream now carries the protocol.
//
// A failure to read the reply is returned as itself and never as a refusal. A
// refusal means that the worker is connected and said no. A failed read means
// that the tunnel broke, and a caller that took one for the other would report
// a dead link as a decision of the worker.
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
	for _, r := range streamRefusals {
		if code == r.code {
			return fmt.Errorf("%w: %s", r.sentinel, text)
		}
	}
	// A code from a newer worker. It is not mapped onto the nearest known code,
	// so that a frontend does not retry for ever against a refusal with another
	// meaning, and so that IsWorkerAnswer is false and nothing is reaped.
	return fmt.Errorf("tunnel stream refused with unrecognised code %q: %s", code, text)
}

// truncateRunes cuts s to at most limit bytes on a rune boundary. A plain slice
// could cut a rune in two and put a lone continuation byte on the wire.
func truncateRunes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	// A rune has at most 4 bytes, so this walks back at most 3 steps.
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// writeFrame writes a frame with one Write. The stream is a yamux stream, where
// a write becomes one data frame, and two writes would put the length and the
// payload in two frames. One Write also keeps the websocket adapter to one
// message for each frame.
func writeFrame(w io.Writer, payload string) error {
	if len(payload) > maxFrame {
		return fmt.Errorf("tunnel frame is %d bytes, over the %d-byte limit", len(payload), maxFrame)
	}
	buf := make([]byte, 2+len(payload))
	// #nosec G115 -- the check above limits the payload to maxFrame (1024),
	// which is inside uint16.
	binary.BigEndian.PutUint16(buf[:2], uint16(len(payload)))
	copy(buf[2:], payload)
	_, err := w.Write(buf)
	return err
}

// readFrame reads a frame. It uses io.ReadFull because a yamux stream returns
// what has arrived, and a header in two data frames is normal. A frame that is
// cut short gives io.ErrUnexpectedEOF, which is what a peer that hung up in the
// middle of a header looks like.
func readFrame(r io.Reader) (string, error) {
	var size [2]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return "", err
	}
	n := binary.BigEndian.Uint16(size[:])
	if int(n) > maxFrame {
		return "", fmt.Errorf("tunnel frame declares %d bytes, over the %d-byte limit", n, maxFrame)
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
