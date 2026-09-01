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
	// is closed exactly once, here and nowhere else, which keeps the error
	// below meaningful: a second Close of a yamux stream that was reset
	// returns the error that killed it, and Splice would have no way to tell
	// that from a fresh failure.
	closeErrA := a.Close()
	closeErrB := b.Close()

	// Wait for the second direction so no copy is still touching either stream
	// once Splice has returned. This is load-bearing: it assumes Close unblocks
	// a copy parked in Read or Write, and a stream where that is false hangs
	// here rather than leaking a goroutine. The two stream types this is built
	// for satisfy it: net.Conn does, and so does go-yamux/v5, whose Close sets
	// readErr and calls notifyWaiting to wake a parked Read while a parked
	// Write returns ErrStreamClosed. Nothing outside this package's own specs
	// calls Splice yet, so a phase 2 caller relaying over anything else has to
	// check this property rather than assume it.
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
// rather than broken: net.ErrClosed is what a socket reports once it or its
// peer has been closed, and io.ErrClosedPipe is the same condition on an
// in-memory pipe.
//
// io.EOF is deliberately absent. A clean read-side EOF never gets this far,
// because io.Copy consumes it and reports nil, and neither *yamux.Stream nor
// *net.TCPConn takes a WriteTo/ReadFrom path that would hand one back. So a
// bare io.EOF arriving here came from a failing Write or Close, where it means
// the peer is gone, and yamux produces exactly that when a Write races its
// session's shutdown (see isMuxSessionFailure).
//
// A socket-level abort (ECONNRESET, EPIPE) is deliberately absent too, which
// makes the same underlying event, a peer aborting mid-stream, reach the caller
// as nil over a yamux tunnel and as an error over a raw socket. That asymmetry
// is intended: the yamux endings are the teardown Splice's own Close provokes,
// so this primitive is the only thing that can tell them from a fault, whereas
// whether an aborted request is routine or a failure is the relay's policy and
// only the relay knows which request was abandoned.
//
// The mux checks run first, and that ordering is load-bearing: a dying yamux
// session usually hands every live stream its own cause wrapped up
// (session.go:330), and that cause is routinely a closed-socket error, so
// consulting the generic endings first would report a peer that vanished
// mid-request as a clean completion.
func normalizeStreamErr(err error) error {
	if err == nil {
		return nil
	}
	if isMuxSessionFailure(err) {
		return err
	}
	if isMuxStreamTeardown(err) {
		return nil
	}
	if errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}

// normalGoAwayCode is yamux's "no error" go-away code, read off a sentinel
// declared with it because the constant itself is unexported.
var normalGoAwayCode = yamux.ErrRemoteGoAway.ErrorCode

// isMuxSessionFailure reports whether err is the yamux session underneath a
// stream dying, as opposed to a single stream being torn down. The distinction
// matters because Splice must stay quiet about the teardown it provokes itself
// while still reporting a dead peer: a keepalive timeout, a broken TCP
// connection or a protocol error under a relayed request has to reach the
// caller, or a failed inference looks like a finished one.
func isMuxSessionFailure(err error) bool {
	// A go-away ends the whole session. Only the "no error" code is a normal
	// ending; a protocol or internal error go-away is a real failure.
	var goAway *yamux.GoAwayError
	if errors.As(err, &goAway) {
		return goAway.ErrorCode != normalGoAwayCode
	}
	// A stream error is scoped to one stream, whatever killed it.
	var streamErr *yamux.StreamError
	if errors.As(err, &streamErr) {
		return false
	}
	// Session.close gives every stream it kills ErrStreamReset wrapped around
	// the cause, so the bare sentinel means this stream was reset and a
	// wrapped one means the session died under it. Identity is what separates
	// them; errors.Is cannot.
	//
	// Wrapped is not the only way a dead session shows up, though. close()
	// publishes shutdownErr and closes shutdownCh before it force-closes the
	// streams, so a Write or Close landing in that window gets the raw cause
	// back instead (session.go:507-510, 528-533). That form is unrecognisable
	// as yamux at all, which is why normalizeStreamErr no longer forgives a
	// bare io.EOF: for a peer that vanished, the raw cause is precisely io.EOF.
	return errors.Is(err, yamux.ErrStreamReset) && err != yamux.ErrStreamReset
}

// isMuxStreamTeardown reports whether err is yamux ending one stream, which
// Splice mostly provokes itself: closing a stream whose session has already
// shut down normally returns ErrSessionShutdown from the FIN write, and a copy
// parked on a stream that gets closed comes back with ErrStreamClosed or a
// reset. A reset does not only mean that, though; the same sentinel heads the
// error a dying session hands its streams, which is why isMuxSessionFailure
// runs first. None of yamux's error types match net.ErrClosed, so all of this
// has to be recognised by shape.
func isMuxStreamTeardown(err error) bool {
	// Sentinels by identity, never errors.Is: the wrapped forms belong to a
	// dead session and are reported instead. ErrSessionShutdown is absent on
	// purpose rather than by oversight, being a *GoAwayError carrying the
	// normal code, which the last check below covers.
	if err == yamux.ErrStreamClosed || err == yamux.ErrStreamReset {
		return true
	}
	var streamErr *yamux.StreamError
	if errors.As(err, &streamErr) {
		return true
	}
	var goAway *yamux.GoAwayError
	return errors.As(err, &goAway) && goAway.ErrorCode == normalGoAwayCode
}
