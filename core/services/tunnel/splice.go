package tunnel

import (
	"errors"
	"io"
	"net"
	"sync"

	"github.com/libp2p/go-yamux/v5"
)

// writeCloser is the half-close that *net.TCPConn and a yamux stream have.
type writeCloser interface {
	CloseWrite() error
}

// Splice joins two streams. It copies in both directions at the same time,
// because either side can speak first and gRPC has both directions live at
// once. Two copies in sequence would wait for a request on a stream whose other
// end waits for a response.
//
// When a direction reaches its end, Splice passes the end on with CloseWrite
// to the stream that the direction wrote to, and it goes on copying the other
// direction until that one ends as well. A protocol that closes its sending side
// and then waits for a reply, such as a request that ends in a half-close, so
// keeps its reply. If the stream cannot half-close, or the direction failed,
// Splice closes both streams at once. That is the only way to wake a copy that
// is parked in a read or in a write, and it is what the protocols that end a
// stream in both directions together (gRPC, HTTP/2) need when a peer is gone.
//
// Splice closes each stream exactly once and waits for both copies before it
// returns, so no copy touches a stream after the return. The wait assumes that
// Close wakes a copy that is parked in Read or Write. A net.Conn and a yamux
// stream do that. Check this before using another type.
//
// The result is the first failure of a direction. An ending that means "someone
// closed" is not a failure. A result therefore means "one direction failed" and
// never "only this failed".
func Splice(a, b io.ReadWriteCloser) error {
	results := make(chan copyResult, 2)
	go func() { results <- copyStream(b, a) }()
	go func() { results <- copyStream(a, b) }()

	first := <-results
	if first.halfClosed {
		// The end was passed on. The other direction ends when the peer closes
		// its side, and that is its own result and not an echo of a close by
		// Splice.
		//
		// It can also be parked in a read of a stream that nobody will write to
		// again, because the session under the other stream ended. Nothing wakes
		// that read, and the streams would stay open for ever. So the end of
		// either session closes both streams.
		done := make(chan struct{})
		defer close(done)
		select {
		case second := <-results:
			closeErrA := a.Close()
			closeErrB := b.Close()
			if second.err != nil {
				return second.err
			}
			return firstCloseFailure(closeErrA, closeErrB)
		case <-watchSessions(done, a, b):
			_ = a.Close()
			_ = b.Close()
			<-results
			return errSessionEnded
		}
	}

	// Each stream is closed here and nowhere else. A second Close of a yamux
	// stream that was reset returns the error that killed it, and Splice could
	// not tell that from a new failure.
	closeErrA := a.Close()
	closeErrB := b.Close()

	// The other direction ends because of the Close above. Its result is an
	// echo of that Close, so it is dropped, and the failure of the first
	// direction is the one to report. It can also be a real failure that lost
	// the race, which is why a result means "one direction failed" and never
	// "only this failed".
	<-results

	if first.err != nil {
		return first.err
	}
	return firstCloseFailure(closeErrA, closeErrB)
}

// firstCloseFailure returns the first Close that failed on a stream that was
// healthy. A close of a stream that is already dead is not a failure.
func firstCloseFailure(errA, errB error) error {
	if err := normalizeStreamErr(errA); err != nil {
		return err
	}
	return normalizeStreamErr(errB)
}

// errSessionEnded is the result of a Splice whose session ended while one
// direction was still open, so the request was cut.
var errSessionEnded = errors.New("tunnel: the session ended while a stream was half-closed")

// sessionOf is implemented by the streams of a session.
type sessionOf interface {
	Session() *yamux.Session
}

// watchSessions returns a channel that is closed when the session of one of the
// streams ends. It stops watching when done is closed, so that the goroutines do
// not outlive the Splice for a session that goes on. A stream that does not
// belong to a session gives no signal.
func watchSessions(done <-chan struct{}, streams ...io.ReadWriteCloser) <-chan struct{} {
	ended := make(chan struct{})
	var once sync.Once
	for _, st := range streams {
		s, ok := st.(sessionOf)
		if !ok {
			continue
		}
		gone := s.Session().CloseChan()
		go func() {
			select {
			case <-gone:
				once.Do(func() { close(ended) })
			case <-done:
			}
		}()
	}
	return ended
}

