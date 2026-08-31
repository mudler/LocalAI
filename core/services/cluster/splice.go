package cluster

import (
	"errors"
	"io"
	"net"

	"github.com/libp2p/go-yamux/v5"
)

// Splice joins two streams and copies bytes between them in both directions
// until one direction finishes, then closes both so the other unblocks and
// returns. It is the primitive under the inter-replica relay and the worker
// tunnel, so it carries gRPC: both directions can be live at once and either
// peer may speak first, which is why the copies run concurrently. A sequential
// io.Copy then io.Copy would deadlock waiting for a request on a stream whose
// far side is waiting for a response.
//
// EOF in one direction therefore truncates whatever is still in flight in the
// other. That is right for gRPC, HTTP/2 and yamux, which end a stream in both
// directions at once, but a future caller relaying raw TCP with a half-close
// would lose the response body still arriving after the request's CloseWrite.
//
// The error reported is the one from the direction that finished first, with
// the endings that mean "someone closed" mapped to nil. The other direction's
// error is dropped; most of the time it is an echo of the Close below, but it
// can also be a genuine failure that lost the race, so a Splice error means
// "one direction failed", never "only this failed".
func Splice(a, b io.ReadWriteCloser) error {
	errs := make(chan error, 2)
	go func() { errs <- copyStream(b, a) }()
	go func() { errs <- copyStream(a, b) }()

	first := <-errs

	// Closing both ends is what releases the other direction, whether it is
	// parked in Read or halfway through a Write nobody is draining. Each end
	// is closed exactly once, here and nowhere else, so a stream that reports
	// an error on a second Close (yamux does) never sees one.
	closeErrA := a.Close()
	closeErrB := b.Close()

	// Wait for the second direction so no copy is still touching either stream
	// once Splice has returned. This is load-bearing: it assumes Close unblocks
	// a copy parked in Read or Write, and a stream where that is false hangs
	// here rather than leaking a goroutine. Both callers satisfy it. net.Conn
	// does, and so does go-yamux/v5, whose Close sets readErr and calls
	// notifyWaiting to wake a parked Read while a parked Write returns
	// ErrStreamClosed.
	<-errs

	if first != nil {
		return first
	}
	// A close that fails on a stream that was otherwise healthy is worth
	// reporting; a close of an already-dead stream is not.
	if err := normalizeStreamErr(closeErrA); err != nil {
		return err
	}
	return normalizeStreamErr(closeErrB)
}

// copyStream moves one direction and reports only genuine transport failures.
func copyStream(dst io.Writer, src io.Reader) error {
	_, err := io.Copy(dst, src)
	return normalizeStreamErr(err)
}

// normalizeStreamErr drops the endings that mean the conversation is over
// rather than broken. io.EOF is the clean end of a stream, net.ErrClosed is
// what a socket reports once it or its peer has been closed, and
// io.ErrClosedPipe is the same condition on an in-memory pipe.
//
// The yamux sentinels are here because Splice owns the Close that produces
// them: closing a stream whose session has already gone away returns
// ErrSessionShutdown from the FIN write, and a stream torn down under a live
// copy surfaces as ErrStreamClosed or a reset. None of yamux's error types
// match net.ErrClosed, so each has to be named. Matching ErrStreamReset covers
// every *StreamError and *GoAwayError, both of which report themselves as a
// reset; ErrStreamClosed is a plain sentinel and matches only itself.
//
// Anything else is a real failure the caller should see. Note that a peer
// resetting a socket mid-stream (ECONNRESET, EPIPE) is deliberately not in
// this set: whether an abandoned request is routine is the caller's policy,
// not this primitive's.
func normalizeStreamErr(err error) error {
	if err == nil ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, yamux.ErrStreamClosed) ||
		errors.Is(err, yamux.ErrStreamReset) ||
		errors.Is(err, yamux.ErrSessionShutdown) {
		return nil
	}
	return err
}
