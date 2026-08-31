package cluster

import (
	"errors"
	"io"
	"net"
)

// Splice joins two streams and copies bytes between them in both directions
// until one direction finishes, then closes both so the other unblocks and
// returns. It is the primitive under the inter-replica relay and the worker
// tunnel, so it carries gRPC: both directions can be live at once and either
// peer may speak first, which is why the copies run concurrently. A sequential
// io.Copy then io.Copy would deadlock waiting for a request on a stream whose
// far side is waiting for a response.
//
// The error reported is the one from the direction that finished first, with
// the endings that mean "someone closed" mapped to nil. The other direction's
// error is discarded: by then it is only an echo of the Close done here.
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

	// Wait for the second direction so no copy is still touching either
	// stream once Splice has returned.
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
// what a socket or yamux stream reports once it or its peer has been closed,
// and io.ErrClosedPipe is the same condition on an in-memory pipe. Anything
// else is a real failure the caller should see.
func normalizeStreamErr(err error) error {
	if err == nil ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}