// copyResult is the end of one direction. halfClosed is true when the end was
// passed on to the destination with CloseWrite.
type copyResult struct {
	err        error
	halfClosed bool
}

// copyStream moves one direction and reports only failures of the transport.
func copyStream(dst io.Writer, src io.Reader) copyResult {
	_, err := io.Copy(dst, src)
	if err != nil {
		// A read that ends because a stream was closed or reset is not the end
		// of the data. The peer may never close its side, so Splice must close
		// both streams and not wait for it.
		return copyResult{err: normalizeStreamErr(err)}
	}
	cw, ok := dst.(writeCloser)
	if !ok {
		return copyResult{}
	}
	if err := normalizeStreamErr(cw.CloseWrite()); err != nil {
		return copyResult{err: err}
	}
	return copyResult{halfClosed: true}
}

// normalizeStreamErr drops the endings that mean that the conversation is over
// and not broken: net.ErrClosed, which a socket reports after it or its peer
// closed, and io.ErrClosedPipe, which is the same on an in-memory pipe.
//
// io.EOF is not in the list. io.Copy consumes a clean end of a read and reports
// nil, so an io.EOF that arrives here came from a Write or a Close that failed,
// and it means that the peer is gone. yamux gives exactly that when a Write
// races the shutdown of its session.
//
// A reset of a socket (ECONNRESET, EPIPE) is not in the list either, so the
// same event, a peer that aborts in the middle of a stream, is an error on a
// raw socket and, through muxVerdict, on a yamux stream.
//
// The verdict of the multiplexer comes first. A session that dies gives every
// live stream its own cause, and that cause is often a closed-socket error, so
// the generic endings would report a peer that vanished as a clean end.
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
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}

// normalGoAwayCode is the code of a go-away that reports no error. yamux does
// not export the constant, so it is read from a sentinel that has it.
var normalGoAwayCode = yamux.ErrRemoteGoAway.ErrorCode

// muxVerdict classifies an ending that comes from yamux. recognised says that
// the error is from the multiplexer. report says that the ending was forced on
// this stream and was not asked for by this side. The policy turns on one bit,
// Remote, and it is in one function so that a test can show each read of it.
//
//   - A reset of the stream by the peer is reported. For a request that is
//     relayed it means that the response is cut. A caller decides how loud that
//     is: a client that cancels causes one for each cancel.
//   - A go-away from the peer is reported. It is graceful for the session, but
//     every stream on the session was in the middle of a request.
//   - The same endings caused by this side are silent. ErrSessionShutdown is
//     what this process gets when it closes its own session.
//
// A reset that arrives on a frame type other than a window update gives the bare
// ErrStreamReset sentinel, which is treated as the teardown of this side. Every
// reset that go-yamux sends uses a window update, so only a foreign
// implementation of the protocol can reach that case.
func muxVerdict(err error) (recognised, report bool) {
	var goAway *yamux.GoAwayError
	if errors.As(err, &goAway) {
		return true, goAway.Remote || goAway.ErrorCode != normalGoAwayCode
	}
	var streamErr *yamux.StreamError
	if errors.As(err, &streamErr) {
		return true, streamErr.Remote
	}
	// These sentinels are compared by identity and not with errors.Is. They are
	// the endings that Splice causes itself: a Write on a stream that was closed
	// gives ErrStreamClosed, and a Read that was parked when CloseRead ran gives
	// the bare ErrStreamReset.
	//
	// A copy into a *net.TCPConn wraps the error in a net.OpError, so the layer
	// is removed before the comparison.
	for {
		opErr, ok := err.(*net.OpError)
		if !ok {
			break
		}
		err = opErr.Err
	}
	if err == yamux.ErrStreamClosed || err == yamux.ErrStreamReset {
		return true, false
	}
	// The same sentinel wrapped is another thing: Session.close gives every
	// stream that it kills ErrStreamReset around the cause. The session died
	// under a live stream. Identity above is what tells the two apart.
	if errors.Is(err, yamux.ErrStreamReset) {
		return true, true
	}
	return false, false
}
