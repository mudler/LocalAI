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
// session's shutdown (see muxVerdict).
//
// A socket-level abort (ECONNRESET, EPIPE) is deliberately absent too, which
// makes the same underlying event, a peer aborting mid-stream, reach the caller
// as nil over a raw socket where it would be an error. That asymmetry is
// narrower than it was, since a yamux abort is now reported (see muxVerdict),
// and what remains of it is that a raw socket cannot say who aborted.
//
// The mux verdict is consulted first, and that ordering is load-bearing: a
// dying yamux session usually hands every live stream its own cause wrapped up
// (go-yamux/v5@v5.1.0 session.go, Session.close), and that cause is routinely a
// closed-socket error, so consulting the generic endings first would report a
// peer that vanished mid-request as a clean completion.
func normalizeStreamErr(err error) error {
	if err == nil {
		return nil
	}
	if recognised, report := muxVerdict(err); recognised {
		if report {
			return err
		}
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

// muxVerdict classifies a yamux ending. recognised says the error came from the
// multiplexer at all; report says the ending was INFLICTED on this stream
// rather than asked for by this side.
//
// It is ONE function, and that is the point rather than a matter of taste. The
// policy below turns on a single bit, Remote, and an earlier shape read that
// bit in two predicates with a report-by-default fallthrough behind them.
// Reverting either read left the whole suite green, because the error reached
// the same answer down the other path: the classifier could not be
// mutation-tested in pieces, which in code whose correctness argument IS its
// mutation evidence is worse than the duplication it bought. Here each type is
// decided once, so falsifying either read reddens a spec.
//
// The distinction it draws is what keeps Splice quiet about the teardown it
// provokes itself while still reporting a request that died: a keepalive
// timeout, a broken connection, a peer that reset the stream or a peer that
// went away under a relayed request has to reach the caller, or a failed
// inference looks like a finished one.
//
// TWO OF THESE ARE THE POLICY PHASE 1 LEFT OPEN, and this is where they are
// settled, by the relay in core/services/cluster/relay.go, which is Splice's
// first production caller. Both used to be reported as normal termination.
// Neither can be settled by a caller reading Splice's result, because a result
// mapped to nil carries nothing left to reclassify, so the decision has to live
// at the classifier; the two callers there are the relay and the worker tunnel,
// and both are splicing an in-flight request, so both want the same answer.
//
//  1. A peer-initiated stream reset, *StreamError{Remote: true}. yamux builds
//     it in processFlags when an RST frame arrives on the stream
//     (stream.go:432-449); a reset this side asked for carries Remote: false
//     instead (stream.go:283-291), and Splice never resets anything anyway, its
//     own Close sending a FIN (stream.go:303-331, 365-368). So Remote: true is
//     unambiguously "the far side aborted this stream", which for a relayed
//     request means the response was truncated. REPORTED. The caller decides
//     how loud that is: a client cancelling produces one per cancellation, so
//     the relay logs it at debug rather than treating it as a fault.
//
//  2. A graceful go-away from the peer, ErrRemoteGoAway. handleGoAway returns
//     it for code goAwayNormal (session.go:829-833), recv closes the session
//     with it, and close hands it UNWRAPPED to every live stream, because it
//     already is a *GoAwayError and so escapes the ErrStreamReset wrapping
//     (session.go:328-337, stream.go:371-387). Graceful describes the SESSION,
//     not the requests on it: every one of those streams was mid-request.
//     REPORTED, for the same reason as above.
//
// The locally-initiated forms of both stay silent, and keying on Remote is what
// separates them: ErrSessionShutdown is a *GoAwayError with Remote: false
// (const.go:96) and is exactly what this process closing its own session
// produces (session.go:284).
//
// The rule is "a remote reset is reported" and not "every remote reset is
// reported". yamux only builds a *StreamError when the RST rides a
// typeWindowUpdate frame (stream.go:436); an RST on any other frame type
// yields the BARE ErrStreamReset sentinel, which is claimed below as this
// side's own teardown and silenced. Every reset go-yamux itself sends uses
// typeWindowUpdate, so the gap is unreachable between two LocalAI processes and
// only a foreign multiplexer implementation could reach it.
//
// What this does NOT do is make the far side see a failure. Splice ends both
// streams with Close, which is a FIN, and a reset after that is a no-op because
// Close has already moved the stream to streamFinished (stream.go:266-272,
// 303-331, 336-361). Propagating a truncation as an RST would mean reshaping
// Splice's teardown, and it buys little: the protocols relayed here are gRPC
// and HTTP, both of which detect a body that ended without its trailers or its
// final chunk. Reporting is what the caller needs and this is where it comes
// from.
func muxVerdict(err error) (recognised, report bool) {
	// A go-away ends the whole session. Only a normal-code go-away this side
	// sent is a normal ending.
	var goAway *yamux.GoAwayError
	if errors.As(err, &goAway) {
		return true, goAway.Remote || goAway.ErrorCode != normalGoAwayCode
	}
	// A stream error is scoped to one stream. Only a reset this side asked for
	// is a normal ending.
	var streamErr *yamux.StreamError
	if errors.As(err, &streamErr) {
		return true, streamErr.Remote
	}
	// Sentinels by identity, never errors.Is, and before the wrapped check
	// below: these are the endings Splice provokes itself. Closing a stream
	// whose session has already shut down normally returns ErrSessionShutdown
	// from the FIN write, which the go-away branch above has already claimed;
	// a copy parked on a stream that gets closed comes back with
	// ErrStreamClosed from a Write (stream.go:157-159) or the bare
	// ErrStreamReset from a Read, which is what CloseRead installs
	// (stream.go:348-349).
	if err == yamux.ErrStreamClosed || err == yamux.ErrStreamReset {
		return true, false
	}
	// The same sentinel WRAPPED means something else entirely: Session.close
	// gives every stream it kills ErrStreamReset wrapped around the cause, so
	// this is the session dying under a live stream. Identity above is what
	// separates the two; errors.Is cannot.
	//
	// Wrapped is not the only way a dead session shows up, though. close()
	// publishes shutdownErr and closes shutdownCh before it force-closes the
	// streams, so a Write or Close landing in that window gets the raw cause
	// back instead (session.go:305-308, 528-533). That form is unrecognisable
	// as yamux at all, and is why it is left unrecognised here rather than
	// guessed at: normalizeStreamErr no longer forgives a bare io.EOF, because
	// for a peer that vanished the raw cause is precisely io.EOF.
	if errors.Is(err, yamux.ErrStreamReset) {
		return true, true
	}
	return false, false
}
